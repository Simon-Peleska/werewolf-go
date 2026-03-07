#!/usr/bin/env bash
# start_chromium.sh - Open multiple Chromium instances for manual testing
#
# Usage: ./tools/start_chromium.sh [OPTIONS]
#
# Options:
#   -u, --url URL         Target URL (default: http://localhost:8080)
#   -n, --instances N     Number of Chromium windows to open (default: 5)
#   -b, --bin BIN         Chromium binary name (default: chromium)
#   -w, --workspace N     Hyprland workspace to open windows on (default: 5)

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

TMP_ROOT="$(mktemp -d)"

cleanup() {
	rm -rf "$TMP_ROOT"
}
trap cleanup EXIT

# Switch to the target workspace before launching so windows land there
if command -v hyprctl &>/dev/null; then
	hyprctl dispatch workspace "$WORKSPACE" >/dev/null 2>&1
else
	echo "Warning: hyprctl not found — windows will open on the current workspace (--workspace ignored)"
fi

for i in $(seq 1 "$INSTANCES"); do
	PROFILE_DIR="$TMP_ROOT/profile_$i"
	mkdir -p "$PROFILE_DIR"

	"$CHROMIUM_BIN" \
		--user-data-dir="$PROFILE_DIR" \
		--incognito \
		--no-first-run \
		--no-default-browser-check \
		--disable-sync \
		"$URL" \
		>/dev/null 2>&1 &

	sleep 0.3
done

wait
