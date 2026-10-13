#!/usr/bin/env python3
"""Generate the Raven logo SVGs.

A clean, geometric redraw of the original line-drawn raven:

- one long tapered line sweeps from the tail tip up the back into the head
  (colour 2);
- the head is an @: a ring with the eye inside, the ring curling in to meet
  it, and a tapered beak off the crown; the same stroke drops down the chest
  and spirals into the torso, with one leg and a foot (colour 1).

Every curve is a true circle, ellipse, spiral or cubic, joined with matching
tangents, so the mark stays crisp. Each colourway is a few hex values; adding
one is one line below.

Needs numpy and shapely (pip install numpy shapely), which merge the strokes
into clean outlines. Run from anywhere:

    python3 scripts/logo/generate.py

Writes assets/logo.svg (the default colourway) and every colourway to
assets/brand/. Render PNGs from these SVGs (see assets/brand/README.md).
"""
import math
import os

import numpy as np
from shapely import affinity
from shapely.geometry import LineString, Point, Polygon
from shapely.ops import unary_union

# Tile colourways: name: (tile bottom, tile top, bird, back line).
COLORWAYS = {
    "raven": ("#D6246E", "#E8457F", "#FFFFFF", "#FFB3D1"),
    "classic": ("#A8641F", "#C27A30", "#FFF8EE", "#F2C890"),
    "minimal-dark": ("#111113", "#2A2A2E", "#F5F5F5", "#9A9AA2"),
    "minimal-light": ("#F5F5F2", "#FFFFFF", "#111113", "#8A8A92"),
}
DEFAULT = "raven"

# Mark-only (transparent) colourways: name: (bird, back line).
MARKS = {
    "raven": ("#D6246E", "#F59BBF"),
    "classic": ("#A8641F", "#E3B07A"),
    "ink": ("#111113", "#8A8A92"),
    "white": ("#FFFFFF", "#9A9AA2"),
}

# Geometry on a 1024 canvas.
STROKE = 38
HEAD = (712, 205)        # centre of the @ ring
HEAD_R = 80              # ring centreline radius
EYE_R = 24               # solid eye
EYE_OFFSET = (4, 0)
RING_EXIT = 140          # angle where the ring leaves the head for the neck
CURL = 95                # degrees the ring curls inward towards the eye
CURL_GAP = 8
TORSO = dict(c=(530, 420), r0=122, r1=56, a0=-66, a1=250, sx=0.62, rot=35)
NECK = (60, 50)          # tangent lengths of the head-to-torso join
TAIL = [(118, 880), (250, 580), (410, 300)]  # tail tip and two controls
BACK_AT = 165            # angle on the ring where the back line arrives
BEAK_AT = 280            # angle on the ring where the beak leaves
BEAK_TIP = (925, 232)
BEAK_CONTROLS = (90, (-20, -70))
LEG = [(505, 508), (540, 560), (595, 575), (600, 642)]
FOOT = [(548, 652), (664, 642)]
SHIFT = (-10, 19)        # centres the mark on the tile


def _ellipse(c, r0, r1, a0, a1, sx=1.0, rot=0.0, n=300):
    t = np.linspace(0, 1, n)
    a = np.radians(a0 + (a1 - a0) * t)
    r = r0 + (r1 - r0) * t
    x, y = r * np.cos(a) * sx, r * np.sin(a)
    ro = math.radians(rot)
    return np.stack([c[0] + x * math.cos(ro) - y * math.sin(ro),
                     c[1] + x * math.sin(ro) + y * math.cos(ro)], 1)


def _cubic(p0, p1, p2, p3, n=120):
    t = np.linspace(0, 1, n)[:, None]
    p0, p1, p2, p3 = map(np.asarray, (p0, p1, p2, p3))
    return (1 - t) ** 3 * p0 + 3 * (1 - t) ** 2 * t * p1 + 3 * (1 - t) * t * t * p2 + t ** 3 * p3


def _direction(pts, at_end):
    d = pts[-1] - pts[-4] if at_end else pts[3] - pts[0]
    return d / np.linalg.norm(d)


def _on_head(deg):
    a = math.radians(deg)
    return np.array([HEAD[0] + HEAD_R * math.cos(a), HEAD[1] + HEAD_R * math.sin(a)])


def _stroke(pts, width):
    return LineString(pts).buffer(width / 2, cap_style="round", join_style="round")


