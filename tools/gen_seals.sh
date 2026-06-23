#!/usr/bin/env bash
#
# gen_seals.sh — regenerate the served seal images and the blur-up placeholders.
#
# Seals: reads the high-resolution originals from originals/seals/*.orig.webp and writes
#   - static/seals/<Name>.webp   : right-sized (600px) seal served to the browser
#   - static/seals/<Name>.avif   : same image re-encoded as AVIF (smaller; templates use it
#                                  as the <picture> source, with the WebP as <img> fallback)
#   - static/seal_lqip.json      : { "<Name>": "data:image/webp;base64,..." } tiny
#                                  placeholders shown (blurred) until the full image loads
#
# Backgrounds: the WebP itself is tuned by hand (low-res/compression is noticeable) and is
# NEVER re-encoded/resized — but we derive a same-pixels AVIF sibling from it for the CSS
# image-set() fallback, same as seals:
#   - static/backgrounds/<x>.avif : the hand-tuned WebP losslessly re-decoded and re-encoded
#                                    as AVIF (smaller; CSS picks it via image-set(), WebP is
#                                    the fallback)
#   - static/bg_lqip.json         : { "background_<x>": "data:image/webp;base64,..." }
#
# LQIP placeholders stay WebP-only: at 32px they're inlined as data: URIs (no separate
# request to save), and AVIF's container overhead makes it *larger* than WebP at this size
# (measured ~2x bigger for these placeholders) — AVIF would only bloat every page load here.
#
# WebP has no progressive decoding, so we fake "load a compressed version first" with a
# tiny placeholder that the frontend scales up + blurs, then swaps for the full image.
#
# Requires libwebp (cwebp/dwebp) and libavif (avifenc). Run via nix if not on PATH:
#   nix shell nixpkgs#libwebp nixpkgs#libavif -c ./tools/gen_seals.sh
#
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Full seal: 1024px covers 500px hero seals at ~2x (HiDPI) and 280px cards at ~3.5x.
FULL_SIZE=1024
FULL_Q=80
# AVIF quality is on the same 0-100 scale, but AVIF reaches comparable quality at lower
# numbers than WebP (~30% smaller files at this setting, checked by eye against FULL_Q).
AVIF_Q=58
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
    full_png="$tmp/$name.full.png"
    dwebp -quiet "$src" -o "$png"
    cwebp -quiet -q "$FULL_Q" -resize "$FULL_SIZE" 0 "$png" -o "$ROOT/static/seals/$name.webp"
    # Re-decode the just-written (already resized) WebP so avifenc encodes at the same
    # dimensions without a separate resize step.
    dwebp -quiet "$ROOT/static/seals/$name.webp" -o "$full_png"
    avifenc -q "$AVIF_Q" "$full_png" "$ROOT/static/seals/$name.avif" >/dev/null
    seal_entries+=("  \"$name\": \"$(lqip_data_uri "$src")\"")
    echo "  seal $name: $(stat -c%s "$ROOT/static/seals/$name.webp") bytes webp, $(stat -c%s "$ROOT/static/seals/$name.avif") bytes avif"
done
write_json "$ROOT/static/seal_lqip.json" "${seal_entries[@]}"
echo "Wrote static/seal_lqip.json (${#seal_entries[@]} placeholders)"

# ── Backgrounds: derive an AVIF sibling (WebP itself is hand-tuned, never re-encoded) ──
bg_entries=()
for src in "$ROOT"/static/backgrounds/*.webp; do
    name="$(basename "$src" .webp)"
    png="$tmp/$name.bg.png"
    dwebp -quiet "$src" -o "$png"
    avifenc -q "$AVIF_Q" "$png" "$ROOT/static/backgrounds/$name.avif" >/dev/null
    bg_entries+=("  \"$name\": \"$(lqip_data_uri "$src")\"")
    echo "  bg $name: $(stat -c%s "$src") bytes webp (untouched), $(stat -c%s "$ROOT/static/backgrounds/$name.avif") bytes avif"
done
write_json "$ROOT/static/bg_lqip.json" "${bg_entries[@]}"
echo "Wrote static/bg_lqip.json (${#bg_entries[@]} placeholders)"
