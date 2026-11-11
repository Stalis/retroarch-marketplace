# AGENTS.md — Developer and Agent Guide for RetroArch Marketplace

This guide outlines the system architecture, domain models, protocol compliance, development workflows, and engineering rules for coding assistants and contributors working on this repository.

## 1. Project Overview & Architecture

This repository contains a local content catalog and asset server for RetroArch, accompanied by a React-based web administration interface.

```
├── main.go                       # `content-catalog` subcommand dispatch (serve, scan, find-name, version)
├── config.go                     # Configuration loading (flags > env > TOML > defaults)
├── serve.go                      # `serve`: HTTP server & maintenance lifecycle
├── scan.go                       # `scan`: add games found under a directory
├── findname.go                   # `find-name`: Libretro canonical name lookup
├── config.example.toml           # Reference configuration
├── Makefile                      # Standard build, test, and dev tasks
├── internal/
│   ├── content/                  # Game model, validation, card building, ZIP building
│   ├── store/                    # SQLite storage (schema, games, FTS search, BoxArt cache)
│   │   └── known-platforms.json  # Built-in Libretro platforms (82)
│   ├── canonicalnames/           # BoxArt listing fetch/cache (Finder), title ranking, filename matching
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
│   │   ├── game.js               # Client-side game validation
│   │   ├── i18n.js               # UI translations (Russian default, English)
│   │   └── validation-i18n.js    # Localized validation error messages
│   ├── mock-backend.js       # In-memory mock admin backend & Vite middleware
│   ├── package.json
│   └── vite.config.js
├── .agents/skills/               # Agent skills
│   ├── build-content-catalog/    # Skill for adding user files with `scan`
│   └── find-canonical-name/      # Skill for canonical name lookups
├── SPECIFICATION.md              # Product specification
└── e2e_test.go                   # End-to-end integration test suite
```

### Key Components

1. **Catalog Database (`catalog.db`, `internal/store`)**:
   - SQLite (`modernc.org/sqlite`, pure Go) is the source of truth: `platforms`, `games`, `discs`, `files`, FTS5 indexes, and the BoxArt listing cache. Schema version is tracked with `PRAGMA user_version`.
   - Each game has `id`, `canonical_name`, optional `display_name`, `platform_id`, `discs`, optional `playlist`, and a `revision`. Each disc specifies an `entrypoint` and `files` (`source` path relative to the database directory, `path` relative to archive root).
   - Multi-disc games specify `playlist` (e.g. `game.m3u`). The server generates this M3U dynamically in order of disc entrypoints.
   - A game is available when all sources are regular files; availability is re-evaluated at startup, each scan tick, and on admin writes. Unavailable games stay in the database but are hidden from the catalog.
   - Every mutation bumps `meta.revision`; the server rebuilds its snapshot when it changes.

2. **Catalog & Package Server (`internal/catalog/customapi.go`, shared state in `catalog.go`)**:
   - `GET /api/games`: Serves cards conforming to `openapi.yaml`, in insertion order. `?q=` is a case-insensitive substring search on canonical and display names (FTS5 trigram for 3+ characters, `instr` for shorter queries).
   - `GET /api/packages/<encoded-id>.zip`: Lazily generates and caches game ZIPs in `packages/.cache/<fingerprint>-<id_hash>.zip`, keyed by a per-game fingerprint.
   - Active downloads hold reference-counted leases so the background cleaner never removes a file currently being streamed.

3. **RetroArch Content Downloader Compatibility (`internal/catalog/retroarch.go`)**:
   - Implements standard RetroArch asset server protocol (`core_updater_buildbot_assets_url = "http://<host>:5050/assets/"`).
   - `GET /assets/cores/.index-dirs`: Newline-delimited list of platform directory names.
   - `GET /assets/cores/<platform>/.index`: Newline-delimited list of available `<game>.zip` archives.
   - `GET /assets/cores/<platform>/<game>.zip`: Serves ZIP with contents nested in a game subdirectory so extractions don't overwrite sibling games.

4. **Admin API & Web UI (`internal/catalog/admin.go` + `web/`)**:
   - Loopback-only (`127.0.0.1` / `::1` client and Host). Cross-origin requests blocked via Origin header check.
   - Per-game CRUD under `/api/admin/` (`games`, `platforms`, `known-platforms`, `canonical-names`); see `SPECIFICATION.md`.
   - Game writes use `ETag`/`If-Match` with the game revision (`412` when stale). Missing sources return `422` with a `missing` list; retrying with `?allow_missing=1` stores the game as unavailable.
   - Serves pre-built Vite assets from `web/dist` at `/admin/`.

5. **Background Maintenance (`internal/catalog/maintenance.go`)**:
   - Uses Go standard library `time.Ticker` (default: scan every 10m, cleanup every 1h, cache max age 24h).
   - Re-evaluates source availability on each scan tick; purges expired BoxArt listings (7-day TTL) on each cleanup tick.
   - Cleans up generated cache archives older than max age while respecting active download leases. Never deletes user files or source ROMs.

---

## 2. Developer & Agent Workflows

### Essential Commands

