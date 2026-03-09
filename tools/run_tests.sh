#!/usr/bin/env bash
# run_tests.sh - Run tests with extensive logging options
#
# Usage: ./tools/run_tests.sh [OPTIONS]
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
#   ./tools/run_tests.sh                    # Run all tests
#   ./tools/run_tests.sh -v                 # Verbose output
#   ./tools/run_tests.sh --all-logs         # All logging enabled
#   ./tools/run_tests.sh --test TestSignup  # Run specific test
#   ./tools/run_tests.sh --debug --log-db   # Debug with DB dumps

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
TEST_PARALLEL=""
KEEP_LOGS=""
CLEAN=""

show_help() {
    cat << 'EOF'
run_tests.sh - Run tests with extensive logging options

Usage: ./tools/run_tests.sh [OPTIONS]

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
  --parallel N          Max parallel tests (go test -parallel N)
  --keep-logs           Keep logs even if tests pass
  --clean               Remove old logs before running
  -h, --help            Show this help message

Environment Variables Set:
  TEST_LOG_REQUESTS=1   Log HTTP requests/responses
  TEST_LOG_HTML=1       Capture HTML state
  TEST_LOG_DB=1         Dump database state
  TEST_LOG_WS=1         Log WebSocket messages
  TEST_DEBUG=1          Enable debug mode
  TEST_OUTPUT_DIR=path  Log output directory (each test gets its own subdirectory)

Examples:
  ./tools/run_tests.sh                    # Run all tests
  ./tools/run_tests.sh -v                 # Verbose output
  ./tools/run_tests.sh --all-logs         # All logging enabled
  ./tools/run_tests.sh --test TestSignup  # Run specific test
  ./tools/run_tests.sh --debug --log-db   # Debug with DB dumps
  ./tools/run_tests.sh --all-logs --clean # Fresh logs, all enabled
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
        --parallel)
            TEST_PARALLEL="$2"
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
SUMMARY_LOG="$RUN_DIR/summary.log"

echo "Test run: $TIMESTAMP" | tee "$SUMMARY_LOG"
echo "Output directory: $RUN_DIR" | tee -a "$SUMMARY_LOG"
echo "" | tee -a "$SUMMARY_LOG"

# Build test arguments (-v is always passed for full per-test output in JSON mode)
TEST_ARGS=("-v")
if [[ -n "$TEST_NAME" ]]; then
    TEST_ARGS+=("-run" "$TEST_NAME")
fi
if [[ -n "$TEST_COUNT" ]]; then
    TEST_ARGS+=("-count" "$TEST_COUNT")
fi
if [[ -n "$TEST_PARALLEL" ]]; then
    TEST_ARGS+=("-parallel" "$TEST_PARALLEL")
fi

# Export environment variables for test hooks
export TEST_OUTPUT_DIR="$RUN_DIR"
if [[ -n "$DEBUG" ]]; then
    export TEST_DEBUG="1"
    echo "DEBUG mode enabled" | tee -a "$SUMMARY_LOG"
fi
if [[ -n "$LOG_REQUESTS" ]]; then
    export TEST_LOG_REQUESTS="1"
    echo "Request logging enabled" | tee -a "$SUMMARY_LOG"
fi
if [[ -n "$LOG_HTML" ]]; then
    export TEST_LOG_HTML="1"
    echo "HTML state logging enabled" | tee -a "$SUMMARY_LOG"
fi
if [[ -n "$LOG_DB" ]]; then
    export TEST_LOG_DB="1"
    echo "Database logging enabled" | tee -a "$SUMMARY_LOG"
fi
if [[ -n "$LOG_WS" ]]; then
    export TEST_LOG_WS="1"
    echo "WebSocket logging enabled" | tee -a "$SUMMARY_LOG"
fi

echo "" | tee -a "$SUMMARY_LOG"
echo "Running: go test ./... ${TEST_ARGS[*]}" | tee -a "$SUMMARY_LOG"
echo "" | tee -a "$SUMMARY_LOG"

