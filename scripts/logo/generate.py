#!/usr/bin/env python3
"""Generate the Raven logo SVGs.

The mark is a solid raven in two tones (body + wing) with an @ for the eye.
Each colourway is just three hex values, so adding a new one is one line in
COLORWAYS. Run from the repo root:

    python3 scripts/logo/generate.py

Writes assets/logo.svg (the default "raven" colourway) and every colourway
to assets/brand/. Render PNGs from these SVGs (see assets/brand/README.md).
"""
import math
import os

# name: (background, background-top for the subtle gradient, body, wing)
COLORWAYS = {
    "raven": ("#D6246E", "#E8457F", "#FFFFFF", "#FFB3D1"),
    "classic": ("#A8641F", "#C27A30", "#FFF8EE", "#F2C890"),
    "minimal-dark": ("#111113", "#2A2A2E", "#F5F5F5", "#8A8A92"),
    "minimal-light": ("#F5F5F2", "#FFFFFF", "#111113", "#7A7A82"),
}
DEFAULT = "raven"

# Mark-only (transparent) colourways: name: (body, wing).
MARKS = {
    "raven": ("#D6246E", "#F59BBF"),
    "classic": ("#A8641F", "#E3B07A"),
    "ink": ("#111113", "#8A8A92"),
    "white": ("#FFFFFF", "#9A9AA2"),
}

# Shifts the bird so its bounding box sits optically centred on the tile.
MARK_TRANSFORM = "translate(-44 -10)"

BODY = (
    "M 540 205 C 620 190 690 210 740 255 C 800 300 870 350 930 405 "
    "C 935 412 930 420 920 418 C 850 410 770 420 712 448 "
    "C 730 575 690 700 570 775 L 220 885 C 205 890 196 875 206 864 "
    "C 300 765 340 630 370 520 C 395 360 440 225 540 205 Z"
)
WING = "M 405 400 C 560 450 640 600 570 775 L 220 885 C 315 770 370 600 405 400 Z"


def at_eye(color, cx=608, cy=318, outer=46, inner=15, width=15):
    """An @ drawn as strokes: the 'a' bowl, its stem, and the open outer ring."""
    a0, a1 = math.radians(25), math.radians(70)
    sx, sy = cx + outer * math.cos(a0), cy + outer * math.sin(a0)
    ex, ey = cx + outer * math.cos(a1), cy + outer * math.sin(a1)
    stem = cx + inner
    return (
        f'<g fill="none" stroke="{color}" stroke-linecap="round" stroke-width="{width}">'
        f'<circle cx="{cx}" cy="{cy}" r="{inner}"/>'
        f'<path d="M {stem:.1f} {cy - inner:.1f} L {stem:.1f} {cy + inner * 0.6:.1f} '
        f'Q {stem:.1f} {sy + 4:.1f} {sx:.1f} {sy:.1f} '
        f'A {outer} {outer} 0 1 0 {ex:.1f} {ey:.1f}"/></g>'
    )


def mark(body, wing, eye):
    return (
        f'<g transform="{MARK_TRANSFORM}">'
        f'<path fill="{body}" d="{BODY}"/>'
        f'<path fill="{wing}" d="{WING}"/>'
        f"{at_eye(eye)}</g>"
    )


def icon(name):
    bg, top, body, wing = COLORWAYS[name]
    gid = f"raven-bg-{name}"
    return (
        '<svg xmlns="http://www.w3.org/2000/svg" width="1024" height="1024" viewBox="0 0 1024 1024">\n'
        f'  <defs><linearGradient id="{gid}" x1="0" y1="0" x2="0" y2="1">'
        f'<stop offset="0" stop-color="{top}"/><stop offset="1" stop-color="{bg}"/>'
        "</linearGradient></defs>\n"
        f'  <rect width="1024" height="1024" rx="229" fill="url(#{gid})"/>\n'
        f"  {mark(body, wing, bg)}\n"
        "</svg>\n"
    )


def glyph(name):
    """Mark only, transparent background. The eye is cut out so it works on any surface."""
    body, wing = MARKS[name]
    mask = (
        '<mask id="raven-eye" maskUnits="userSpaceOnUse" x="0" y="0" width="1024" height="1024">'
        '<rect width="1024" height="1024" fill="#fff"/>'
        f'<g transform="{MARK_TRANSFORM}">{at_eye("#000")}</g></mask>'
    )
    return (
        '<svg xmlns="http://www.w3.org/2000/svg" width="1024" height="1024" viewBox="0 0 1024 1024">\n'
        f"  <defs>{mask}</defs>\n"
        f'  <g mask="url(#raven-eye)"><g transform="{MARK_TRANSFORM}">'
        f'<path fill="{body}" d="{BODY}"/><path fill="{wing}" d="{WING}"/></g></g>\n'
        "</svg>\n"
    )


def main():
    root = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
    brand = os.path.join(root, "assets", "brand")
    os.makedirs(brand, exist_ok=True)
    for name in COLORWAYS:
        with open(os.path.join(brand, f"logo-{name}.svg"), "w") as f:
            f.write(icon(name))
    for name in MARKS:
        with open(os.path.join(brand, f"mark-{name}.svg"), "w") as f:
            f.write(glyph(name))
    with open(os.path.join(root, "assets", "logo.svg"), "w") as f:
        f.write(icon(DEFAULT))


if __name__ == "__main__":
    main()
