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
        --output-dir) return ;;
    esac

    # After -- : delegate to go-test-tui's go test flag completions
    local i
    for (( i=1; i<COMP_CWORD; i++ )); do
        if [[ "${COMP_WORDS[i]}" == "--" ]]; then
            local go_flags="-run -count -parallel -timeout -v -race -bench -benchtime -benchmem -coverprofile -failfast -short"
            COMPREPLY=($(compgen -W "$go_flags" -- "$cur"))
            return
        fi
    done

    local opts="--debug --log-requests --log-html --log-db --log-ws --all-logs --output-dir --keep-logs --clean --"
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

complete -F _run_server_completions     run_server.sh ./tools/run_server.sh tools/run_server.sh
complete -F _run_tests_completions      run_tests.sh  ./tools/run_tests.sh  tools/run_tests.sh
complete -F _start_chromium_completions start_chromium.sh ./tools/start_chromium.sh tools/start_chromium.sh

# go-test-tui completions are sourced in the Nix devShell shellHook from
# the flake input source tree. Nothing to do here.
