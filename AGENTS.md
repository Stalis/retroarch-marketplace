# AGENTS.md — Developer and Agent Guide for RetroArch Marketplace

This guide outlines the system architecture, domain models, protocol compliance, development workflows, and engineering rules for coding assistants and contributors working on this repository.

## 1. Project Overview & Architecture

This repository contains a local content catalog and asset server for RetroArch, accompanied by a React-based web administration interface.

```
├── main.go                       # Application entrypoint & maintenance lifecycle
├── config.go                     # Configuration loading (flags > env > TOML > defaults)
├── config.example.toml           # Reference configuration
├── Makefile                      # Standard build, test, and dev tasks
├── internal/
│   ├── content/                  # Domain model, manifest parsing, fingerprinting, ZIP building
│   │   ├── content.go
│   │   └── content_test.go
│   └── catalog/                  # HTTP handlers & background maintenance
│       ├── catalog.go            # Shared snapshot, cache, and request dispatch
│       ├── customapi.go          # Application catalog API (/api/games, /api/platforms, /api/packages/...)
│       ├── retroarch.go          # RetroArch Content Downloader compatibility (/assets/...)
│       ├── admin.go              # Loopback-only admin API & static web/dist serving
│       ├── maintenance.go        # Periodic source checks & cache cleanup scheduler
│       ├── catalog_test.go
│       ├── asset_test.go
│       ├── admin_test.go
│       └── maintenance_test.go
├── web/                          # Admin frontend (React 19 + Vite)
│   ├── src/
│   │   ├── main.jsx              # App component & UI state
│   │   ├── manifest.js           # AST-based manifest mutations (jsonc-parser)
│   │   ├── platforms.js          # Predefined Libretro platform catalog (82 platforms)
│   │   ├── i18n.js               # UI translations (Russian default, English)
│   │   └── validation-i18n.js    # Localized validation error messages
│   ├── mock-backend.js       # In-memory mock admin backend & Vite middleware
│   ├── package.json
│   └── vite.config.js
├── .agents/skills/               # Agent skills
│   └── build-content-manifest/   # Skill for assembling manifests from user files
├── SPECIFICATION.md              # Product specification
└── e2e_test.go                   # End-to-end integration test suite
```

### Key Components

1. **Domain Manifest (`catalog-source.json`)**:
   - The authoritative source of truth. Contains `platforms` and `contents` arrays.
   - Each content has `id`, `canonical_name`, optional `display_name`, `platform_id`, and `discs`.
   - Each disc specifies an `entrypoint` and `files` (`source` path relative to manifest directory, `path` relative to archive root).
   - Multi-disc games specify `playlist` (e.g. `game.m3u`). The server generates this M3U dynamically in order of disc entrypoints.
   - Games with missing sources can have `defer_missing: true`: they are retained in the manifest but omitted from the active catalog until files exist on disk.

2. **Catalog & Package Server (`internal/catalog/customapi.go`, shared state in `catalog.go`)**:
   - `GET /api/games`: Serves cards conforming to `openapi.yaml`. Supports `?q=` Unicode case-insensitive substring search on canonical and display names.
   - `GET /api/packages/<encoded-id>.zip`: Lazily generates and caches game ZIPs in `packages/.cache/<fingerprint>-<id_hash>.zip`.
   - Manifest reloads dynamically on requests if file size or `mtime` changes. Invalid manifests result in 500 error rather than serving stale content.
   - Active downloads hold reference-counted leases so the background cleaner never removes a file currently being streamed.

3. **RetroArch Content Downloader Compatibility (`internal/catalog/retroarch.go`)**:
   - Implements standard RetroArch asset server protocol (`core_updater_buildbot_assets_url = "http://<host>:5050/assets/"`).
   - `GET /assets/cores/.index-dirs`: Newline-delimited list of platform directory names.
   - `GET /assets/cores/<platform>/.index`: Newline-delimited list of available `<game>.zip` archives.
   - `GET /assets/cores/<platform>/<game>.zip`: Serves ZIP with contents nested in a game subdirectory so extractions don't overwrite sibling games.

4. **Admin API & Web UI (`internal/catalog/admin.go` + `web/`)**:
   - Loopback-only (`127.0.0.1` / `::1` client and Host). Cross-origin requests blocked via Origin header check.
   - `GET /api/admin/manifest`: Returns raw JSON with `ETag`.
   - `PUT /api/admin/manifest`: Atomic manifest replacement with `If-Match` revision check. Returns HTTP 422 with missing file list if any sources do not exist; retrying with `?allow_missing=1` marks affected games with `defer_missing: true`.
   - Serves pre-built Vite assets from `web/dist` at `/admin/`.

5. **Background Maintenance (`internal/catalog/maintenance.go`)**:
   - Uses Go standard library `time.Ticker` (default: scan every 10m, cleanup every 1h, cache max age 24h).
   - Scans and stats all referenced files.
   - Cleans up generated cache archives older than max age while respecting active download leases. Never deletes user files or source ROMs.

---

## 2. Developer & Agent Workflows

### Essential Commands

```sh
# Run backend server (default: 127.0.0.1:5050)
make run
make run ARGS="-listen 127.0.0.1:5051"

# Build binaries
make build            # Builds to bin/catalog-server

# Tests
make test             # Run all Go unit and E2E tests
make e2e              # Run E2E integration test suite without cache
make vet              # Run go vet
make fmt              # Run go fmt

# Frontend
make admin-install    # Install web dependencies (npm ci --prefix web)
make admin-build      # Build web production bundle (Vite -> web/dist)
make admin-dev        # Run Vite dev server with proxy to 127.0.0.1:5050
make admin-test       # Run Node.js frontend tests (node --test)
make admin-dev-mock   # Vite dev server with integrated mock backend (standalone)
make admin-mock       # Standalone Node.js mock admin server
```

### Making Changes: Rules & Invariants

1. **Smallest Complete Change**:
   - Prefer Go standard library over third-party dependencies. Only `github.com/BurntSushi/toml` is used for config decoding.
   - Do not add heavy frameworks, databases, or runtime daemons.
2. **Security & Path Safety**:
   - Never follow symlinks for cache root or packages directory.
   - Paths inside ZIP must be strictly relative, clean, slash-separated, with no `.` or `..` components, colons, backslashes, or control characters.
   - Admin routes must remain strictly loopback-only with Origin validation on mutating requests.
3. **Data Integrity & Non-Destructive Operations**:
   - Never mutate user source ROMs or CUE/BIN files.
   - Manifest edits must be atomic (write to temp file in same directory, sync, rename).
   - Concurrent manifest writes must be guarded with ETags.
4. **Verification Requirement**:
   - Always verify changes with `make test` and `make admin-test`.
   - Verify UI changes visually or with headless browser scripts (`xd://eval/browser`) when modifying frontend components.
5. **Git Commits**:
   - Always create a git commit upon completing requested tasks and passing all verifications.
