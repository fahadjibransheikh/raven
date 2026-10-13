#!/usr/bin/env python3
"""Generate the Raven logo SVGs.

A solid, hunched raven built from two circles (head, body), their common
tangent, a wedge bill and a wedge tail. The @ is carved out of the mass as a
single cut: an @ glyph engraved round the eye (its bowl's dark centre is the
eye) whose outer ring runs on into one sweeping arc down the breast. That
cut is also the colour boundary, so the two-colour split costs no extra line:
the head, bill and breast are the bird colour, the back, wing and tail are the
accent colour.

Each colourway is three hex values (tile, bird, wing); adding one is one line
below. Needs numpy and shapely (pip install numpy shapely). Run from anywhere:

    python3 scripts/logo/generate.py

Writes assets/logo.svg (the default colourway), every colourway and a
construction drawing to assets/brand/. Render PNGs from these SVGs (see
assets/brand/README.md).
"""
import math, os
import numpy as np
from shapely.geometry import Point, Polygon, LineString, MultiPolygon
from shapely.ops import unary_union
from shapely import affinity

Q = 128  # circle resolution

# ---------------- construction ----------------
# Numbers are in construction space; the bird is then scaled (s) and moved (T)
# so it fits a FIT_R circle round the tile centre. CUT_W and RING_* are final px.
HEAD_C, HEAD_R = np.array([680., 345.]), 205.
BODY_C, BODY_R = np.array([470., 580.]), 290.
CUT_W = 40.                    # final px, width of the breast sweep
RING_OUT = 116.                # final px; outer ring centreline = RING_OUT - CUT_W / 2
RING_IN = RING_OUT - CUT_W     # inner counter radius (final px)
EXIT = np.array([480., 866.])  # where the sweep leaves the hull (spec)
BILL_TIP = np.array([1120., 445.])
TAIL = [(18., 968.), (78., 1004.)]
TAIL_ANG = (165., 112.)        # attach angles on body circle (screen deg, 90 = down)
HOOK = 18.
FIT_R = 452.
OFFSET = (6., -10.)
AT_STROKE = 34.                # final px, stroke of the engraved @
AT_BOWL = 0.40                 # bowl centreline radius, as a fraction of the outer ring
AT_SHIFT = -0.06               # bowl offset left of the head centre (fraction of ring)
AT_JOIN = 25.                  # angle where the stem's foot meets the outer ring
AT_END = 62.                   # angle where the outer ring stops (the @'s open end)


def pt(c, r, deg):
    a = math.radians(deg)
    return np.array([c[0] + r * math.cos(a), c[1] + r * math.sin(a)])


def circle(c, r):
    return Point(*c).buffer(r, quad_segs=Q)


def tangents(c1, r1, c2, r2):
    d = c2 - c1
    L = np.linalg.norm(d)
    phi = math.atan2(d[1], d[0])
    al = math.acos((r1 - r2) / L)
    out = []
    for sgn in (1, -1):
        n = np.array([math.cos(phi + sgn * al), math.sin(phi + sgn * al)])
        out.append((c1 + r1 * n, c2 + r2 * n))
    return out


# ---------------- sweep solve (needs scale s) ----------------
def solve_sweep(s):
    W = CUT_W / s
    r_c = (RING_OUT - CUT_W / 2) / s
    px = 900.
    lo, hi = 300., 900.
    f = lambda py: math.hypot(px - EXIT[0], py - EXIT[1]) - math.hypot(px - HEAD_C[0], py - HEAD_C[1]) - r_c
    for _ in range(80):
        m = (lo + hi) / 2
        if f(m) > 0: lo = m
        else: hi = m
    SW_C = np.array([px, (lo + hi) / 2])
    SW_R = math.hypot(*(SW_C - EXIT))
    u = (HEAD_C - SW_C) / np.linalg.norm(HEAD_C - SW_C)
    return W, r_c, SW_C, SW_R, HEAD_C + r_c * u


