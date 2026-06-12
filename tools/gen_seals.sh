#!/usr/bin/env bash
#
# gen_seals.sh — regenerate the served seal images and the blur-up placeholders.
#
# Seals: reads the high-resolution originals from originals/seals/*.orig.webp and writes
#   - static/seals/<Name>.webp   : right-sized (600px) seal served to the browser
#   - static/seal_lqip.json      : { "<Name>": "data:image/webp;base64,..." } tiny
#                                  placeholders shown (blurred) until the full image loads
#
# Backgrounds: only generates placeholders — the full background images are tuned by hand
# (low-res/compression is noticeable), so they are NEVER re-encoded here, only read:
#   - static/bg_lqip.json        : { "background_<x>": "data:image/webp;base64,..." }
#
# WebP has no progressive decoding, so we fake "load a compressed version first" with a
# tiny placeholder that the frontend scales up + blurs, then swaps for the full image.
#
# Requires libwebp (cwebp/dwebp). Run via nix if not on PATH:
#   nix shell nixpkgs#libwebp -c ./tools/gen_seals.sh
#
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Full seal: 1024px covers 500px hero seals at ~2x (HiDPI) and 280px cards at ~3.5x.
FULL_SIZE=1024
FULL_Q=80
# Placeholder: tiny + low quality; the frontend blurs it, so detail is wasted bytes.
LQIP_SIZE=32
LQIP_Q=40

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# lqip_data_uri <src.webp> — print a "data:image/webp;base64,..." placeholder for one image.
lqip_data_uri() {
    local src="$1" tiny="$tmp/lqip.webp" png="$tmp/lqip.png"
    dwebp -quiet "$src" -o "$png"
    cwebp -quiet -q "$LQIP_Q" -resize "$LQIP_SIZE" 0 "$png" -o "$tiny"
    # base64 alphabet (A-Za-z0-9+/=) needs no JSON escaping.
    echo "data:image/webp;base64,$(base64 -w0 "$tiny")"
}

# write_json <out.json> <entry...> — emit { "k": "v", ... } from "  \"k\": \"v\"" lines.
write_json() {
    local out="$1"; shift
    {
        echo "{"
        printf '%s,\n' "${@:1:$#-1}"
        printf '%s\n' "${!#}"
        echo "}"
    } > "$out"
}

# ── Seals: re-encode full image + placeholder ──────────────────────────────────
mkdir -p "$ROOT/static/seals"
seal_entries=()
for src in "$ROOT"/originals/seals/*.orig.webp; do
    name="$(basename "$src" .orig.webp)"
    png="$tmp/$name.png"
    dwebp -quiet "$src" -o "$png"
    cwebp -quiet -q "$FULL_Q" -resize "$FULL_SIZE" 0 "$png" -o "$ROOT/static/seals/$name.webp"
    seal_entries+=("  \"$name\": \"$(lqip_data_uri "$src")\"")
    echo "  seal $name: $(stat -c%s "$ROOT/static/seals/$name.webp") bytes"
done
write_json "$ROOT/static/seal_lqip.json" "${seal_entries[@]}"
echo "Wrote static/seal_lqip.json (${#seal_entries[@]} placeholders)"

# ── Backgrounds: placeholder only, full image left untouched ───────────────────
bg_entries=()
for src in "$ROOT"/static/backgrounds/*.webp; do
    name="$(basename "$src" .webp)"
    bg_entries+=("  \"$name\": \"$(lqip_data_uri "$src")\"")
    echo "  bg $name: placeholder only (full image untouched)"
done
write_json "$ROOT/static/bg_lqip.json" "${bg_entries[@]}"
echo "Wrote static/bg_lqip.json (${#bg_entries[@]} placeholders)"
