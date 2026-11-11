# RetroArch Marketplace specification

## Purpose and scope

The application serves a user's local game collection to RetroArch and provides a local web interface and command line for managing the collection. It does not provide or host game files. The supported user workflows are adding games (by scanning folders or through the admin interface), browsing the published catalog, and downloading game archives through the catalog API or RetroArch Content Downloader.

This document records the current product contract inferred from [README.md](README.md), [openapi.yaml](openapi.yaml), and the implementation; it is not a recovered statement of the original author's intent. The OpenAPI document defines the public HTTP response schemas, parameters, and listed status codes. If this document and OpenAPI disagree about a public endpoint, the discrepancy must be resolved explicitly rather than assumed to be intentional.

## Domain

- The **catalog** is a SQLite database (`database` setting, default `catalog.db`) holding platforms and games. Source paths are relative to the database directory; archive paths are relative to the archive root. The server creates the database if it is absent; a missing parent directory is a fatal startup error.
- A **platform** has a unique `id`, a human-readable `name`, and a `libretro_name`. A game references a platform by its ID. Several platform IDs may share a `libretro_name`. A platform with games cannot be deleted.
- A **game** has a unique `id`, `canonical_name`, optional `display_name`, `platform_id`, and one or more ordered discs. Each disc has an `entrypoint` and a nonempty list of files, each with a source path and an archive path. Each disc's entrypoint must be one of its file paths; archive paths and the playlist must be unique across the game, and no such path may be an ancestor of another. Each game has a revision that increases on every update.
- A game with multiple discs requires a `playlist` path. When a playlist is specified, the server generates an M3U containing disc entrypoints in disc order, relative to the playlist directory. The playlist is the game card's entrypoint and is included in its files. Otherwise, the first disc entrypoint is used.
- A game is **available** only when all its sources are regular files. Availability is re-evaluated at startup, on each source scan (`scan_interval`), and on admin writes. Unavailable games are kept in the database and excluded from the published catalog. Source errors other than a missing file make the game unavailable and are logged.
- Paths and identifiers used in archives must satisfy the path-safety constraints in OpenAPI. Games with unknown fields or unsafe paths are rejected. Source files and disc data must never be modified by the application.

## Public catalog and downloads

The public API has no authentication and is described in [openapi.yaml](openapi.yaml). In particular:

The application catalog handlers (`internal/catalog/customapi.go`) and the RetroArch Content Downloader handlers (`internal/catalog/retroarch.go`) are separate modules in the same Go package. Both use the same catalog snapshot and package cache. The catalog card format remains compatible with the RetroArch catalog client; the `/assets/` handlers implement the standard Content Downloader interface.

1. `GET /api/games` returns available games as a JSON array in insertion order. `q` is a case-insensitive substring match on canonical and display names. Search and platform filters combine with AND; pagination is applied after filtering. Each card describes the exact files in its package.
2. `GET /api/platforms` returns all platforms, including platforms without available games, in insertion order. For repeated `libretro_name` values, only the first platform is returned.
3. A card's `download_url` resolves to a lazily generated, cached ZIP containing exactly the card's listed files. Generated playlists are included where applicable. Concurrent requests for one archive share its build; unrelated archives can build or download independently, and active archives are protected from cache cleanup.
4. The `/assets/cores/` indexes and ZIP endpoints support RetroArch Content Downloader. Asset ZIP contents are nested under a game subdirectory to avoid overwriting sibling games on extraction. Colons in game names are retained in archive and directory names; clients on filesystems that disallow colons may not be able to extract them.
5. Changes from admin writes or `scan` are reflected in the next catalog request. Source availability changes are reflected after the next source scan; downloading a game whose source disappeared before then fails.

Frontend asset bundle requests share a bounded upstream download; a failed download falls back to the existing placeholder and later requests may retry.

The HTTP server limits request-header reads to five seconds without imposing a response write timeout, allowing long archive downloads.

## Administration

- The web interface is available at `/admin/` and supports English and Russian. It lists and searches games (including unavailable ones), edits, adds, and deletes individual games, and adds or deletes platforms. The platform list displays the number of currently published games per platform.
- Admin requests require both a loopback client address and a loopback or `localhost` Host. `POST` and `PUT` additionally require an `Origin` matching the request scheme and Host and `Content-Type: application/json`; `DELETE` requires the matching `Origin`. Request bodies are limited to 1 MiB and unknown fields are rejected. Errors are plain text unless stated otherwise. Unsupported methods return `405` with `Allow`.
- Endpoints:

  | Method & path | Behavior |
  |---|---|
  | `GET /api/admin/known-platforms` | Built-in Libretro platform presets |
  | `GET /api/admin/platforms` | Platforms with the available `games` count and `total_games` across all availability states |
  | `POST /api/admin/platforms` | Create a platform: `201`; `409` duplicate ID; `400` invalid |
  | `DELETE /api/admin/platforms/{id}` | `204`; `404`; `409` when the platform has games |
  | `GET /api/admin/games?q=&platform_id=&skip=&count=` | Game summaries (`id`, `canonical_name`, `display_name`, `platform_id`, `available`, `revision`), including unavailable games; `count` defaults to 50 |
  | `GET /api/admin/games/{id}` | `{"game":…,"available":bool,"missing":[{"id","path"}]}` with `ETag` |
  | `POST /api/admin/games[?allow_missing=1]` | Create a game: `201` with `ETag` and `Location`; `409` duplicate ID; `400` invalid or unknown platform; `422` missing sources |
  | `PUT /api/admin/games/{id}[?allow_missing=1]` | Replace a game; `If-Match` required. `204` with the new `ETag`; `412` missing or stale revision; `404`; `400` invalid or body `id` differs from the path; `422` missing sources |
  | `DELETE /api/admin/games/{id}` | `If-Match` required; `204`; `412`; `404` |
  | `GET /api/admin/canonical-names?platform=<libretro_name>&q=<title>` | Up to ten ranked BoxArt names as a JSON array; `400` invalid parameters; `502` upstream errors |

