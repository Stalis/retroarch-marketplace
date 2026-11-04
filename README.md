# RetroArch Content Catalog

This project was originally built to develop a custom marketplace protocol for RetroArch. It also supports RetroArch's standard **Content Downloader** asset-server protocol, so the same catalog can be browsed from a stock RetroArch installation.

- **Custom marketplace:** JSON catalog and lazy ZIP downloads; requires a compatible client. See [the protocol notes](CONTENT_CATALOG_PROTOCOL.md).
- **Standard RetroArch:** browse and download games through Online Updater → Content Downloader.
- **Web admin:** add platforms and games, edit the manifest, and choose English or Russian.

The server uses your local files. It does not provide, create, or download game data.

> **Disclaimer:** This project is an independent personal initiative and is not affiliated with, endorsed by, or in any way officially connected with RetroArch or the Libretro team. It does not aim to compete or conflict with RetroArch in any way.

## Quick start

**Requirements:** Go 1.22+, Git, Node.js 20.19+ or 22.12+, and npm. Node.js and npm are only needed for the web admin.

```sh
git clone https://github.com/Stalis/retroarch-marketplace.git
cd retroarch-marketplace

# Create an empty manifest on a fresh checkout; do not overwrite an existing one.
test ! -e catalog-source.json && test ! -L catalog-source.json && \
  printf '{"platforms":[],"contents":[]}\n' > catalog-source.json

make admin-install
make admin-build
make run
```

Open **http://127.0.0.1:5050/admin/**. Add a platform and a game, then click **Save**. Source file paths are relative to the manifest directory. The catalog is available at **http://127.0.0.1:5050/api/games**; an empty catalog returns `[]`.

> **Security:** The server listens on loopback by default and has no authentication. Exposing it to a network makes the catalog and game downloads available to others. The admin interface and API are restricted to loopback clients, but local processes can use them without authentication.

## Use with RetroArch

Set the asset-server URL in `retroarch.cfg`:

```ini
core_updater_buildbot_assets_url = "http://127.0.0.1:5050/assets/"
```

In RetroArch, open **Online Updater → Content Downloader**. If RetroArch runs on another device, replace `127.0.0.1` with the server's reachable address and explicitly set `-listen`, for example:

```sh
make run ARGS="-listen 0.0.0.0:5050"
```

This server implements Content Downloader, not every resource on the standard buildbot. Other updates that use the same asset URL may stop working until the original setting is restored. Extracted content location depends on RetroArch's settings and version; downloads are not automatically added to a playlist.

## Managing content

The admin supports predefined Libretro platform names and custom platforms. To add a game, enter its ID, title, platform, and one or more discs. For each disc, enter its package-relative entrypoint and the source and package paths for its files. Multiple discs require a playlist path; the server generates the M3U. CUE and other source files are copied unchanged; CUE files are not generated.

Changes remain in the editor until **Save**. The server validates the manifest and files before atomically replacing the manifest. If a file or parent directory is missing, you can return to editing or explicitly save the game with `defer_missing: true`. Such a game stays out of the public catalog until its files appear; other games remain available. Invalid paths and other validation errors cannot be bypassed. Concurrent manifest edits are rejected rather than overwritten.

You can also edit the manifest manually or ask an agent to use the [`build-content-manifest` skill](.agents/skills/build-content-manifest/SKILL.md). This example runs from the project root and creates `source.json` and `example/game.txt`; do not run it if those paths contain files you want to keep.

```sh
mkdir -p example
printf 'My test content\n' > example/game.txt
cat > source.json <<'JSON'
{
  "platforms": [{"id": "test", "name": "Test", "libretro_name": "Test"}],
  "contents": [{
    "id": "example",
    "canonical_name": "Example Game",
    "platform_id": "test",
    "discs": [{
      "entrypoint": "game.txt",
      "files": [{"source": "example/game.txt", "path": "game.txt"}]
    }]
  }]
}
JSON

go run . -catalog source.json
```

`source` is relative to the manifest directory; `path`, `entrypoint`, and `playlist` are relative to the ZIP root. Paths use `/` and must be safe relative paths. Define each platform once and refer to it by `platform_id`. The preset platform list is a snapshot from [libretro-database](https://github.com/libretro/libretro-database/tree/master/rdb), not a list of installed cores.

## Configuration

The optional `config.toml` is read from the current directory. Copy `config.example.toml` to get started, or choose a file with `-config` or `RETRO_CONFIG`. An explicitly specified missing file is an error. Precedence: **CLI flag → environment variable → TOML → default**.

| TOML | Environment variable | Flag | Default |
|---|---|---|---|
| `listen` | `RETRO_LISTEN` | `-listen` | `127.0.0.1:5050` |
| `catalog` | `RETRO_CATALOG` | `-catalog` | `catalog-source.json` |
| `packages` | `RETRO_PACKAGES` | `-packages` | `packages` |
| `scan_interval` | `RETRO_SCAN_INTERVAL` | `-scan-interval` | `10m` |
| `cleanup_interval` | `RETRO_CLEANUP_INTERVAL` | `-cleanup-interval` | `1h` |
| `cache_max_age` | `RETRO_CACHE_MAX_AGE` | `-cache-max-age` | `24h` |

Intervals are positive Go duration strings, such as `30s`, `10m`, and `24h`. Unknown TOML fields and invalid values prevent startup. Start the server from the project root so it can find the built `web/dist`; the admin is unavailable remotely even with an external `-listen`.

## ZIP cache

ZIPs are built on the first download and reused from `packages/.cache/`. The manifest and each source file's size and `mtime` are checked on requests and periodically; changes invalidate the cache. Checksums are not calculated, so edits preserving both size and `mtime` require a manifest change. Do not modify files during a download.

By default, a background task removes generated cache ZIPs older than 24 hours every hour. Age is based on ZIP modification time, not last use. Active downloads, source files, other files, and existing `packages/<id>.zip` are not deleted. Cache paths and intervals are configurable above.

## Development

```sh
make test         # Go tests, including E2E
make e2e          # E2E without the Go test cache
make admin-test   # frontend tests
make vet
make fmt
make build        # bin/catalog-server
```

For frontend development against a live server, run `make run` at `127.0.0.1:5050`, then run `make admin-dev` separately.

To develop, test, or redesign the web interface independently without running the Go backend, start the mock development server:

```sh
make admin-dev-mock   # Vite dev server with built-in mock backend (http://127.0.0.1:5173/admin/)
```

Alternatively, run `make admin-mock` to launch the standalone mock server on `http://127.0.0.1:5050`. `ARGS` passes flags to `make run`.

E2E uses temporary text files; game data and Python are not required. Actual installation in RetroArch is not covered by the tests.
