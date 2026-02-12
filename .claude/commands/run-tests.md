# Run Tests

Run tests with extensive logging options for debugging.

## Usage

Run all tests with default settings:

```bash
./agent_tools/run_tests.sh
```

### Options

| Option | Description |
|--------|-------------|
| `-v, --verbose` | Enable verbose test output (go test -v) |
| `--debug` | Enable debug mode with DB dumps on error |
| `--log-requests` | Log all HTTP requests and responses |
| `--log-html` | Capture HTML state from browser tests |
| `--log-db` | Dump database state before/after tests |
| `--log-ws` | Log WebSocket messages |
| `--all-logs` | Enable all logging options |
| `--output-dir DIR` | Directory for log files (default: ./test_logs) |
| `--test NAME` | Run specific test (go test -run NAME) |
| `--count N` | Run tests N times |
| `--keep-logs` | Keep logs even if tests pass |
| `--clean` | Remove old logs before running |

### Examples

```bash
# Run all tests
./agent_tools/run_tests.sh

# Verbose output
./agent_tools/run_tests.sh -v

# All logging enabled
./agent_tools/run_tests.sh --all-logs

# Run specific test with debugging
./agent_tools/run_tests.sh --test TestSignup --debug --log-db

# Full debugging for a failing test
./agent_tools/run_tests.sh --test TestWebSocketSync --all-logs --keep-logs --clean
```

## Log Files

Logs are stored in `./test_logs/run_TIMESTAMP/`:

- `test_output.log` - Go test output
- `requests.log` - HTTP request/response log
- `html_states.log` - Captured HTML states
- `database.log` - Database dumps
- `websocket.log` - WebSocket messages
- `summary.log` - Test run summary

The symlink `./test_logs/latest` always points to the most recent run.

## Instructions

When the user asks to run tests:

1. If they want to run all tests: `./agent_tools/run_tests.sh`
2. If they want verbose output: add `-v`
3. If they're debugging a failure: add `--all-logs --keep-logs`
4. If they want a specific test: add `--test TestName`
5. If they want fresh logs: add `--clean`

After running, report:
- Whether tests passed or failed
- Location of log files if `--keep-logs` was used or tests failed
- Key error messages from the output

If tests fail, offer to examine the log files (especially `database.log` and `html_states.log`) to help diagnose the issue.