- The `ETag` is the quoted game revision. Revisions are not reused when an ID is deleted and recreated. Upgrading a version 1 database invalidates existing game ETags once; editors must reload before saving. A write with a stale or missing `If-Match` returns `412` without changing the game; the revision is checked before sources.
- Missing sources require explicit approval: without `?allow_missing=1`, the server returns `422` with a JSON `missing` list of `{"id","path"}` and stores nothing. With approval, the game is stored and excluded from the catalog until its sources exist. Non-regular sources and other stat errors are rejected.
- The admin editor checks the canonical name against the platform's BoxArt listing as it is loaded or typed, and marks a name that is not an exact listing entry as "not in BoxArt" (RetroArch would find no cover art), offering similar names.
- Selecting a canonical-name candidate changes only the editor's value; it is not persisted until the game is saved.

## Canonical names

Canonical-name lookups use Libretro BoxArt listings, fetched per platform and cached in the database for 7 days. Expired listings are never served: they are refetched, and a failed fetch is an error. Expired listings are purged on each cleanup interval.

A title query ranks listing names by the title. A filename match compares word tokens of the file title (extension, disc marker, and `()`/`[]` tags removed; Roman numerals normalized; letters and digits split) with each name, keeps names with Jaccard similarity ≥ 0.6, and prefers higher similarity, then the region in the filename tags (`U`/`USA`, `E`/`Europe`, `J`/`Japan`), then USA, Europe, Japan, then shorter names.

## Command line

The `content-catalog` binary has the subcommands `serve`, `scan`, `find-name`, and `version`. Every subcommand accepts `-config`, `-listen`, `-database`, `-packages`, `-scan-interval`, `-cleanup-interval`, and `-cache-max-age` (see README for environment variables and defaults).

- `serve` runs the HTTP server and background maintenance.
- `scan [-platform <libretro_name>] [-dry-run] [-offline] <dir>` adds games found under `dir`, which must be inside the database directory (after resolving symlinks).
  - It skips the catalog database and its SQLite sidecars, dotfiles and dot-directories, symlinked directories, and non-game extensions (text, images, saves, configs, databases, playlists, `.dat`, `.log`, and similar).
  - The platform is `-platform` (which must name a database or built-in platform), or otherwise the first path component relative to the database directory equal to a `libretro_name` of a database or built-in platform. Files without one are skipped with `no platform`. A built-in platform is created only when an added game uses it.
  - A `.cue` and the files named by its `FILE` lines form one disc; a missing or outside reference skips the cue with `cue reference missing: <name>`. Other files are single-file discs.
  - Discs in one directory and platform with the same title and a disc marker (`(Disc N)`, `(Disc N of M)`, `_disc1`, ` CD2`, …) form one game ordered by disc number, with the playlist `<title>.m3u`.
  - Games with a source already in the database are skipped with `already in database`; games failing validation are skipped with the validation error.
  - The canonical name is the best BoxArt filename match (`name_source: "boxart"`), or the file title when there is no match, with `-offline`, or after a lookup error (`name_source: "filename"`). A lookup error is logged and disables further lookups for that platform in the scan.
  - IDs are slugs of the canonical name, with `-2`, `-3`, … appended on collision.
  - All games are written in one transaction; `-dry-run` writes nothing. Output is JSON: `{"added":[{"id","canonical_name","platform","name_source","files"}],"skipped":[{"path","reason"}]}`, where paths are relative to the database directory.
- `find-name -platform <libretro_name> (-q <title> | -file <path>)` prints a JSON array of candidates, using and filling the database cache. Exactly one of `-q` and `-file` is required. Invalid input exits with `2`; upstream, output, and database errors exit with `1`.
- `version` prints the version.

## Operation and safety

- The default listener is `127.0.0.1:5050`. Public access from other devices requires a suitable listen address and network configuration; the admin interface remains loopback-only. Configuration flags, environment variables, and defaults are listed in [README.md](README.md) and [config.example.toml](config.example.toml).
- Background maintenance re-evaluates source availability, removes expired generated cache archives, and purges expired BoxArt listings. It must not remove source files or a cached archive while it is being streamed.
- A release binary can include the web admin; Docker deployment mounts the user's data directory as described in the README.

## Acceptance scenarios

1. Given an available single-disc game, listing games returns its card, and downloading its package yields precisely the files listed in that card.
2. Given an available multi-disc game, the package includes its generated playlist and disc files; playlist entries refer to the discs in disc order.
3. Given two matching games on different platforms, filtering by one platform returns only that platform's games; `skip` and `count` apply to the filtered result.
4. Given a platform with no available games, it is still listed by `/api/platforms`, and the admin shows zero published games for it.
5. Creating a game with a missing source without approval returns `422` and stores nothing. Retrying with approval stores the game and excludes it from the catalog; when the file appears, the game is listed after the next source scan.
6. Given two editors using the same game revision, after one saves, the other's `PUT` returns `412` and does not overwrite the first save.
7. Given a non-loopback client or Host, admin access is denied. A cross-origin admin write is denied even from a loopback client.
8. Scanning `Sony - PlayStation/Tekken3.cue` when the BoxArt listing contains `Tekken 3 (USA)` stores the canonical name `Tekken 3 (USA)`; an unmatched `Foo Bar.bin` stores `Foo Bar`.