def head_cross(SW_C, SW_R):
    # upper-right intersection of sweep circle with head circle -> bill top base
    best = None
    for a in np.linspace(-120, 0, 2401):
        p = pt(HEAD_C, HEAD_R, a)
        e = abs(math.hypot(*(p - SW_C)) - SW_R)
        if best is None or e < best[0]: best = (e, a)
    return best[1]


def make_sil(s):
    W, r_c, SW_C, SW_R, TANG = solve_sweep(s)
    a_top = head_cross(SW_C, SW_R)
    return a_top, W, r_c, SW_C, SW_R, TANG


s = 0.75
for _ in range(8):
    A_ANG, W, r_c, SW_C, SW_R, TANG = make_sil(s)
    tl = tangents(BODY_C, BODY_R, HEAD_C, HEAD_R)
    fr = max(tl, key=lambda q: q[0][0] + q[1][0])
    hull = unary_union([circle(HEAD_C, HEAD_R), circle(BODY_C, BODY_R), Polygon([fr[0], fr[1], HEAD_C, BODY_C])])
    # culmen tangent to the head: forehead flows straight into the bill
    dvec = BILL_TIP - HEAD_C; Ld = np.linalg.norm(dvec)
    tang_a = math.degrees(math.atan2(dvec[1], dvec[0]) - math.acos(HEAD_R / Ld))
    A = pt(HEAD_C, HEAD_R, tang_a)
    B = pt(HEAD_C, HEAD_R, 34)
    ang = math.atan2(BILL_TIP[1] - A[1], BILL_TIP[0] - A[0])
    Lb = np.linalg.norm(BILL_TIP - A)
    Qp = A + 0.86 * Lb * np.array([math.cos(ang), math.sin(ang)])
    ang2 = ang + math.radians(HOOK)
    tip = Qp + 0.14 * Lb * np.array([math.cos(ang2), math.sin(ang2)])
    mid = (B + tip) / 2 + np.array([-4, 22.])
    bill_pts = [HEAD_C, A, Qp, tip]
    for tt in np.linspace(0, 1, 20)[1:]:
        bill_pts.append((1 - tt) ** 2 * tip + 2 * tt * (1 - tt) * mid + tt ** 2 * B)
    bill_pts.append(HEAD_C)
    bill = Polygon(bill_pts)
    TA, TB = pt(BODY_C, BODY_R, TAIL_ANG[0]), pt(BODY_C, BODY_R, TAIL_ANG[1])
    tail = Polygon([TA, TAIL[0], TAIL[1], TB, BODY_C])
    sil = unary_union([hull, bill, tail])
    F = 60
    sil = sil.buffer(F, quad_segs=48).buffer(-F, quad_segs=48)
    bx0, by0, bx1, by1 = sil.bounds
    cen = 0.4 * np.array([(bx0 + bx1) / 2, (by0 + by1) / 2]) + 0.6 * np.array(sil.centroid.coords[0])
    verts = np.array(sil.exterior.coords)
    s = FIT_R / np.max(np.linalg.norm(verts - cen, axis=1))
T = np.array([512., 512.]) + np.array(OFFSET) - s * cen
A_ANG, W, r_c, SW_C, SW_R, TANG = make_sil(s)


def fx(g):
    return affinity.affine_transform(g, [s, 0, 0, s, T[0], T[1]])


def P(p):
    return s * np.asarray(p) + T


a_t = math.degrees(math.atan2(*(TANG - SW_C)[::-1]))
a_e = math.degrees(math.atan2(*(EXIT - SW_C)[::-1]))
a_hd = math.degrees(math.atan2(*(pt(HEAD_C, HEAD_R, A_ANG) - SW_C)[::-1]))
a_e, a_hd, a_t = a_e % 360, a_hd % 360, a_t % 360
arcpts = [pt(SW_C, SW_R, a) for a in np.linspace(a_e - 12, a_t, 400)]
band = LineString(arcpts).buffer(W / 2, cap_style='flat')

