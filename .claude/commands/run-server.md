# Run Server

Start the werewolf development server with automatic cleanup and optional extensive logging.

## Usage

Run the server with default settings (10 second timeout, port 8080):

```bash
./agent_tools/run_server.sh
```

### Options

| Option | Description |
|--------|-------------|
| `--timeout SECONDS` | Kill server after SECONDS (default: 10) |
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
./agent_tools/run_server.sh

# Longer timeout
./agent_tools/run_server.sh --timeout 60

# With all logging
./agent_tools/run_server.sh --all-logs

# Request and WebSocket logging only
./agent_tools/run_server.sh --log-requests --log-ws

# Custom timeout and database
./agent_tools/run_server.sh --timeout 120 -db test.db
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
5. Waits for the timeout, then stops the server

## Instructions

When the user asks to run or start the server:
1. Use default settings unless they specify otherwise
2. If they want to debug or see requests, add `--all-logs`
3. For longer running sessions, increase `--timeout`
4. After running, inform them of the server URL (http://localhost:8080) and log file locations
