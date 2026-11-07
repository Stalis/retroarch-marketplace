# RetroArch Marketplace specification

## Purpose and scope

The application serves a user's local game collection to RetroArch and provides a local web interface for managing the collection. It does not provide or host game files. The supported user workflows are maintaining a manifest, browsing the published catalog, and downloading game archives through the catalog API or RetroArch Content Downloader.

This document records the current product contract inferred from [README.md](README.md), [openapi.yaml](openapi.yaml), and the implementation; it is not a recovered statement of the original author's intent. The OpenAPI document defines the public HTTP response schemas, parameters, and listed status codes. If this document and OpenAPI disagree about a public endpoint, the discrepancy must be resolved explicitly rather than assumed to be intentional.

## Domain

- A **manifest** is the authoritative JSON document containing `platforms` and `contents` arrays. Source paths are relative to the manifest directory; archive paths are relative to the archive root.
- A **platform** has an `id`, a human-readable `name`, and a `libretro_name`. Platform IDs are unique in a manifest. A game references a platform by its ID. Several platform IDs may share a `libretro_name`.
- A **game** has a unique `id`, `canonical_name`, optional `display_name`, `platform_id`, and one or more ordered discs. Each disc has an `entrypoint` and a nonempty list of files, each with a source path and an archive path. Each disc's entrypoint must be one of its file paths; archive paths and the playlist must be unique across the game, and no such path may be an ancestor of another. Existing manifests with conflicting paths must be corrected before use.
- A game with multiple discs requires a `playlist` path. When a playlist is specified, the server generates an M3U containing disc entrypoints in disc order, relative to the playlist directory. The playlist is the game card's entrypoint and is included in its files. Otherwise, the first disc entrypoint is used.
- A game is **available** only when all its sources exist and are regular files. A game with missing sources may be retained with `defer_missing: true`; it is excluded from the published catalog until all sources become available. Missing sources on a game without that flag make the manifest invalid for serving. Other source errors and invalid paths remain errors.
- Paths and identifiers used in archives must satisfy the path-safety constraints in OpenAPI. The manifest must not contain duplicate JSON members, unknown fields, or unsafe paths. Source files and disc data must never be modified by the application.

## Public catalog and downloads

The public API has no authentication and is described in [openapi.yaml](openapi.yaml). In particular:

The application catalog handlers (`internal/catalog/customapi.go`) and the RetroArch Content Downloader handlers (`internal/catalog/retroarch.go`) are separate modules in the same Go package. Both use the same validated catalog snapshot and package cache. The catalog card format remains compatible with the RetroArch catalog client; the `/assets/` handlers implement the standard Content Downloader interface.

1. `GET /api/games` returns available games as a JSON array. Search and platform filters combine with AND; pagination is applied after filtering. Each card describes the exact files in its package.
2. `GET /api/platforms` returns declared platforms, including platforms without available games, in manifest order. For repeated `libretro_name` values, only the first platform is returned.
3. A card's `download_url` resolves to a lazily generated, cached ZIP containing exactly the card's listed files. Generated playlists are included where applicable. Concurrent requests for one archive share its build; unrelated archives can build or download independently, and active archives are protected from cache cleanup.
4. The `/assets/cores/` indexes and ZIP endpoints support RetroArch Content Downloader. Asset ZIP contents are nested under a game subdirectory to avoid overwriting sibling games on extraction.
5. An invalid manifest must not result in a stale successful catalog response. A valid manifest change or source availability change must be reflected in subsequent catalog requests.

Frontend asset bundle requests share a bounded upstream download; a failed download falls back to the existing placeholder and later requests may retry.

The HTTP server limits request-header reads to five seconds without imposing a response write timeout, allowing long archive downloads.

## Administration

- The web interface is available at `/admin/` and supports English and Russian. It allows users to edit the manifest, add platforms and games, and save changes. The platform list displays the number of currently published games per `libretro_name`.
- Admin requests require both a loopback client address and a loopback or `localhost` Host. A manifest update additionally requires an `Origin` matching the request scheme and Host, and `Content-Type: application/json`.
- `GET /api/admin/manifest` returns the raw manifest and its `ETag`. `PUT /api/admin/manifest` requires that revision in `If-Match`; a stale or missing revision returns `412` without replacing the file.
- Updates are validated before atomic replacement. Newly missing sources require explicit approval: the server returns `422` with a `missing` list; retrying with `?allow_missing=1` marks affected games `defer_missing: true`. Already approved missing sources remain permitted. Invalid input is rejected without replacing the manifest.
- A successful submission returns `204` and an `ETag` when the server does not modify it; a submission modified to mark missing games deferred returns `200`, the resulting manifest, and its `ETag`.

## Operation and safety

- The default listener is `127.0.0.1:5050`. Public access from other devices requires a suitable listen address and network configuration; the admin interface remains loopback-only. Configuration flags, environment variables, and defaults are listed in [README.md](README.md) and [config.example.toml](config.example.toml).
- Background maintenance checks sources and removes expired generated cache archives. It must not remove source files or a cached archive while it is being streamed.
- A release binary can include the web admin; Docker deployment mounts the user's data directory as described in the README.

## Acceptance scenarios

1. Given an available single-disc game, listing games returns its card, and downloading its package yields precisely the files listed in that card.
2. Given an available multi-disc game, the package includes its generated playlist and disc files; playlist entries refer to the discs in manifest order.
3. Given two matching games on different platforms, filtering by one platform returns only that platform's games; `skip` and `count` apply to the filtered result.
4. Given a platform with no available games, it is still listed by `/api/platforms`, and the admin shows zero published games for its `libretro_name`.
5. Given a newly missing source, saving without approval returns `422` and preserves the old manifest. Saving with approval retains the game but excludes it from the catalog; when the source becomes available, the game appears without another manifest edit.
6. Given two editors using the same revision, after one saves, the other's stale `If-Match` update returns `412` and does not overwrite the first save.
7. Given a non-loopback client or Host, admin access is denied. A cross-origin manifest update is denied even from a loopback client.
8. Given an invalid manifest, public catalog requests return an error rather than serving the prior snapshot.
