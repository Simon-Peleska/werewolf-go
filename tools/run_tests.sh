#!/usr/bin/env bash
# run_tests.sh — inject project-specific env vars, then run go-test-tui.
#
# go-test-tui is provided by the Nix devShell (flake input).
#
# Subcommands (passed through to go-test-tui):
#   (none)           Open the interactive TUI
#   run              Stream test output to terminal (no TUI — good for CI / scripting)
#   list             List tests from the last run (filter by -status failed|pass|skip)
#   help             Show go-test-tui help
#
# Project-specific flags (consumed here, not passed to go-test-tui):
#   --debug          Set TEST_DEBUG=1
#   --log-requests   Set TEST_LOG_REQUESTS=1
#   --log-html       Set TEST_LOG_HTML=1
#   --log-db         Set TEST_LOG_DB=1
#   --log-ws         Set TEST_LOG_WS=1
#   --all-logs       Set all of the above
#
# go-test-tui flags forwarded as-is:
#   --keep-logs      Keep log files even when tests pass
#   --clean          Remove old log directories before running
#   --output-dir     Directory for log files (default: ./test_logs)
#
# Use -- to pass extra flags directly to go test:
#   ./run_tests.sh -- -run TestFoo -count 2
#   ./run_tests.sh run -- -run TestFoo -parallel 4

set -e

# Parse project-specific flags; collect the rest for the TUI.
TUI_ARGS=()
while [[ $# -gt 0 ]]; do
    case "$1" in
        --debug)        export TEST_DEBUG=1 ;;
        --log-requests) export TEST_LOG_REQUESTS=1 ;;
        --log-html)     export TEST_LOG_HTML=1 ;;
        --log-db)       export TEST_LOG_DB=1 ;;
        --log-ws)       export TEST_LOG_WS=1 ;;
        --all-logs)
            export TEST_DEBUG=1 TEST_LOG_REQUESTS=1 TEST_LOG_HTML=1 \
                   TEST_LOG_DB=1 TEST_LOG_WS=1
            ;;
        *) TUI_ARGS+=("$1") ;;
    esac
    shift
done

exec go-test-tui "${TUI_ARGS[@]}"
