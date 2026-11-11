---
name: build-content-catalog
description: Add a user's local games to the RetroArch Content Catalog database with `content-catalog scan`, preserving source files and verifying names and disc references.
---

# Build a content catalog

Use this skill when a user wants an agent to add their local content to this project's SQLite catalog database (default `catalog.db`). The goal is games in the database served by this server, not a RetroArch playlist, ZIP archive, or `/api/games` response.

## Discover and plan

1. Read the current `README.md` and `SPECIFICATION.md` (section "Command line") for the scan rules, and `internal/store/known-platforms.json` for built-in platform names. Confirm the database path and content roots from the request or project configuration; ask if they cannot be determined.
2. Confirm the content roots are inside the database directory: source paths are stored relative to it and `scan` rejects other directories. If the files are elsewhere, choose a database location above them or ask the user before creating a symlink inside the database directory. Never alter or delete originals, including symlink targets.
3. Platforms are detected from the first folder under the database directory whose name equals a `libretro_name` (for example `Sony - PlayStation/`). If the folders are named differently, ask the user, and pass `-platform '<libretro_name>'` for a directory holding one platform's games instead of renaming anything. A custom platform not in the built-in list must be created in the admin UI first with values the user provides or confirms.
4. Inspect only the content roots authorized by the user. Do not download or distribute game data.

## Scan

1. From the project root, run `go run . scan -database <db> -dry-run <dir>` and review the JSON:
   - `added`: check each game's `platform`, `files` (cue files with their tracks, multi-disc grouping and order), and `canonical_name`.
   - `skipped`: report each `path` and `reason` (`no platform`, `cue reference missing: …`, `already in database`, validation errors). Do not invent CUE files, entrypoints, disc order, or platform assignments to work around them; ask when they cannot be established reliably.
2. For entries with `name_source: "filename"`, use the `find-canonical-name` skill: `go run . find-name -database <db> -platform '<libretro_name>' -file '<path>'` or `-q '<title>'`. Treat results as suggestions; ask when identity is ambiguous and never silently substitute an unrelated name. A title verified from the user's files or confirmed by the user may be used when the lookup fails or returns nothing.
3. Run the same command without `-dry-run` to write the games. Use `-offline` only if BoxArt is unreachable or the user asks.
4. Fix individual games (canonical name, display name, files, playlist) through the admin UI or `PUT /api/admin/games/{id}` with the game's `ETag` in `If-Match`. If sources are missing, show the exact paths and ask before saving with `?allow_missing=1`; such games stay hidden until the files exist.

## Verify

1. Start `go run . serve -database <db> -packages <fresh temporary directory> -listen 127.0.0.1:<free port>`. Wait for `GET /api/games` to succeed and compare the cards with the scan output; unavailable games are intentionally absent. Treat a startup error or unexpected catalog as a failed validation.
2. For a representative game, download its `download_url` (relative to `/api/games`) and inspect the ZIP against the card's `files`; verify the entrypoint and CUE/M3U references and report any uncertainty. Do not launch RetroArch unless the user asks and a client is available.
3. Stop the server and remove only the temporary cache. Clean up only files created for this task. Report the database path, platforms, added and skipped games, names resolved manually, and checks actually performed. Tell the user how to start the server with `content-catalog serve -database <db>`.
