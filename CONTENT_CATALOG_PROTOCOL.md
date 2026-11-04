# RetroArch Content Catalog Protocol

Protocol notes recovered from the RetroArch `content-catalog` branch. This describes the client contract visible in the branch; it does **not** claim to recover server-side behavior that is not implemented here.

## 1. Endpoint and transport

- Default catalog URL: `http://127.0.0.1:5050/api/games`.
- Both the menu module and CLI use this default. The CLI accepts an optional positional URL to select another catalog.
- Fetch with HTTP `GET`; successful response is JSON. The menu install task uses the catalog URL as the base URL when resolving package paths.
- Menu catalog loading accepts HTTP status `200`; CLI accepts any `2xx` status. The CLI's optional search uses query parameter `q`, URL-encoded: `GET <catalog-url>?q=<encoded-query>` (or `&q=` if the URL already has a query).
- The branch contains no server code. Search matching, ordering, pagination, authentication, caching headers, and server error schema are unspecified.

## 2. Catalog response schema

The response body is a **top-level JSON array**, including for zero results. Each element is a game card object:

```json
[
  {
    "id": "kings-field-psx",
    "canonical_name": "King's Field",
    "display_name": "King's Field (USA)",
    "platform": {
      "id": "playstation",
      "name": "PlayStation",
      "libretro_name": "Sony - PlayStation"
    },
    "entrypoint": "Kings Field (USA).cue",
    "files": [
      "Kings Field (USA).cue",
      "Kings Field (USA).bin"
    ],
    "download_url": "packages/kings-field-psx.zip",
    "boxart_url": "art/kings-field-psx.png"
  }
]
```

| Field | Required by parser | Meaning/constraints observable in client |
|---|---:|---|
| `id` | yes | Nonempty string; unique among returned cards. Used as the install directory name. Must be a safe relative path component (no slash for installation). |
| `canonical_name` | yes | Nonempty string; canonical display/playlist label. |
| `display_name` | no | If supplied, nonempty string. Menu uses `canonical_name` as the game label; display name is printed by CLI only. |
| `platform` | yes | Object containing all three required nonempty strings below. |
| `platform.id` | yes | Parsed and retained, otherwise not used by the observed install/menu flow. |
| `platform.name` | yes | Parsed and retained, otherwise not used by the observed install/menu flow. |
| `platform.libretro_name` | yes | Nonempty string; identifies the platform/core-assets subdirectory and playlist stem. Must be a safe single path component for install. |
| `entrypoint` | yes | Nonempty relative path to the content to launch; must occur exactly in `files`. |
| `files` | yes | Nonempty array of unique, nonempty relative file paths. It is the authoritative package manifest. |
| `download_url` | yes | Nonempty string, resolved against the catalog URL with RetroArch URL-join behavior; used to fetch the ZIP package. |
| `boxart_url` | no | Optional nonempty string if present. Current boxart CLI path does not consume it; it computes artwork URL from Libretro thumbnails instead. |

Unknown JSON object members are ignored, including nested values. Duplicate recognized fields in an object are rejected; duplicate IDs across cards and duplicate `files` entries are rejected. Parsed string fields must be nonempty and contain no embedded NUL byte. `display_name` and `boxart_url` are optional, but if present they still must be nonempty strings. The parser rejects malformed/trailing JSON data and requires `entrypoint` to match a listed file exactly.

### Path safety

Package paths are relative slash-separated paths. The client rejects absolute paths, empty components, `.` and `..` components, backslashes, colons, and control bytes. The server should emit ordinary portable relative file paths and must not rely on normalization by the client. `id` and `platform.libretro_name` additionally cannot contain `/` when used as directory components.

## 3. Package download and install contract

Given a card and catalog base URL, RetroArch resolves `download_url` against the base URL and downloads that resource. The expected package is a ZIP whose archive members match the declared `files` manifest:

- Every archive member must be listed in `files`.
- Each declared file must appear exactly once; no undeclared members or duplicate members.
- Every declared file must be present after extraction as a path that exists and is not a directory.
- `entrypoint` must be one of those declared files.

The client validates archive membership before extraction, extracts into a staging directory, validates extracted files, and then publishes by renaming the staging directory. Existing destination, staging, or temporary archive paths cause installation refusal rather than overwrite. On success it creates/adds a playlist entry using `canonical_name`; its content path is `<content-directory>/<platform.libretro_name>/<id>/<entrypoint>`. Menu installs write `<playlist-directory>/<platform.libretro_name>.lpl`; the CLI debug install prefixes the playlist with `DEBUG - Content Catalog - `. Do not treat a successful HTTP response alone as an installed game.

A package endpoint should return the ZIP bytes at the resolved URL with HTTP 200. The code does not implement a separate manifest endpoint, checksum field, version field, or package metadata document; do not invent one as part of protocol compatibility.

## 4. Search behavior

The CLI option `--install-debug <title>` appends `q=<URL-encoded title>` to the catalog URL and proceeds only if the returned catalog contains exactly one card. The branch demonstrates the parameter name and exact-match cardinality requirement, but does not establish whether the server performs substring, exact, case-insensitive, or other matching. The menu currently loads the unfiltered catalog and does not issue search queries.

## 5. Artwork behavior

`boxart_url` is accepted by the parser but is not used by the current branch's boxart downloader. The CLI derives an image path from `canonical_name` using RetroArch thumbnail filename conversion and downloads from the standard Libretro thumbnails server under:

`<thumbnail-server>/<platform.libretro_name>/Named_Boxarts/<converted-canonical-name>.png`

The result is stored under RetroArch's configured thumbnails directory. This is separate from the catalog package protocol; an implementation may provide `boxart_url`, but current client compatibility does not depend on it.

## 6. Implementer checklist

1. Serve a JSON array from `GET /api/games`; `[]` is the empty catalog.
2. Return all required card and platform fields with the types and constraints above.
3. Ensure each card's `entrypoint` is included in its complete, unique `files` list.
4. Make each `download_url` resolve from the catalog URL to a ZIP matching exactly that file manifest.
5. Support optional `q` on the catalog endpoint if CLI title search is required; matching semantics are not recoverable from this branch and must be chosen/documented by the new server.
6. Treat catalog data and ZIP member names as untrusted. Client performs path and archive validation; server should still avoid emitting unsafe or inconsistent data.

## Source basis and known gaps

Recovered from `content_catalog.c` / `.h`, `tasks/task_content_catalog.c`, `menu/menu_content_catalog.c`, and `tools/content-catalog/`. The hard-coded endpoint is explicitly marked temporary in the menu source. No backend implementation, API schema document, real sample payload, or server tests are present in the inspected branch. The JSON example above is illustrative, not a recovered production record.