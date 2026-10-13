#!/usr/bin/env python3
"""Generate the Raven logo SVGs.

The mark is a raven whose whole head is an @: the outer ring is the skull,
the bowl is the eye, and the beak grows out of the ring. A single crescent
in a second colour is the body. Each colourway is a handful of hex values,
so adding one is one line below.

Needs shapely (pip install shapely), which merges the strokes into clean
outlines. Run from anywhere:

    python3 scripts/logo/generate.py

Writes assets/logo.svg (the default colourway) and every colourway to
assets/brand/. Render PNGs from these SVGs (see assets/brand/README.md).
"""
import math
import os

from shapely import affinity
from shapely.geometry import LineString, Point, Polygon
from shapely.ops import unary_union

# Tile colourways: name: (tile bottom, tile top, head, body).
COLORWAYS = {
    "raven": ("#D6246E", "#E8457F", "#FFFFFF", "#FFB3D1"),
    "classic": ("#A8641F", "#C27A30", "#FFF8EE", "#F2C890"),
    "minimal-dark": ("#111113", "#2A2A2E", "#F5F5F5", "#8A8A92"),
    "minimal-light": ("#F5F5F2", "#FFFFFF", "#111113", "#8A8A92"),
}
DEFAULT = "raven"

# Mark-only (transparent) colourways: name: (head, body).
MARKS = {
    "raven": ("#D6246E", "#F59BBF"),
    "classic": ("#A8641F", "#E3B07A"),
    "ink": ("#111113", "#8A8A92"),
    "white": ("#FFFFFF", "#9A9AA2"),
}

# Geometry on a 1024 canvas.
HEAD = (590, 370)       # centre of the @ ring
RING_R = 168            # ring centreline radius
STROKE = 58             # one stroke weight for ring, bowl and stem
BOWL_R = 58             # centreline radius of the @ bowl (the eye)
STEM_JOIN = 16          # angle where the bowl's stem joins the ring
RING_END = 60           # angle of the @'s open end (lower right)
BEAK_BASE = 28          # angle where the beak's underside meets the ring
BEAK_TIP = (920, 405)
SHIFT = (-20, -22)      # optical centring on the tile


def _arc(c, r, a0, a1, n=240):
    return [
        (c[0] + r * math.cos(math.radians(a0 + (a1 - a0) * i / n)),
         c[1] + r * math.sin(math.radians(a0 + (a1 - a0) * i / n)))
        for i in range(n + 1)
    ]


def _bezier(p0, p1, p2, p3, n=96):
    pts = []
    for i in range(n + 1):
        t = i / n
        u = 1 - t
        pts.append(tuple(
            u ** 3 * p0[k] + 3 * u * u * t * p1[k] + 3 * u * t * t * p2[k] + t ** 3 * p3[k]
            for k in (0, 1)
        ))
    return pts


def _on_ring(r, deg):
    a = math.radians(deg)
    return (HEAD[0] + r * math.cos(a), HEAD[1] + r * math.sin(a))


def geometry():
    """Return (head, body) as shapely geometries."""
    half = STROKE / 2
    ring = LineString(_arc(HEAD, RING_R, STEM_JOIN, RING_END - 360)).buffer(half)
    bowl = Point(HEAD).buffer(BOWL_R + half).difference(Point(HEAD).buffer(BOWL_R - half))

    stem_x = HEAD[0] + BOWL_R
    join = _on_ring(RING_R, STEM_JOIN)
    stem_line = [(stem_x, HEAD[1] - BOWL_R)] + _bezier(
        (stem_x, HEAD[1]), (stem_x, HEAD[1] + 36), (join[0] - 30, join[1] + 10), join)
    stem = unary_union([LineString(stem_line).buffer(half, cap_style="flat"),
                        Point(join).buffer(half - 1)])

    outer = RING_R + half
    crown = (HEAD[0], HEAD[1] - outer)
    base = _on_ring(outer, BEAK_BASE)
    tx, ty = BEAK_TIP
    upper = _bezier(crown, (crown[0] + 175, crown[1]), (tx - 95, ty - 115), BEAK_TIP)
    lower = _bezier(BEAK_TIP, (tx - 60, ty + 36), (base[0] + 50, base[1]), base)
    beak = Polygon(upper + lower + _arc(HEAD, RING_R - half + 1, BEAK_BASE, -90)).buffer(0)

    head = unary_union([ring, bowl, stem, beak])
    body = Polygon(
        _bezier((455, 330), (455, 560), (380, 760), (125, 885))
        + _bezier((125, 885), (480, 930), (750, 760), (745, 330))
    ).buffer(0)
    # The body tucks behind the head; the inside of the ring stays open.
    body = body.difference(Point(HEAD).buffer(RING_R)).difference(head)
    return affinity.translate(head, *SHIFT), affinity.translate(body, *SHIFT)


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


def icon(name, head_d, body_d):
    bottom, top, head, body = COLORWAYS[name]
    gid = f"raven-bg-{name}"
    return _svg(
        f'  <defs><linearGradient id="{gid}" x1="0" y1="0" x2="0" y2="1">'
        f'<stop offset="0" stop-color="{top}"/><stop offset="1" stop-color="{bottom}"/>'
        "</linearGradient></defs>\n"
        f'  <rect width="1024" height="1024" rx="229" fill="url(#{gid})"/>\n'
        f'  <path fill="{body}" d="{body_d}"/>\n'
        f'  <path fill="{head}" fill-rule="evenodd" d="{head_d}"/>\n'
    )


def mark(name, head_d, body_d):
    head, body = MARKS[name]
    return _svg(
        f'  <path fill="{body}" d="{body_d}"/>\n'
        f'  <path fill="{head}" fill-rule="evenodd" d="{head_d}"/>\n'
    )


def main():
    root = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
    brand = os.path.join(root, "assets", "brand")
    os.makedirs(brand, exist_ok=True)
    head, body = geometry()
    head_d, body_d = _path(head), _path(body)
    for name in COLORWAYS:
        with open(os.path.join(brand, f"logo-{name}.svg"), "w") as f:
            f.write(icon(name, head_d, body_d))
    for name in MARKS:
        with open(os.path.join(brand, f"mark-{name}.svg"), "w") as f:
            f.write(mark(name, head_d, body_d))
    with open(os.path.join(root, "assets", "logo.svg"), "w") as f:
        f.write(icon(DEFAULT, head_d, body_d))


if __name__ == "__main__":
    main()