def _tapered(pts, width_at):
    """A stroke whose width follows width_at(s) for s from 0 to 1 along it."""
    d = np.gradient(pts, axis=0)
    d /= np.linalg.norm(d, axis=1)[:, None]
    normal = np.stack([-d[:, 1], d[:, 0]], 1)
    s = np.r_[0, np.cumsum(np.linalg.norm(np.diff(pts, axis=0), axis=1))]
    half = np.array([width_at(x) for x in s / s[-1]])[:, None] / 2
    return Polygon(np.vstack([pts + normal * half, (pts - normal * half)[::-1]])).buffer(0)


def geometry():
    """Return (bird, back line) as shapely geometries."""
    n = 400
    angles = np.radians(np.linspace(RING_EXIT, RING_EXIT - 360 - CURL, n))
    radii = np.full(n, float(HEAD_R))
    k = int(n * CURL / (360 + CURL))
    radii[-k:] = np.linspace(HEAD_R, EYE_R + STROKE / 2 + CURL_GAP, k)
    # Runs from the inner end of the curl out round the ring to the neck.
    ring = np.stack([HEAD[0] + radii * np.cos(angles), HEAD[1] + radii * np.sin(angles)], 1)[::-1]
    torso = _ellipse(TORSO["c"], TORSO["r0"], TORSO["r1"], TORSO["a0"], TORSO["a1"],
                     TORSO["sx"], TORSO["rot"])
    neck = _cubic(ring[-1], ring[-1] + _direction(ring, True) * NECK[0],
                  torso[0] - _direction(torso, False) * NECK[1], torso[0])
    eye = Point(HEAD[0] + EYE_OFFSET[0], HEAD[1] + EYE_OFFSET[1]).buffer(EYE_R)
    back = _tapered(_cubic(*TAIL, _on_head(BACK_AT)), lambda s: 6 + (STROKE - 6) * s ** 0.45)
    start = _on_head(BEAK_AT)
    tip = np.array(BEAK_TIP)
    beak_pts = _cubic(start, start + np.array([BEAK_CONTROLS[0], 0]),
                      tip + np.array(BEAK_CONTROLS[1]), tip)
    beak = _tapered(beak_pts, lambda s: STROKE + (5 - STROKE) * s ** 1.3)
    bird = unary_union([
        _stroke(np.vstack([ring, neck, torso]), STROKE),
        eye,
        beak,
        _stroke(_cubic(*LEG), STROKE * 0.85),
        _stroke(np.array(FOOT), STROKE * 0.85),
    ])
    line = back.difference(bird)
    return affinity.translate(bird, *SHIFT), affinity.translate(line, *SHIFT)


def _path(geom):
    d = []
    for poly in getattr(geom, "geoms", [geom]):
        poly = poly.simplify(0.25)
        for ring in [poly.exterior, *poly.interiors]:
            pts = list(ring.coords)[:-1]
            d.append("M" + "L".join(f"{x:.1f} {y:.1f}" for x, y in pts) + "Z")
    return "".join(d)


def _svg(inner):
    return ('<svg xmlns="http://www.w3.org/2000/svg" width="1024" height="1024" '
            f'viewBox="0 0 1024 1024">\n{inner}</svg>\n')


def icon(name, bird_d, line_d):
    bottom, top, bird, line = COLORWAYS[name]
    gid = f"raven-bg-{name}"
    return _svg(
        f'  <defs><linearGradient id="{gid}" x1="0" y1="0" x2="0" y2="1">'
        f'<stop offset="0" stop-color="{top}"/><stop offset="1" stop-color="{bottom}"/>'
        "</linearGradient></defs>\n"
        f'  <rect width="1024" height="1024" rx="229" fill="url(#{gid})"/>\n'
        f'  <path fill="{line}" d="{line_d}"/>\n'
        f'  <path fill="{bird}" fill-rule="evenodd" d="{bird_d}"/>\n'
    )


def mark(name, bird_d, line_d):
    bird, line = MARKS[name]
    return _svg(
        f'  <path fill="{line}" d="{line_d}"/>\n'
        f'  <path fill="{bird}" fill-rule="evenodd" d="{bird_d}"/>\n'
    )


def main():
    root = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
    brand = os.path.join(root, "assets", "brand")
    os.makedirs(brand, exist_ok=True)
    bird, line = geometry()
    bird_d, line_d = _path(bird), _path(line)
    for name in COLORWAYS:
        with open(os.path.join(brand, f"logo-{name}.svg"), "w") as f:
            f.write(icon(name, bird_d, line_d))
    for name in MARKS:
        with open(os.path.join(brand, f"mark-{name}.svg"), "w") as f:
            f.write(mark(name, bird_d, line_d))
    with open(os.path.join(root, "assets", "logo.svg"), "w") as f:
        f.write(icon(DEFAULT, bird_d, line_d))


if __name__ == "__main__":
    main()
