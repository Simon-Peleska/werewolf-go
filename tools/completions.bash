#!/usr/bin/env bash
# Bash completions for werewolf-go tools scripts.
# Source this file to enable tab completion:
#   source ./tools/completions.bash
# Or add to ~/.bashrc:
#   source /path/to/werewolf-go/tools/completions.bash

_run_server_completions() {
    local cur prev
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    case "$prev" in
        --timeout|--port|--output-dir)
            # No completions for value args — let the user type
            return
            ;;
    esac

    local opts="--timeout --watch --test-db --port --log-requests --log-html --log-db --log-ws --all-logs --debug --output-dir --clean --help"
    COMPREPLY=($(compgen -W "$opts" -- "$cur"))
}

_run_tests_completions() {
    local cur prev
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    case "$prev" in
        --test)
            # Complete with test function names from *_test.go files
            local tests
            tests=$(grep -rh '^func Test' ./*.go 2>/dev/null | sed 's/func \(Test[^(]*\).*/\1/')
            COMPREPLY=($(compgen -W "$tests" -- "$cur"))
            return
            ;;
        --output-dir|--count)
            return
            ;;
    esac

    local opts="-v --verbose --debug --log-requests --log-html --log-db --log-ws --all-logs --output-dir --test --count --keep-logs --clean --help"
    COMPREPLY=($(compgen -W "$opts" -- "$cur"))
}

_start_chromium_completions() {
    local cur prev
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    case "$prev" in
        -u|--url|-n|--instances|-b|--bin|-w|--workspace)
            return
            ;;
    esac

    local opts="-u --url -n --instances -b --bin -w --workspace"
    COMPREPLY=($(compgen -W "$opts" -- "$cur"))
}

complete -F _run_server_completions    ./tools/run_server.sh    tools/run_server.sh
complete -F _run_tests_completions     ./tools/run_tests.sh     tools/run_tests.sh
complete -F _start_chromium_completions ./tools/start_chromium.sh tools/start_chromium.sh
