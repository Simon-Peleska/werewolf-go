#!/usr/bin/env bash

# -------- CONFIG --------
URL="http://localhost:8080" # target site
INSTANCES=5                 # number of Chromium instances
CHROMIUM_BIN="chromium"     # chromium, chromium-browser, google-chrome, etc.
# ------------------------

TMP_ROOT="$(mktemp -d)"

cleanup() {
	rm -rf "$TMP_ROOT"
}
trap cleanup EXIT

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
