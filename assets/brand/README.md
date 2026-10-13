# Raven logo

![Old logo, then every colourway](preview.png)

A solid, hunched raven built from two circles (head and body), their common
tangent, a wedge bill and a wedge tail. The `@` is carved out of the bird as a
single cut: a ring round the eye plus one sweeping arc down the breast, and the
eye is the @'s lowercase "a" (a counter, which doubles as a catchlight, and a
stem on its right). That
cut is also the colour boundary, so the two-colour split needs no extra line:
head, bill and breast are the bird colour; back, wing and tail are the accent.

See `construction.svg` for the circles and tangents.

| File | Use |
| --- | --- |
| `logo-raven.*` | Default app icon (Raven pink accent). Same as `assets/logo.svg`. |
| `logo-raven-light.*` | Pink accent on a light tile. |
| `logo-classic.*` / `logo-classic-light.*` | Classic (copper) accent preset. |
| `logo-minimal-light.*` / `logo-minimal-dark.*` | Minimal accent preset. |
| `mark-raven.svg`, `mark-classic.svg`, `mark-ink.svg` | Bird only, transparent, for light surfaces. |
| `mark-white.svg` | Bird only, transparent, for dark surfaces. |

The SVGs are generated; edit `scripts/logo/generate.py` and rerun it rather than
editing them by hand. It needs `numpy` and `shapely`:

```sh
pip install numpy shapely
python3 scripts/logo/generate.py
```

PNGs (`assets/logo.png` 512px, `assets/apple-touch-icon.png` 180px with square
corners, `tauri-wrapper/src-tauri/icons/icon.png`, and the 1024px PNGs here)
are rendered from the SVGs with any SVG renderer (headless Chromium was used).
