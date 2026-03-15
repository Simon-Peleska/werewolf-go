#!/usr/bin/env bash
# run_tests.sh — inject project-specific env vars, then run go-test-tui.
#
# go-test-tui is provided by the Nix devShell (flake input).
#
# Project-specific flags (consumed here, not passed to the TUI):
#   --debug          Set TEST_DEBUG=1
#   --log-requests   Set TEST_LOG_REQUESTS=1
#   --log-html       Set TEST_LOG_HTML=1
#   --log-db         Set TEST_LOG_DB=1
#   --log-ws         Set TEST_LOG_WS=1
#   --all-logs       Set all of the above
#
# Everything else is forwarded to the TUI as-is.
# Use -- to pass extra flags directly to go test:
#   ./run_tests.sh -- -run TestFoo -count 2

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
