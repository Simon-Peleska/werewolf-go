# Start Chromium

Open multiple Chromium browser instances for manual multi-player testing. Each instance gets its own isolated profile so they behave as separate users.

## Usage

```bash
./tools/start_chromium.sh [OPTIONS]
```

Launches Chromium windows pointed at the target URL, each with a separate incognito profile. On Hyprland, switches to the specified workspace first so all windows open there. All windows close when the script exits (Ctrl+C).

| Option | Default | Description |
|--------|---------|-------------|
| `-u, --url URL` | `http://localhost:8080` | Target URL |
| `-n, --instances N` | `5` | Number of windows to open |
| `-b, --bin BIN` | `chromium` | Chromium binary name |
| `-w, --workspace N` | `5` | Hyprland workspace to open windows on |

## Configuration

Edit the top of `tools/start_chromium.sh` to change defaults:

| Variable | Default | Description |
|----------|---------|-------------|
| `URL` | `http://localhost:8080` | Target URL |
| `INSTANCES` | `5` | Number of windows to open |
| `CHROMIUM_BIN` | `chromium` | Browser binary name |

## Instructions

When the user asks to open browser windows or start Chromium for testing:

1. Make sure the server is running first (`./tools/run_server.sh`)
2. Run `./tools/start_chromium.sh`
3. Inform the user that 5 browser windows will open at http://localhost:8080
4. Each window is a separate player — they can sign in with different names
