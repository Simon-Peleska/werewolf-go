# Run Server

Start the werewolf development server with automatic cleanup and optional extensive logging.

## Usage

Run the server (runs until Ctrl+C by default):

```bash
./tools/run_server.sh
```

### Options

| Option | Description |
|--------|-------------|
| `--timeout SECONDS` | Kill server after SECONDS (no default; runs until Ctrl+C) |
| `--watch` | Restart server when .go/.html/.js/.css files change (requires `inotifywait`) |
| `--port PORT` | Port to check/use (default: 8080) |
| `--log-requests` | Log all HTTP requests and responses |
| `--log-html` | Capture HTML responses |
| `--log-db` | Dump database state on changes |
| `--log-ws` | Log WebSocket messages |
| `--all-logs` | Enable all logging options |
| `--debug` | Enable debug logging |
| `--output-dir DIR` | Directory for log files (default: ./server_logs) |
| `--clean` | Remove old logs before running |

Any additional arguments are passed to `go run .`

### Examples

```bash
# Start with defaults
./tools/run_server.sh

# Stop after 60 seconds
./tools/run_server.sh --timeout 60

# With all logging
./tools/run_server.sh --all-logs

# Request and WebSocket logging only
./tools/run_server.sh --log-requests --log-ws

# Auto-stop after 120s with custom database
./tools/run_server.sh --timeout 120 -db test.db

# Watch mode: restart on source file changes
./tools/run_server.sh --watch
```

## Log Files

When logging is enabled, logs are stored in `./server_logs/run_TIMESTAMP/`:
- `requests.log` - HTTP request/response log
- `html_states.log` - Captured HTML responses
- `database.log` - Database dumps
- `websocket.log` - WebSocket messages

The symlink `./server_logs/latest` always points to the most recent run.

## What it does

1. Kills any existing process on the specified port
2. Clears the log file (werewolf.log)
3. Sets up logging environment if logging options are enabled
4. Starts the server with `go run .`
5. Waits until Ctrl+C or `--timeout` expires, then stops the server (kills both `go run` and the compiled child binary)

## Instructions

When the user asks to run or start the server:
1. Use default settings unless they specify otherwise
2. If they want to debug or see requests, add `--all-logs`
3. Use `--timeout SECONDS` only when a time-limited run is needed
4. After running, inform them of the server URL (http://localhost:8080) and log file locations
