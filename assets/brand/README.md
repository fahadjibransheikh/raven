# Raven logo

![Old logo, then every colourway](preview.png)

A clean, geometric redraw of the original line-drawn raven. One long tapered
line sweeps from the tail up the back (colour 2). The head is an `@` (a ring
curling in to the eye) with a long curved beak, and the same stroke drops down
the chest into a spiral torso, with one leg and a foot (colour 1). Every
colourway is a tile colour plus those two colours.

| File | Use |
| --- | --- |
| `logo-raven.*` | Default app icon (Raven pink accent). Same as `assets/logo.svg`. |
| `logo-classic.*` | Classic (copper) accent preset. |
| `logo-minimal-dark.*` / `logo-minimal-light.*` | Minimal accent preset. |
| `mark-raven.svg`, `mark-classic.svg`, `mark-ink.svg` | Bird only, transparent, for light surfaces. |
| `mark-white.svg` | Bird only, transparent, for dark surfaces. |

The SVGs are generated; edit `scripts/logo/generate.py` and rerun it rather than
editing them by hand. It needs `numpy` and `shapely` to merge the strokes into clean outlines:

```sh
pip install numpy shapely
python3 scripts/logo/generate.py
```

PNGs (`assets/logo.png` 512px, `assets/apple-touch-icon.png` 180px with square
corners, `tauri-wrapper/src-tauri/icons/icon.png`, and the 1024px PNGs here)
are rendered from the SVGs with any SVG renderer (headless Chromium was used).
