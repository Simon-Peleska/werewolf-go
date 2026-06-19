#!/usr/bin/env bash
# start_chromium.sh - Open one Chromium window with N isolated tabs for manual testing
#
# Each tab gets its own CDP browser context (separate cookies/storage), so they
# behave as separate players without opening N separate windows.
#
# Usage: ./tools/start_chromium.sh [OPTIONS]
#
# Options:
#   -u, --url URL         Target URL (default: http://localhost:8080)
#   -n, --instances N     Number of isolated tabs to open (default: 5)
#   -b, --bin BIN         Chromium binary name (default: chromium)
#   -w, --workspace N     Hyprland workspace to open the window on (default: 5)

URL="http://localhost:8080"
INSTANCES=5
CHROMIUM_BIN="chromium"
WORKSPACE=5

while [[ $# -gt 0 ]]; do
	case $1 in
		-u|--url)
			URL="$2"
			shift 2
			;;
		-n|--instances)
			INSTANCES="$2"
			shift 2
			;;
		-b|--bin)
			CHROMIUM_BIN="$2"
			shift 2
			;;
		-w|--workspace)
			WORKSPACE="$2"
			shift 2
			;;
		*)
			shift
			;;
	esac
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

exec go run "$SCRIPT_DIR/start_chromium" \
	-url "$URL" \
	-instances "$INSTANCES" \
	-bin "$CHROMIUM_BIN" \
	-workspace "$WORKSPACE"
