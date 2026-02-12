#!/usr/bin/env bash
# run_tests.sh - Run tests with extensive logging options
#
# Usage: ./agent_tools/run_tests.sh [OPTIONS]
#
# Options:
#   -v, --verbose         Enable verbose test output (go test -v)
#   --debug               Enable debug mode with DB dumps on error
#   --log-requests        Log all HTTP requests and responses
#   --log-html            Capture HTML state from browser tests
#   --log-db              Dump database state before/after tests
#   --log-ws              Log WebSocket messages
#   --all-logs            Enable all logging options
#   --output-dir DIR      Directory for log files (default: ./test_logs)
#   --test NAME           Run specific test (go test -run NAME)
#   --count N             Run tests N times (go test -count N)
#   --keep-logs           Keep logs even if tests pass
#   --clean               Remove old logs before running
#   -h, --help            Show this help message
#
# Examples:
#   ./agent_tools/run_tests.sh                    # Run all tests
#   ./agent_tools/run_tests.sh -v                 # Verbose output
#   ./agent_tools/run_tests.sh --all-logs         # All logging enabled
#   ./agent_tools/run_tests.sh --test TestSignup  # Run specific test
#   ./agent_tools/run_tests.sh --debug --log-db   # Debug with DB dumps

set -e

# Defaults
VERBOSE=""
DEBUG=""
LOG_REQUESTS=""
LOG_HTML=""
LOG_DB=""
LOG_WS=""
OUTPUT_DIR="./test_logs"
TEST_NAME=""
TEST_COUNT=""
KEEP_LOGS=""
CLEAN=""

show_help() {
    cat << 'EOF'
run_tests.sh - Run tests with extensive logging options

Usage: ./agent_tools/run_tests.sh [OPTIONS]

Options:
  -v, --verbose         Enable verbose test output (go test -v)
  --debug               Enable debug mode with DB dumps on error
  --log-requests        Log all HTTP requests and responses
  --log-html            Capture HTML state from browser tests
  --log-db              Dump database state before/after tests
  --log-ws              Log WebSocket messages
  --all-logs            Enable all logging options
  --output-dir DIR      Directory for log files (default: ./test_logs)
  --test NAME           Run specific test (go test -run NAME)
  --count N             Run tests N times (go test -count N)
  --keep-logs           Keep logs even if tests pass
  --clean               Remove old logs before running
  -h, --help            Show this help message

Environment Variables Set:
  TEST_LOG_REQUESTS=1   Log HTTP requests/responses
  TEST_LOG_HTML=1       Capture HTML state
  TEST_LOG_DB=1         Dump database state
  TEST_LOG_WS=1         Log WebSocket messages
  TEST_DEBUG=1          Enable debug mode
  TEST_OUTPUT_DIR=path  Log output directory

Examples:
  ./agent_tools/run_tests.sh                    # Run all tests
  ./agent_tools/run_tests.sh -v                 # Verbose output
  ./agent_tools/run_tests.sh --all-logs         # All logging enabled
  ./agent_tools/run_tests.sh --test TestSignup  # Run specific test
  ./agent_tools/run_tests.sh --debug --log-db   # Debug with DB dumps
  ./agent_tools/run_tests.sh --all-logs --clean # Fresh logs, all enabled
EOF
}

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -v|--verbose)
            VERBOSE="-v"
            shift
            ;;
        --debug)
            DEBUG="1"
            shift
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
            VERBOSE="-v"
            shift
            ;;
        --output-dir)
            OUTPUT_DIR="$2"
            shift 2
            ;;
        --test)
            TEST_NAME="$2"
            shift 2
            ;;
        --count)
            TEST_COUNT="$2"
            shift 2
            ;;
        --keep-logs)
            KEEP_LOGS="1"
            shift
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
            echo "Unknown option: $1"
            show_help
            exit 1
            ;;
    esac
done

# Create output directory
mkdir -p "$OUTPUT_DIR"

