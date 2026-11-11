# RetroArch Content Catalog & Asset Server

Turn your personal collection of game ROMs into a personal game downloader right inside RetroArch.

Browse, search, and download your games directly from the standard RetroArch interface (**Online Updater → Content Downloader**) on any device on your home network (PC, Steam Deck, mobile, or console). Includes a built-in web management dashboard (English & Russian).

> **Disclaimer:** This project is an independent personal initiative and is not affiliated with, endorsed by, or in any way officially connected with RetroArch or the Libretro team. It serves your own local files and does not provide or host copyrighted game data.

---

## 🚀 Quick Start (In 3 Simple Steps)

### Step 1: Start the Server

Choose **one** of the two easy options below:

#### Option A: Download Ready-to-Run Binary (Recommended for Desktop)
No dependencies, no runtime, and no installation needed — the web admin is built right into the single file.

1. Download the archive for your operating system from [**GitHub Releases**](https://github.com/Stalis/retroarch-marketplace/releases/latest):
   - **Windows:** `content-catalog-v...-windows-amd64.zip` (or `arm64`)
   - **macOS:** `content-catalog-v...-darwin-arm64.tar.gz` (Apple Silicon) or `darwin-amd64.tar.gz` (Intel)
   - **Linux:** `content-catalog-v...-linux-amd64.tar.gz` (or `arm64`)
2. Extract the archive into a folder on your computer. The catalog database (`catalog.db`) is created in the working directory; keep your games in folders below it.
3. Run the executable:
   - **Windows:** run in PowerShell: `.\content-catalog.exe serve`
   - **macOS / Linux:** open a terminal in that folder and run:
     ```sh
     ./content-catalog serve
     ```

#### Option B: Run with Docker (Recommended for Home Servers & NAS)
If you run a home server (Unraid, TrueNAS, Synology, Raspberry Pi, etc.):

```sh
docker run -d \
  --name retroarch-catalog \
  -p 5050:5050 \
  -v /path/to/your/games:/data \
  ghcr.io/stalis/retroarch-marketplace:latest
```

For admin access, use one of these two supported options:

- **Linux host networking:** replace `-p 5050:5050` with `--network host`, then open `http://127.0.0.1:5050/admin/` on the Docker host.
- **Bridge networking or Docker Desktop:** install `socat` on the host and run this local tunnel while using the dashboard:
  ```sh
  socat TCP-LISTEN:5051,bind=127.0.0.1,reuseaddr,fork EXEC:'docker exec -i retroarch-catalog nc 127.0.0.1 5050'
  ```
  Open `http://127.0.0.1:5051/admin/` on the host. The `nc` command runs inside the Alpine container.

The normal published port returns 403 for admin requests because bridge connections do not reach the server from container loopback. The tunnel is deliberately bound to host loopback; do not expose it to the LAN or run a second server against the same data.

The database is `/data/catalog.db`. To add games from the mounted folder, run `docker exec retroarch-catalog content-catalog scan /data`.

---

### Step 2: Add Your Games in the Web Dashboard

1. Open **[http://127.0.0.1:5050/admin/](http://127.0.0.1:5050/admin/)** for a standalone server or Linux host networking, or **[http://127.0.0.1:5051/admin/](http://127.0.0.1:5051/admin/)** when using the Docker bridge tunnel above.
2. **Add a Platform**: choose a console from the list of 80+ standard Libretro systems (e.g. *Sony - PlayStation*, *Super Nintendo Entertainment System*, *Sega - Mega Drive - Genesis*).
3. **Add Games**:
   - Enter game title.
   - For single-file games (SNES, Genesis, GBA): specify the ROM filename.
   - For disc games (PlayStation, Saturn): specify the `.cue` entrypoint and associated `.bin` tracks. Multi-disc games can have `.m3u` playlists generated automatically.
   - Source paths are relative to the folder containing the database.
4. Click **Save**. Each game is saved separately; if someone else changed it meanwhile, reload it before saving again.

Games saved with missing files are kept and published automatically after the next source scan finds those files. The platform list shows the number of currently published games.

The dashboard warns before discarding an unfinished game draft when reloading or leaving the page.

> 💡 **Tip:** If you have many games, add them all at once with [`content-catalog scan`](#-adding-many-games-with-scan).

---

### Step 3: Connect RetroArch

1. Open RetroArch.
2. Go to **Settings → Network** (or edit `retroarch.cfg`) and set the Content Downloader URL:
   ```ini
   core_updater_buildbot_assets_url = "http://127.0.0.1:5050/assets/"
   ```
   *(If RetroArch is on another device like a Steam Deck, TV box, or phone, replace `127.0.0.1` with your computer's local IP address, e.g. `http://192.168.1.50:5050/assets/`)*.
3. In RetroArch main menu, open:  
   **Online Updater → Content Downloader**
4. Pick your platform and download your games! They extract into your RetroArch download directory ready to launch.

Games with colliding names receive distinct generated download names and extraction directories; existing non-colliding names remain unchanged.

---

## 🌐 Network Access (Steam Deck, Consoles, Phones)

By default, the server listens on `127.0.0.1` (only accessible from the same machine). To make games available to RetroArch running on other devices in your home network:

- **Standalone binary:** pass `-listen 0.0.0.0:5050`:
  ```sh
  ./content-catalog serve -listen 0.0.0.0:5050
  ```
- **Docker:** already configured to listen on all interfaces (`0.0.0.0:5050`).

> 🔒 **Security Notice:** The web admin interface (`/admin/`) is strictly restricted to loopback connections (`localhost`) so external devices on your network cannot tamper with your catalog.

---

## 📂 Adding Many Games with `scan`

Put your games in folders named after the Libretro platform, below the folder containing the database, then scan them:

```text
games/
├── catalog.db
└── Sony - PlayStation/
    ├── Tekken3.cue
    ├── Tekken3.bin
    ├── Final Fantasy VII (Disc 1).cue
    └── …
```

```sh
./content-catalog scan -database games/catalog.db -dry-run games   # preview
./content-catalog scan -database games/catalog.db games            # write
```

- The platform comes from the first folder named like a Libretro platform (`-platform '<libretro_name>'` overrides it).
- A `.cue` is packaged with the tracks it references; `(Disc N)`, `_disc1`, ` CD2` files become one multi-disc game with a generated `.m3u`.
- Names are matched against Libretro BoxArt (e.g. `Tekken3.cue` → `Tekken 3 (USA)`); without a match, or with `-offline`, the file name is used.
- Files already in the database are skipped, so rescanning only adds new games.
- The output is JSON listing `added` and `skipped` files with reasons.

Look up likely Libretro canonical names without starting the server:

```sh
./content-catalog find-name -platform 'Sony - PlayStation' -q "King's Field"
./content-catalog find-name -platform 'Sony - PlayStation' -file 'Tekken3.cue'
```

The command returns up to ten ranked candidates as a JSON array. Candidates are suggestions, not verified release identities; check region, edition, revision, and disc information against your files. BoxArt listings are cached in the database for 7 days.

AI assistants (Claude, Cursor, Copilot, etc.) can drive this workflow with the project's agent skills: [`build-content-catalog`](.agents/skills/build-content-catalog/SKILL.md) and [`find-canonical-name`](.agents/skills/find-canonical-name/SKILL.md). Example prompt:
> *"Add my games in `/path/to/my/games` to the catalog using the `build-content-catalog` skill."*

---

## ⚙️ Configuration

The server works out of the box with zero configuration. For advanced setups, you can customize it via flags (accepted by every subcommand), environment variables, or a `config.toml` file (see `config.example.toml`):

| Setting | Flag | Environment variable | Default | Description |
|---|---|---|---|---|
| Listen address | `-listen` | `RETRO_LISTEN` | `127.0.0.1:5050` | Host and port to bind to |
| Database path | `-database` | `RETRO_DATABASE` | `catalog.db` | SQLite catalog; game source paths are relative to its folder |
| Cache folder | `-packages` | `RETRO_PACKAGES` | `packages` | Directory for generated game ZIPs |
| Scan interval | `-scan-interval` | `RETRO_SCAN_INTERVAL` | `10m` | How often to re-check source files for availability |
| Cleanup interval| `-cleanup-interval` | `RETRO_CLEANUP_INTERVAL` | `1h` | Cache cleaner frequency |
| Cache max age | `-cache-max-age` | `RETRO_CACHE_MAX_AGE` | `24h` | How long to keep generated ZIPs |

The database is created on first startup if absent; its parent folder must exist.

The server writes timestamped startup, HTTP request, and maintenance messages to standard error (view container output with `docker logs retroarch-catalog`). Request logs include the method, escaped path, response status, and duration; query parameters and request bodies are not logged. Paths may contain game names.

Check installed version at any time:
```sh
./content-catalog version
```

---

## 🛠️ For Developers & Building from Source

If you want to contribute or build the server yourself:

**Prerequisites:** Go 1.27+, Node.js 22+, npm.

```sh
git clone https://github.com/Stalis/retroarch-marketplace.git
cd retroarch-marketplace

# Run tests
make test           # Go unit & E2E tests
make admin-test     # Frontend unit tests
make e2e            # E2E tests without cache

# Build standalone release binary with embedded web admin
make build-release  # Outputs to bin/content-catalog

# Local development
make run            # Backend on http://127.0.0.1:5050
make admin-dev      # Vite frontend dev server with live reload
make admin-dev-mock # Standalone Vite frontend with mock backend
```

- Product specification: [SPECIFICATION.md](SPECIFICATION.md).
- Public API contract: [openapi.yaml](openapi.yaml).
- Architecture & guidelines for AI agents: [AGENTS.md](AGENTS.md).
