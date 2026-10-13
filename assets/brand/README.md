# Raven logo

![Old logo, then every colourway](preview.png)

A raven whose whole head is an `@`: the outer ring is the skull, the bowl is
the eye, and the beak grows out of the ring. One crescent in a second colour is
the body. Every colourway is a tile colour plus head and body colours.

| File | Use |
| --- | --- |
| `logo-raven.*` | Default app icon (Raven pink accent). Same as `assets/logo.svg`. |
| `logo-classic.*` | Classic (copper) accent preset. |
| `logo-minimal-dark.*` / `logo-minimal-light.*` | Minimal accent preset. |
| `mark-raven.svg`, `mark-classic.svg`, `mark-ink.svg` | Bird only, transparent, for light surfaces. |
| `mark-white.svg` | Bird only, transparent, for dark surfaces. |

The SVGs are generated; edit `scripts/logo/generate.py` and rerun it rather than
editing them by hand. It needs `shapely` to merge the strokes into clean outlines:

```sh
pip install shapely
python3 scripts/logo/generate.py
```

PNGs (`assets/logo.png` 512px, `assets/apple-touch-icon.png` 180px with square
corners, `tauri-wrapper/src-tauri/icons/icon.png`, and the 1024px PNGs here)
are rendered from the SVGs with any SVG renderer (headless Chromium was used).
