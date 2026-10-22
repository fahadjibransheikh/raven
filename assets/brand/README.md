# Raven logo

![Old logo, then every colourway](preview.png)

A solid, hunched raven built from two circles (head and body), their common
tangent, a wedge bill and a wedge tail. The `@` is carved out of the bird as a
single cut: a ring round the eye plus one sweeping arc down the breast. That
cut is also the colour boundary, so the two-colour split needs no extra line:
head, bill and breast are the bird colour; back, wing and tail are the accent.

See `construction.svg` for the circles and tangents.

| File | Use |
| --- | --- |
| `logo-raven.*` | Default app icon: white bird, pink wing, black tile. Same as `assets/logo.svg`. |
| `logo-raven-light.*` | Pink accent on a light tile. |
| `logo-classic.*` / `logo-classic-light.*` | Classic (copper) accent preset; the dark one is a black tile like `logo-raven`. |
| `logo-minimal-light.*` / `logo-minimal-dark.*` | Minimal accent preset. |
| `mark-raven.svg`, `mark-classic.svg`, `mark-ink.svg` | Bird only, transparent, for light surfaces. |
| `mark-white.svg` | Bird only, transparent, for dark surfaces. |
| `touch-logo-*` | 180px square-cornered home-screen icon for each `logo-*` tile (apple-touch-icon). |
| `app-logo-*.svg` | Each tile on Apple's icon grid (824px tile in a 1024 canvas), the source of the Dock `logo-*.png` and `icon.icns`. |

The SVGs are generated; edit `scripts/logo/generate.py` and rerun it rather than
editing them by hand. It needs `numpy` and `shapely`:

```sh
pip install numpy shapely
python3 scripts/logo/generate.py
```

Then render the PNGs and the macOS `icon.icns` (needs `rsvg-convert`, from
`brew install librsvg`):

```sh
scripts/logo/render.sh
```

The 1024px `logo-*.png` here are the desktop Dock icons, so they are rendered
from `app-logo-*.svg` and have a transparent margin; the web uses the
full-bleed SVGs.