# Clean old logs if requested
if [[ -n "$CLEAN" ]]; then
    echo "Cleaning old logs in $OUTPUT_DIR..."
    rm -rf "${OUTPUT_DIR:?}"/*
fi

# Set timestamp for this run
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
RUN_DIR="$OUTPUT_DIR/run_$TIMESTAMP"
mkdir -p "$RUN_DIR"

# Create log files
TEST_LOG="$RUN_DIR/test_output.log"
REQUEST_LOG="$RUN_DIR/requests.log"
HTML_LOG="$RUN_DIR/html_states.log"
DB_LOG="$RUN_DIR/database.log"
WS_LOG="$RUN_DIR/websocket.log"
SUMMARY_LOG="$RUN_DIR/summary.log"

echo "Test run: $TIMESTAMP" | tee "$SUMMARY_LOG"
echo "Output directory: $RUN_DIR" | tee -a "$SUMMARY_LOG"
echo "" | tee -a "$SUMMARY_LOG"

# Build test arguments
TEST_ARGS=()
if [[ -n "$VERBOSE" ]]; then
    TEST_ARGS+=("-v")
fi
if [[ -n "$TEST_NAME" ]]; then
    TEST_ARGS+=("-run" "$TEST_NAME")
fi
if [[ -n "$TEST_COUNT" ]]; then
    TEST_ARGS+=("-count" "$TEST_COUNT")
fi

# Export environment variables for test hooks
export TEST_OUTPUT_DIR="$RUN_DIR"
if [[ -n "$DEBUG" ]]; then
    export TEST_DEBUG="1"
    echo "DEBUG mode enabled" | tee -a "$SUMMARY_LOG"
fi
if [[ -n "$LOG_REQUESTS" ]]; then
    export TEST_LOG_REQUESTS="1"
    export TEST_REQUEST_LOG="$REQUEST_LOG"
    echo "Request logging enabled -> $REQUEST_LOG" | tee -a "$SUMMARY_LOG"
fi
if [[ -n "$LOG_HTML" ]]; then
    export TEST_LOG_HTML="1"
    export TEST_HTML_LOG="$HTML_LOG"
    echo "HTML state logging enabled -> $HTML_LOG" | tee -a "$SUMMARY_LOG"
fi
if [[ -n "$LOG_DB" ]]; then
    export TEST_LOG_DB="1"
    export TEST_DB_LOG="$DB_LOG"
    echo "Database logging enabled -> $DB_LOG" | tee -a "$SUMMARY_LOG"
fi
if [[ -n "$LOG_WS" ]]; then
    export TEST_LOG_WS="1"
    export TEST_WS_LOG="$WS_LOG"
    echo "WebSocket logging enabled -> $WS_LOG" | tee -a "$SUMMARY_LOG"
fi

echo "" | tee -a "$SUMMARY_LOG"
echo "Running: go test ./... ${TEST_ARGS[*]}" | tee -a "$SUMMARY_LOG"
echo "================================================" | tee -a "$SUMMARY_LOG"
echo "" | tee -a "$SUMMARY_LOG"

# Run tests and capture output
set +e
go test ./... "${TEST_ARGS[@]}" 2>&1 | tee "$TEST_LOG"
TEST_EXIT_CODE=${PIPESTATUS[0]}
set -e

echo "" | tee -a "$SUMMARY_LOG"
echo "================================================" | tee -a "$SUMMARY_LOG"

# Summarize results
if [[ $TEST_EXIT_CODE -eq 0 ]]; then
    echo "TESTS PASSED" | tee -a "$SUMMARY_LOG"

    # Clean up logs on success unless --keep-logs is set
    if [[ -z "$KEEP_LOGS" ]]; then
        echo "Cleaning up logs (use --keep-logs to preserve)..." | tee -a "$SUMMARY_LOG"
        # Keep summary but remove detailed logs
        find "$RUN_DIR" -type f ! -name "summary.log" ! -name "test_output.log" -delete
    fi
else
    echo "TESTS FAILED (exit code: $TEST_EXIT_CODE)" | tee -a "$SUMMARY_LOG"
    echo "" | tee -a "$SUMMARY_LOG"
    echo "Log files preserved in: $RUN_DIR" | tee -a "$SUMMARY_LOG"

    # List available log files
    echo "" | tee -a "$SUMMARY_LOG"
    echo "Available logs:" | tee -a "$SUMMARY_LOG"
    for f in "$RUN_DIR"/*.log; do
        if [[ -f "$f" && -s "$f" ]]; then
            echo "  - $(basename "$f") ($(wc -l < "$f") lines)" | tee -a "$SUMMARY_LOG"
        fi
    done

    # Show last few lines of test output on failure
    echo "" | tee -a "$SUMMARY_LOG"
    echo "Last 20 lines of test output:" | tee -a "$SUMMARY_LOG"
    echo "------------------------------" | tee -a "$SUMMARY_LOG"
    tail -20 "$TEST_LOG" | tee -a "$SUMMARY_LOG"
fi

# Create a symlink to latest run
rm -f "$OUTPUT_DIR/latest"
ln -s "run_$TIMESTAMP" "$OUTPUT_DIR/latest"

echo ""
echo "Full logs: $RUN_DIR"
echo "Latest: $OUTPUT_DIR/latest"

exit $TEST_EXIT_CODE