```sh
# Run backend server (default: 127.0.0.1:5050)
make run                                   # go run . serve
make run ARGS="-listen 127.0.0.1:5051"

# CLI
go run . scan -database <db> -dry-run <dir>
go run . find-name -platform 'Sony - PlayStation' -q "King's Field"

# Build binaries
make build            # Builds to bin/content-catalog

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
   - Prefer Go standard library over third-party dependencies. Only `github.com/BurntSushi/toml` (config) and `modernc.org/sqlite` (storage) are used.
   - Do not add heavy frameworks, additional databases, or runtime daemons.
2. **Security & Path Safety**:
   - Never follow symlinks for cache root or packages directory.
   - Paths inside ZIP must be strictly relative, clean, slash-separated, with no `.` or `..` components, colons, backslashes, or control characters.
   - Admin routes must remain strictly loopback-only with Origin validation on mutating requests.
3. **Data Integrity & Non-Destructive Operations**:
   - Never mutate user source ROMs or CUE/BIN files.
   - Every store mutation runs in a single transaction.
   - Concurrent game writes must be guarded with revision ETags.
4. **Verification Requirement**:
   - Always verify changes with `make test` and `make admin-test`.
   - Verify UI changes visually or with headless browser scripts (`xd://eval/browser`) when modifying frontend components.
5. **Git Commits**:
   - Always create a git commit upon completing requested tasks and passing all verifications.

<!-- gitnexus:start -->
# GitNexus — Code Intelligence

This project is indexed by GitNexus as **retroarch-marketplace** (650 symbols, 1905 relationships, 55 execution flows).

> Index stale? Run `node .gitnexus/run.cjs analyze --index-only` from the project root — it auto-selects an available runner. No `.gitnexus/run.cjs` yet? Bootstrap with `npx`, `bunx`, or `pnpm dlx` — e.g. `bunx gitnexus@latest analyze` (npm 11 npx crash; #1939).

## Always Do

- **MUST run impact before editing.** Use `impact({target: "symbolName", direction: "upstream"})` or `node .gitnexus/run.cjs impact "symbolName" --direction upstream --repo .`; report callers, processes, and risk. Never substitute grep for graph analysis.
- **MUST analyze graph changes before committing.** Use `detect_changes({scope: "all"})` (MCP) or `node .gitnexus/run.cjs detect-changes --scope all --repo .` (CLI fallback). `partial: true` or `truncated: true` is not a clean check — a zero means unseen, not unaffected; re-run it. For regression review: `detect_changes({scope: "compare", base_ref: "main"})` or `node .gitnexus/run.cjs detect-changes --scope compare --base-ref "main" --repo .`.
- MUST warn on HIGH/CRITICAL `risk` pre-edit; never use `riskSharedAxes` to waive a HIGH/CRITICAL `risk` warning. Compare File/symbol: MCP File omits axes; Graph-RAG expands File.
- **MUST treat `risk: UNKNOWN` as unresolved, not as low.** An empty caller set is not evidence the symbol is unused — it can also mean the callers are not resolvable by the index (plain-object property access, dynamic dispatch, cross-language calls). `impact` pairs `UNKNOWN` with a `riskNote` saying so. Confirm with a text search before treating the symbol as safe to change or delete; do not proceed on the strength of a zero.
- **MUST use `query({search_query: "concept"})` for concepts/flows, `context({name: "symbolName"})` for a named symbol, or `impact` for blast radius, on read-only callers, dependencies, imports, or execution flow.** Graph first; text search only for empty/`UNKNOWN`/literals.
- For security review, `explain({target: "fileOrSymbol"})` lists taint findings (source→sink flows; needs `analyze --pdg`).

## Never Do

- NEVER edit a function, class, or method before MCP/CLI impact analysis.
- NEVER ignore HIGH or CRITICAL risk warnings from impact analysis, and never read `UNKNOWN` as an all-clear — it means the walk could not answer, which is the one verdict that requires confirming by other means.
- NEVER rename symbols with find-and-replace — use `rename` which understands the call graph.
- NEVER commit before MCP/CLI graph change analysis.

## Resources

| Resource | Use for |
| --- | --- |
| `gitnexus://repo/retroarch-marketplace/context` | Codebase overview, check index freshness |
| `gitnexus://repo/retroarch-marketplace/clusters` | All functional areas |
| `gitnexus://repo/retroarch-marketplace/processes` | All execution flows |
| `gitnexus://repo/retroarch-marketplace/process/{name}` | Step-by-step execution trace |

## CLI

| Task | Read this skill file |
| --- | --- |
| Understand architecture / "How does X work?" | `.claude/skills/gitnexus-exploring/SKILL.md` |
| Blast radius / "What breaks if I change X?" | `.claude/skills/gitnexus-impact-analysis/SKILL.md` |
| Trace bugs / "Why is X failing?" | `.claude/skills/gitnexus-debugging/SKILL.md` |
| Rename / extract / split / refactor | `.claude/skills/gitnexus-refactoring/SKILL.md` |
| Tools, resources, schema reference | `.claude/skills/gitnexus-guide/SKILL.md` |
| Index, status, clean, wiki CLI commands | `.claude/skills/gitnexus-cli/SKILL.md` |

<!-- gitnexus:end -->
