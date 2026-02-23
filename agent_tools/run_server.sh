#!/usr/bin/env bash
# run_server.sh - Start the werewolf server for testing
#
# Usage: ./agent_tools/run_server.sh [OPTIONS]
#
# Options:
#   --timeout SECONDS   Kill server after SECONDS (default: 300)
#   --port PORT         Port to check/use (default: 8080)
#   --log-requests      Log all HTTP requests and responses
#   --log-html          Capture HTML responses
#   --log-db            Dump database state on changes
#   --log-ws            Log WebSocket messages
#   --all-logs          Enable all logging options
#   --debug             Enable debug logging
#   --output-dir DIR    Directory for log files (default: ./server_logs)
#   --clean             Remove old logs before running
#   All other arguments are passed to 'go run main.go'

set -e

TIMEOUT=10
PORT=8080
LOG_REQUESTS=""
LOG_HTML=""
LOG_DB=""
LOG_WS=""
DEBUG=""
OUTPUT_DIR="./server_logs"
CLEAN=""
ARGS=()

show_help() {
    cat << 'EOF'
run_server.sh - Start the werewolf server for testing

Usage: ./agent_tools/run_server.sh [OPTIONS] [GO_RUN_ARGS...]

Options:
  --timeout SECONDS   Kill server after SECONDS (default: 10)
  --port PORT         Port to check/use (default: 8080)
  --log-requests      Log all HTTP requests and responses
  --log-html          Capture HTML responses
  --log-db            Dump database state on changes
  --log-ws            Log WebSocket messages
  --all-logs          Enable all logging options
  --debug             Enable debug logging
  --output-dir DIR    Directory for log files (default: ./server_logs)
  --clean             Remove old logs before running
  -h, --help          Show this help message

All other arguments are passed to 'go run main.go'

Examples:
  ./agent_tools/run_server.sh
  ./agent_tools/run_server.sh --timeout 60
  ./agent_tools/run_server.sh --all-logs
  ./agent_tools/run_server.sh --log-requests --log-ws
  ./agent_tools/run_server.sh --timeout 120 -db test.db
EOF
}

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --timeout)
            TIMEOUT="$2"
            shift 2
            ;;
        --port)
            PORT="$2"
            shift 2
            ;;
        --log-requests)
            LOG_REQUESTS="1"
            shift
            ;;
        --log-html)
            LOG_HTML="1"
            shift
            ;;
        --log-db)
            LOG_DB="1"
            shift
            ;;
        --log-ws)
            LOG_WS="1"
            shift
            ;;
        --all-logs)
            LOG_REQUESTS="1"
            LOG_HTML="1"
            LOG_DB="1"
            LOG_WS="1"
            DEBUG="1"
            shift
            ;;
        --debug)
            DEBUG="1"
            shift
            ;;
        --output-dir)
            OUTPUT_DIR="$2"
            shift 2
            ;;
        --clean)
            CLEAN="1"
            shift
            ;;
        -h|--help)
            show_help
            exit 0
            ;;
        *)
            ARGS+=("$1")
            shift
            ;;
    esac
done

# Kill any existing process on the port
EXISTING_PID=$(ss -tlnp 2>/dev/null | grep ":$PORT " | grep -oP 'pid=\K\d+' || true)
if [ -n "$EXISTING_PID" ]; then
    echo "Killing existing process on port $PORT (PID: $EXISTING_PID)"
    kill "$EXISTING_PID" 2>/dev/null || true
    sleep 1
fi

# Clear log file
> werewolf.log

# Set up logging environment variables
HAS_LOGGING=""
if [[ -n "$LOG_REQUESTS" || -n "$LOG_HTML" || -n "$LOG_DB" || -n "$LOG_WS" || -n "$DEBUG" ]]; then
    HAS_LOGGING="1"

    # Clean old logs if requested
    if [[ -n "$CLEAN" ]]; then
        echo "Cleaning old logs in $OUTPUT_DIR..."
        rm -rf "${OUTPUT_DIR:?}"/*
    fi

    # Create timestamped run directory
    mkdir -p "$OUTPUT_DIR"
    TIMESTAMP=$(date +%Y%m%d_%H%M%S)
    RUN_DIR="$OUTPUT_DIR/run_$TIMESTAMP"
    mkdir -p "$RUN_DIR"

    # Update latest symlink
    rm -f "$OUTPUT_DIR/latest"
    ln -s "run_$TIMESTAMP" "$OUTPUT_DIR/latest"

    export LOG_OUTPUT_DIR="$RUN_DIR"

    if [[ -n "$LOG_REQUESTS" ]]; then
        export LOG_REQUESTS="1"
        echo "Request logging enabled -> $RUN_DIR/requests.log"
    fi
    if [[ -n "$LOG_HTML" ]]; then
        export LOG_HTML="1"
        echo "HTML logging enabled -> $RUN_DIR/html_states.log"
    fi
    if [[ -n "$LOG_DB" ]]; then
        export LOG_DB="1"
        echo "Database logging enabled -> $RUN_DIR/database.log"
    fi
    if [[ -n "$LOG_WS" ]]; then
        export LOG_WS="1"
        echo "WebSocket logging enabled -> $RUN_DIR/websocket.log"
    fi
    if [[ -n "$DEBUG" ]]; then
        export LOG_DEBUG="1"
        echo "Debug logging enabled"
    fi

    echo "Log output directory: $RUN_DIR"
    echo ""
fi

# AI Storyteller configuration
export STORYTELLER_PROVIDER="claude"
export STORYTELLER_MODEL="claude-sonnet-4-6"

# Start server in background
echo "Starting server with timeout ${TIMEOUT}s..."
echo "Arguments: ${ARGS[*]}"

go run . "${ARGS[@]}" &
SERVER_PID=$!

# Set up cleanup
cleanup() {
    echo "Stopping server (PID: $SERVER_PID)"
    kill $SERVER_PID 2>/dev/null || true

    if [[ -n "$HAS_LOGGING" ]]; then
        echo ""
        echo "Log files available in: $RUN_DIR"
        echo "Latest: $OUTPUT_DIR/latest"
    fi
}
trap cleanup EXIT

# Wait for server to start
sleep 2

# Check if server is running
if ! kill -0 $SERVER_PID 2>/dev/null; then
    echo "Server failed to start"
    cat werewolf.log
    exit 1
fi

echo "Server running on port $PORT (PID: $SERVER_PID)"
echo "Will timeout in ${TIMEOUT}s"
echo "Logs: werewolf.log"

# Wait for timeout or manual interrupt
sleep $TIMEOUT &
wait $!
