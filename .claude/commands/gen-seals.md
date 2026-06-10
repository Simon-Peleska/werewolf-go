# Generate Seals

Regenerate the served seal images and their blur-up placeholders from the
high-resolution originals.

## Usage

```bash
nix shell nixpkgs#libwebp -c ./tools/gen_seals.sh
```

(If `cwebp`/`dwebp` are already on your PATH you can run `./tools/gen_seals.sh` directly.)

## What it does

Reads `originals/seals/*.orig.webp` and writes:

| Output | Description |
|--------|-------------|
| `static/seals/<Name>.webp` | Right-sized (600px, q80) seal served to the browser |
| `static/seal_lqip.json` | `{ "<Name>": "data:image/webp;base64,..." }` tiny (32px) blurred placeholders for seals |
| `static/bg_lqip.json` | Same, for the full-screen phase backgrounds |

WebP has no progressive decoding, so the placeholder is how we "load a
compressed version first": the frontend scales the 32px image up + blurs it,
then swaps in the full image on load. Placeholders are embedded into the binary
and used via `window.SEAL_LQIP` (cards), `{{sealLQIP}}` (hero/win seals), and
`--bg-<x>-lqip` CSS vars (backgrounds).

**Full background images are read-only here** — they are hand-tuned (low-res /
compression artifacts are noticeable), so this script never re-encodes
`static/backgrounds/`, only reads them to build placeholders.

Run this whenever you add or replace a seal in `originals/seals/` or a
background in `static/backgrounds/`.