# The @ itself, engraved round the eye as one light stroke: the inner bowl (its
# dark centre is the eye), a stem on the bowl's right whose foot curls into the
# outer ring, and the outer ring, open at the lower right. The breast sweep
# leaves the outer ring tangentially, so the @ and the colour boundary are one
# continuous cut.
def at_glyph():
    t = AT_STROKE / s
    R = r_c
    br = AT_BOWL * R
    c = HEAD_C + np.array([AT_SHIFT * R, 0])
    bowl = LineString([pt(c, br, a) for a in np.linspace(0, 360, 200)]).buffer(t / 2)
    sx = c[0] + br
    jp = pt(HEAD_C, R, AT_JOIN)
    p0 = np.array([sx, c[1] + 0.25 * br])
    p1 = p0 + np.array([0, 0.9 * br])
    p2 = jp + np.array([-0.35 * br, 0.55 * br])
    foot = [np.array([sx, c[1] - br]), p0] + [
        (1 - u) ** 3 * p0 + 3 * (1 - u) ** 2 * u * p1 + 3 * (1 - u) * u * u * p2 + u ** 3 * jp
        for u in np.linspace(0, 1, 40)[1:]]
    stem = LineString(foot).buffer(t / 2, cap_style="flat")
    outer = LineString([pt(HEAD_C, R, a) for a in np.linspace(AT_JOIN, AT_END - 360, 300)]).buffer(t / 2)
    return unary_union([bowl, stem, outer, Point(*jp).buffer(t / 2)])


cut = unary_union([band, at_glyph()])

disc = circle(SW_C, SW_R)   # colour split follows the sweep circle
front = unary_union([disc, circle(HEAD_C, HEAD_R + 2)])
white = sil.intersection(front).difference(cut)
grey = sil.difference(front).difference(cut)
whole = sil.difference(cut)


def clean(g, amin=60):
    parts = list(g.geoms) if hasattr(g, 'geoms') else [g]
    parts = [p for p in parts if p.geom_type == 'Polygon' and p.area * s * s > amin]
    return unary_union(parts) if parts else Polygon()


white, grey, whole = clean(white), clean(grey), clean(whole)


def path(g):
    g = fx(g)
    polys = list(g.geoms) if hasattr(g, 'geoms') else [g]
    d = []
    for p in polys:
        for ring_ in [p.exterior] + list(p.interiors):
            c = list(ring_.coords)[:-1]
            d.append('M' + 'L'.join(f'{x:.1f} {y:.1f}' for x, y in c) + 'Z')
    return ''.join(d)



# ---------------- construction overlay ----------------
def cross(p, k=7):
    x, y = P(p)
    return f'<path d="M{x-k:.1f} {y:.1f}H{x+k:.1f}M{x:.1f} {y-k:.1f}V{y+k:.1f}"/>'


def circ(c, r):
    x, y = P(c)
    return f'<circle cx="{x:.1f}" cy="{y:.1f}" r="{r*s:.1f}"/>'


def line(a, b):
    (x0, y0), (x1, y1) = P(a), P(b)
    return f'<path d="M{x0:.1f} {y0:.1f}L{x1:.1f} {y1:.1f}"/>'


ov = []
ov.append(circ(HEAD_C, HEAD_R)); ov.append(circ(BODY_C, BODY_R))
ov.append(cross(HEAD_C)); ov.append(cross(BODY_C))
for a_, b_ in tangents(BODY_C, BODY_R, HEAD_C, HEAD_R):
    ov.append(line(a_, b_))