# ─── Live test display ────────────────────────────────────────────────────────
# Reads jq-preprocessed lines:
#   EVT<TAB><action><TAB><test>  – lifecycle events: run / pass / fail / skip
#   OUT<TAB><test><TAB><line>    – output lines, written to per-test log files
display_tests() {
    local outdir="$1"
    local -a spin=('⠋' '⠙' '⠹' '⠸' '⠼' '⠴' '⠦' '⠧' '⠇' '⠏')
    local spin_idx=0 lines_drawn=0
    declare -A state seen
    declare -a order

    _redraw() {
        local n=${#order[@]}
        [[ $n -eq 0 ]] && return

        # Terminal dimensions — read from /dev/tty so this works inside a pipeline
        local stty_out
        stty_out=$(stty size </dev/tty 2>/dev/null)
        local term_rows=${stty_out% *}  term_cols=${stty_out#* }
        : "${term_rows:=24}"  "${term_cols:=80}"

        # Longest test name (for fixed column width)
        local max_len=0 t
        for t in "${order[@]}"; do
            [[ ${#t} -gt $max_len ]] && max_len=${#t}
        done
        local col_width=$(( max_len + 6 ))   # "  X name  " visual width

        # Rows available before we'd scroll off screen (leave a few lines for
        # the surrounding script output)
        local avail=$(( term_rows - 5 ))
        [[ $avail -lt 3 ]] && avail=3

        # Columns needed to fit everything vertically, clamped to terminal width
        local num_cols=$(( (n + avail - 1) / avail ))
        [[ $num_cols -lt 1 ]] && num_cols=1
        local max_cols=$(( term_cols / col_width ))
        [[ $max_cols -lt 1 ]] && max_cols=1
        [[ $num_cols -gt $max_cols ]] && num_cols=$max_cols

        local rows=$(( (n + num_cols - 1) / num_cols ))

        # Move cursor up to where we started drawing last time
        local old_drawn=$lines_drawn
        [[ $old_drawn -gt 0 ]] && printf '\033[%dA' "$old_drawn"
        lines_drawn=0

        local row col idx s icon
        for (( row = 0; row < rows; row++ )); do
            printf '\033[2K'
            for (( col = 0; col < num_cols; col++ )); do
                idx=$(( col * rows + row ))
                [[ $idx -ge $n ]] && break
                t="${order[$idx]}"
                s="${state[$t]}"
                case "$s" in
                    run)  icon="${spin[$spin_idx]}"  ;;
                    pass) icon=$'\033[32m✓\033[0m'   ;;
                    fail) icon=$'\033[31m✗\033[0m'   ;;
                    skip) icon=$'\033[33m~\033[0m'   ;;
                    *)    icon='?'                   ;;
                esac
                printf '  %s %-*s  ' "$icon" "$max_len" "$t"
            done
            printf '\n'
            (( lines_drawn++ )) || true
        done

        # When column count increases, row count decreases. Erase stale lines
        # that were drawn during the taller single-column phase so the summary
        # printed afterwards doesn't land on top of leftover content.
        local stale=$(( old_drawn - rows ))
        for (( i = 0; i < stale; i++ )); do
            printf '\033[2K\n'
        done
        [[ $stale -gt 0 ]] && printf '\033[%dA' "$stale"

        (( spin_idx = (spin_idx + 1) % ${#spin[@]} )) || true
    }

    local type f1 f2
    while IFS=$'\t' read -r type f1 f2; do
        case "$type" in
            EVT)
                # f1=action  f2=test-name
                [[ -z "$f2" ]] && continue
                if [[ -z "${seen[$f2]+x}" ]]; then
                    order+=("$f2")
                    seen[$f2]=1
                fi
                state[$f2]="$f1"
                _redraw
                ;;
            OUT)
                # f1=test-name  f2=output-line (trailing newline already stripped by jq)
                if [[ -n "$f1" && -n "$outdir" ]]; then
                    local safe="${f1//\//_}"
                    mkdir -p "$outdir/$safe"
                    printf '%s\n' "$f2" >> "$outdir/$safe/test_output.log"
                fi
                ;;
        esac
    done

    _redraw  # freeze final state
    echo     # newline after the list
}

# jq filter: emit EVT lines for lifecycle events, OUT lines for test output.
# -rR reads raw lines so non-JSON build errors are silently skipped via `try`.
JQ_FILTER='
    try fromjson |
    if .Action == "output" and .Test then
        "OUT\t" + .Test + "\t" + (.Output | rtrimstr("\n"))
    elif (.Action | test("^(run|pass|fail|skip)$")) and .Test then
        "EVT\t" + .Action + "\t" + .Test
    else
        empty
    end
'

set +e
go test -json ./... "${TEST_ARGS[@]}" 2>&1 | \
    tee "$RUN_DIR/test_output.json" | \
    jq --unbuffered -rR "$JQ_FILTER" | \
    display_tests "$RUN_DIR"
TEST_EXIT_CODE=${PIPESTATUS[0]}
set -e

# ─── Summary ──────────────────────────────────────────────────────────────────
echo "================================================" | tee -a "$SUMMARY_LOG"

# Parse failures from the saved JSON (more reliable than grepping text output)
FAILED_TESTS=$(jq -r 'select(.Action == "fail" and .Test != null) | .Test' \
    "$RUN_DIR/test_output.json" 2>/dev/null || true)

if [[ $TEST_EXIT_CODE -eq 0 ]]; then
    echo "TESTS PASSED" | tee -a "$SUMMARY_LOG"

    if [[ -z "$KEEP_LOGS" ]]; then
        echo "Cleaning up logs (use --keep-logs to preserve)..." | tee -a "$SUMMARY_LOG"
        find "$RUN_DIR" -mindepth 1 -maxdepth 1 -type d -exec rm -rf {} +
        rm -f "$RUN_DIR/test_output.json"
    fi
else
    echo "TESTS FAILED (exit code: $TEST_EXIT_CODE)" | tee -a "$SUMMARY_LOG"
    echo "" | tee -a "$SUMMARY_LOG"

    if [[ -n "$FAILED_TESTS" ]]; then
        echo "Failed tests:" | tee -a "$SUMMARY_LOG"
        echo "------------------------------" | tee -a "$SUMMARY_LOG"
        while IFS= read -r test; do
            echo "  ✗ $test" | tee -a "$SUMMARY_LOG"
            # Show per-test log location if it exists
            safe="${test//\//_}"
            log="$RUN_DIR/$safe/test_output.log"
            if [[ -f "$log" && -s "$log" ]]; then
                echo "    $log" | tee -a "$SUMMARY_LOG"
            fi
        done <<< "$FAILED_TESTS"
        echo "" | tee -a "$SUMMARY_LOG"
    fi

    echo "All logs in: $RUN_DIR" | tee -a "$SUMMARY_LOG"
fi

# Create a symlink to latest run
rm -f "$OUTPUT_DIR/latest"
ln -s "run_$TIMESTAMP" "$OUTPUT_DIR/latest"

echo ""
echo "Full logs: $RUN_DIR"
echo "Latest: $OUTPUT_DIR/latest"

exit $TEST_EXIT_CODE