# bill
ov.append(line(A, BILL_TIP)); ov.append(line(B, BILL_TIP)); ov.append(line(A, B))
# tail wedge
ov.append(line(TA, TAIL[0])); ov.append(line(TB, TAIL[1])); ov.append(line(TAIL[0], TAIL[1]))
ov.append(line(TA, TB))
# ring: centreline, outer, inner
ov.append(circ(HEAD_C, r_c)); ov.append(circ(HEAD_C, r_c + W / 2)); ov.append(circ(HEAD_C, r_c - W / 2))
# sweep circle (full) + centre + tangent point + exit
ov.append(circ(SW_C, SW_R)); ov.append(cross(SW_C, 9)); ov.append(cross(TANG, 5)); ov.append(cross(EXIT, 5))
ov.append(line(SW_C, HEAD_C))
overlay = ('<g fill="none" stroke="#000000" stroke-opacity="0.25" stroke-width="2">' + ''.join(ov) + '</g>'
           '<g fill="none" stroke="#4FA3FF" stroke-opacity="0.5" stroke-width="2">'
           '<rect x="64" y="64" width="896" height="896" rx="170"/>'
           '<circle cx="512" cy="512" r="410"/></g>')


# ---------------- colourways ----------------
# Tile colourways: name: (tile, tile top for a subtle gradient, bird, wing).
COLORWAYS = {
    "raven": ("#D6246E", "#E3407F", "#151517", "#FFB3D1"),
    "raven-light": ("#FBE3EC", "#FDEFF4", "#151517", "#D6246E"),
    "classic": ("#A8641F", "#BA7530", "#151517", "#F2C890"),
    "classic-light": ("#F6E8D6", "#FAF1E6", "#151517", "#A8641F"),
    "minimal-light": ("#EDEBE6", "#F5F4F0", "#151517", "#6A6A72"),
    "minimal-dark": ("#151517", "#26262A", "#F5F5F5", "#7A7A82"),
}
DEFAULT = "raven"

# Mark-only (transparent) colourways: name: (bird, wing).
MARKS = {
    "raven": ("#151517", "#D6246E"),
    "classic": ("#151517", "#A8641F"),
    "ink": ("#151517", "#8A8A92"),
    "white": ("#F5F5F5", "#9A9AA2"),
}

SVG_OPEN = '<svg xmlns="http://www.w3.org/2000/svg" width="1024" height="1024" viewBox="0 0 1024 1024">\n'


def _shapes(bird, wing):
    return (f'  <path fill="{wing}" fill-rule="evenodd" d="{path(grey)}"/>\n'
            f'  <path fill="{bird}" fill-rule="evenodd" d="{path(white)}"/>\n')


def icon(name):
    tile, top, bird, wing = COLORWAYS[name]
    gid = f"raven-bg-{name}"
    return (SVG_OPEN
            + f'  <defs><linearGradient id="{gid}" x1="0" y1="0" x2="0" y2="1">'
            + f'<stop offset="0" stop-color="{top}"/><stop offset="1" stop-color="{tile}"/>'
            + '</linearGradient></defs>\n'
            + f'  <rect width="1024" height="1024" rx="229" fill="url(#{gid})"/>\n'
            + _shapes(bird, wing) + '</svg>\n')


def glyph(name):
    bird, wing = MARKS[name]
    return SVG_OPEN + _shapes(bird, wing) + '</svg>\n'


def construction():
    return (SVG_OPEN + '  <rect width="1024" height="1024" rx="229" fill="#EDEBE6"/>\n'
            + _shapes("#151517", "#6A6A72") + overlay + '\n</svg>\n')


def main():
    root = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
    brand = os.path.join(root, "assets", "brand")
    os.makedirs(brand, exist_ok=True)
    files = {f"logo-{n}.svg": icon(n) for n in COLORWAYS}
    files.update({f"mark-{n}.svg": glyph(n) for n in MARKS})
    files["construction.svg"] = construction()
    for name, text in files.items():
        with open(os.path.join(brand, name), "w") as f:
            f.write(text)
    with open(os.path.join(root, "assets", "logo.svg"), "w") as f:
        f.write(icon(DEFAULT))


if __name__ == "__main__":
    main()
