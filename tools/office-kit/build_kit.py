# The virtual office's asset kit: shared code for the build_*.py sets.
# Run build_all.py in Blender (docs/office-kit.md); each set exports one GLB per
# piece into internal/web/static/office/sets/cyberpunk/. Geometry is authored in three.js coordinates (Y up, front +Z) and rotated to
# Blender's Z up (front -Y) at the end, so the glTF export lands back in three.js
# exactly as authored. Neon surfaces all use RS_Neon_Accent: recolor it in three.js
# by material name (emissive) to change the highlight color.
import bpy, bmesh, math, random, os
import numpy as np
from mathutils import Vector, Matrix, Euler
from contextlib import contextmanager

OUT = os.environ.get('OFFICE_KIT_OUT') or os.path.normpath(
    os.path.join(os.path.dirname(__file__), '..', '..', 'internal', 'web', 'static', 'office', 'sets', 'cyberpunk'))
os.makedirs(OUT, exist_ok=True)

# ---------- collections: each set builds into its own, cleared on rebuild ----------
def use_collection(name):
    global coll
    coll = bpy.data.collections.get(name) or bpy.data.collections.new(name)
    if coll.name not in bpy.context.scene.collection.children:
        bpy.context.scene.collection.children.link(coll)
    for o in list(coll.objects):
        bpy.data.objects.remove(o, do_unlink=True)
    for me in [m for m in bpy.data.meshes if m.users == 0]:  # keeps rebuilt mesh names free of .001 suffixes
        bpy.data.meshes.remove(me)
    return coll


def quad_uvs(objs, prefixes=('RS_Screen', 'RS_Poster', 'RS_City', 'RS_RackDisplay')):
    """Map each textured quad (screens, posters, backdrop) to the full 0..1 image."""
    for o in objs.values():
        me = o.data
        idx = {i for i, m in enumerate(me.materials) if m and m.name.startswith(prefixes)}
        if not idx:
            continue
        layer = me.uv_layers.get('UVMap') or me.uv_layers.new(name='UVMap')
        for poly in me.polygons:
            if poly.material_index in idx and len(poly.loop_indices) == 4:
                for li, c in zip(poly.loop_indices, [(0, 0), (1, 0), (1, 1), (0, 1)]):
                    layer.data[li].uv = c


# TRIM are the tiling textures, metres per repeat: the floor (one sheet per
# 4 m tile) and walls on the 4 m grid, and the worn paint every prop is coated in (scuffs, panel seams).
TRIM = {'RS_Floor': 4, 'RS_Wall': 2, 'RS_Body': 1, 'RS_BodyDark': 1, 'RS_PaintViolet': 1}


def trim_uvs(o):
    """Planar UVs for the floor and wall textures (TRIM metres per repeat), projected
    along each face's main axis in the piece's own coordinates, so pieces on
    the 4 m grid continue each other's pattern."""
    me = o.data
    idx = {i for i, m in enumerate(me.materials) if m and m.name in TRIM}
    if not idx:
        return
    layer = me.uv_layers.get('UVMap') or me.uv_layers.new(name='UVMap')
    for poly in me.polygons:
        if poly.material_index not in idx:
            continue
        n = poly.normal
        ax = max(range(3), key=lambda i: abs(n[i]))
        for li in poly.loop_indices:
            co = me.vertices[me.loops[li].vertex_index].co
            u, v = ((co.y, co.z), (co.x, co.z), (co.x, co.y))[ax]
            sz = TRIM[me.materials[poly.material_index].name]
            layer.data[li].uv = (u / sz, v / sz)


def export_all(objs):
    for o in objs.values():
        o.data.update()
        bpy.ops.object.select_all(action='DESELECT')
        for c in [o, *o.children_recursive]:
            c.select_set(True)
            if c.type == 'MESH':
                c.data.name = c.name  # glTF mesh names match their nodes
                trim_uvs(c)
        bpy.context.view_layer.objects.active = o
        o.location = (0, 0, 0)
        # .gltf + .bin with the textures as files beside them: the dashboard's CSP
        # (default-src 'self') refuses the blob: URLs three.js makes for embedded images
        bpy.ops.export_scene.gltf(filepath=os.path.join(OUT, o.name + '.gltf'), use_selection=True,
                                  export_format='GLTF_SEPARATE', export_texture_dir='', export_apply=True,
                                  use_active_scene=True)  # other scenes' selections stay out


def to_bl(x, y, z):
    """three.js (Y up, front +Z) -> Blender (Z up, front -Y)."""
    return Vector((x, -z, y))


def part(parent, name, fn, pivot=(0, 0, 0)):
    """A separately animatable child node. fn draws around (0,0,0) = the pivot;
    pivot is in the parent's local three.js coordinates."""
    a = Asset(name)
    fn(a)
    o = a.build()
    o.parent = parent
    o.location = to_bl(*pivot)
    bpy.context.view_layer.update()
    return o


def add_colliders(root, boxes='aabb'):
    """Hidden collision mesh <root>_COL: 'aabb' = one box around root and its visual
    children, or a list of (w, h, d, x, y, z) boxes in root-local three.js coordinates."""
    if not boxes:
        return None
    bpy.context.view_layer.update()
    inv = root.matrix_world.inverted()
    if boxes == 'aabb':
        pts = [p for p in (inv @ o.matrix_world @ v.co for o in [root, *root.children_recursive]
                           if o.type == 'MESH' and '_COL' not in o.name for v in o.data.vertices)
               if p.z > .04]  # Blender Z = height: floor-level cables, rings and decals don't block
        if not pts:
            return None
        lo = Vector((min(p[i] for p in pts) for i in range(3)))
        hi = Vector((max(p[i] for p in pts) for i in range(3)))
        c, d = (lo + hi) / 2, hi - lo
        boxes = [(max(d.x, .02), max(d.z, .02), max(d.y, .02), c.x, c.z, -c.y)]
    cols = []
    for i, (w, h, d, x, y, z) in enumerate(boxes):
        # one node per box (<root>_COL, <root>_COL1, ...) so engines can use box shapes
        a = Asset(f"{root.name}_COL{i if i else ''}")
        a.box(w, h, d, None, x, y, z)
        col = a.build()
        col.parent = root  # identity parent inverse: built in root-local coordinates
        col.display_type = 'WIRE'
        col.hide_render = True
        cols.append(col)
    return cols


def layout(objs, y0=0.0, width=16):
    """Catalog grid for viewing in Blender (after export, which needs the origin)."""
    x, y, row_h = 0.0, y0, 0
    for o in objs.values():
        d = o.dimensions
        if x + d.x > width:
            x, y, row_h = 0.0, y + row_h + 1.0, 0
        o.location = (x + d.x / 2, y + d.y / 2, 0)
        x += d.x + 0.8
        row_h = max(row_h, d.y)


def lin(h):
    h = h.lstrip('#')
    c = [int(h[i:i + 2], 16) / 255 for i in (0, 2, 4)]
    return tuple(x / 12.92 if x <= 0.04045 else ((x + 0.055) / 1.055) ** 2.4 for x in c)


def sock(node, ident):
    return next(s for s in node.inputs if s.identifier == ident)


def mat(name, base='#000000', metal=0.0, rough=0.5, emit=None, strength=0.0, alpha=1.0, image=None, emit_image=False, normal=None):
    m = bpy.data.materials.get(name) or bpy.data.materials.new(name)
    m.use_nodes = True
    nt = m.node_tree
    for n in [n for n in nt.nodes if n.type in ('TEX_IMAGE', 'NORMAL_MAP')]:
        nt.nodes.remove(n)
    b = next(n for n in nt.nodes if n.type == 'BSDF_PRINCIPLED')
    sock(b, 'Base Color').default_value = (*lin(base), 1)
    sock(b, 'Metallic').default_value = metal
    sock(b, 'Roughness').default_value = rough
    sock(b, 'Alpha').default_value = alpha
    if emit:
        sock(b, 'Emission Color').default_value = (*lin(emit), 1)
        sock(b, 'Emission Strength').default_value = strength
    if image:
        t = nt.nodes.new('ShaderNodeTexImage')
        t.image = image
        nt.links.new(t.outputs['Color'], sock(b, 'Emission Color' if emit_image else 'Base Color'))
        if emit_image:
            sock(b, 'Emission Strength').default_value = strength
    if normal:  # glTF's normalTexture: the relief catches the room's lights
        normal.colorspace_settings.name = 'Non-Color'
        t = nt.nodes.new('ShaderNodeTexImage')
        t.image = normal
        nm = nt.nodes.new('ShaderNodeNormalMap')
        nt.links.new(t.outputs['Color'], nm.inputs['Color'])
        nt.links.new(nm.outputs['Normal'], sock(b, 'Normal'))
    if alpha < 1:
        m.surface_render_method = 'BLENDED'
    m.diffuse_color = (*lin(emit or base), 1)
    return m


def make_image(name, w, h, draw):
    px = np.zeros((h, w, 4), np.float32)
    px[..., 3] = 1
    draw(px, w, h)  # drawn top-down
    px = px[::-1].copy()
    img = bpy.data.images.get(name)
    if img and (img.size[0] != w or img.size[1] != h):
        bpy.data.images.remove(img); img = None
    img = img or bpy.data.images.new(name, w, h, alpha=True)
    img.pixels.foreach_set(px.ravel())
    img.pack()  # kept in the .blend and embedded in each GLB; no file on disk
    return img


def draw_screen(px, w, h):
    r = random.Random(7)
    px[0:5, :, :3] = 0.9
    for y in range(16, h - 8, 11):
        x = 10 + r.randrange(4) * 14
        for _ in range(1 + r.randrange(5)):
            if x >= w * 0.6:
                break
            L = int(8 + r.random() * 60)
            px[y:y + 5, x:min(x + L, w), :3] = 0.2 + r.random() * 0.65
            x += L + 6
    x0, pw = int(w * 0.65), int(w * 0.32)
    px[14:16, x0:x0 + pw, :3] = 0.85; px[14 + int(h * .42):16 + int(h * .42), x0:x0 + pw, :3] = 0.85
    px[14:16 + int(h * .42), x0:x0 + 2, :3] = 0.85; px[14:16 + int(h * .42), x0 + pw - 2:x0 + pw, :3] = 0.85
    prev = None
    for i in range(41):  # line graph
        x = x0 + 4 + i * (pw - 8) // 40
        y = int(14 + h * 0.38 - r.random() * h * 0.3)
        if prev:
            for t in np.linspace(0, 1, 12):
                xx, yy = int(prev[0] + (x - prev[0]) * t), int(prev[1] + (y - prev[1]) * t)
                px[yy:yy + 2, xx:xx + 2, :3] = 0.9
        prev = (x, y)
    for i in range(10):
        bh = int(10 + r.random() * h * 0.32)
        bx = x0 + i * (pw // 10) + 2
        px[h - 10 - bh:h - 10, bx:bx + pw // 10 - 5, :3] = 0.45 + r.random() * 0.5


def smooth_noise(rng, n, size):
    """Tileable value noise: an n×n random grid, bilinear up to size×size."""
    g = rng.random((n, n))
    t = np.arange(size) * n / size
    i0 = np.floor(t).astype(int)
    f = (t - i0)[:, None]
    i1 = (i0 + 1) % n
    rows = g[i0] * (1 - f) + g[i1] * f            # (size, n): rows interpolated
    fx = (t - i0)[None, :]
    return rows[:, i0] * (1 - fx) + rows[:, i1] * fx


# Floor plates: a 4 m sheet of sci-fi deck panels, mostly plain and bolted
# quarters, with a grate, drilled holes, vent slots, a hatch and an X brace,
# drawn as a height field; draw_floor colours it, draw_floor_normal makes its normal map.
FLOOR_PANELS = [  # (x0, y0, x1, y1) in 0.5 m cells of the 4 m sheet, the kind; mostly plain plates
    (0, 0, 4, 4, 'quarters'), (4, 0, 8, 2, 'plain'), (4, 2, 6, 4, 'grate'), (6, 2, 8, 4, 'hatch'),
    (0, 4, 2, 8, 'plain'), (2, 4, 6, 6, 'slots'), (6, 4, 8, 8, 'quarters'),
    (2, 6, 4, 8, 'perf'), (4, 6, 6, 8, 'xbrace'),
]
_floor = {}


def inset(X, Y, w, h, c):
    """Distance inside a w×h rectangle at the origin with corners chamfered by c (px)."""
    dx, dy = np.minimum(X, w - X), np.minimum(Y, h - Y)
    return np.minimum(np.minimum(dx, dy), (dx + dy - c) / 1.414)


def floor_fields(size):
    """The height field (0 groove .. 1 bolt head) and a per-pixel shade."""
    if size in _floor:
        return _floor[size]
    rng = np.random.default_rng(11)
    H = np.zeros((size, size), np.float32)
    S = np.ones((size, size), np.float32)  # albedo multiplier: holes dark
    cell = size // 8
    for x0, y0, x1, y1, kind in FLOOR_PANELS:
        X0, Y0, X1, Y1 = x0 * cell, y0 * cell, x1 * cell, y1 * cell
        w, h = X1 - X0, Y1 - Y0
        Y, X = np.mgrid[0:h, 0:w].astype(np.float32)
        h_ = H[Y0:Y1, X0:X1]
        s_ = S[Y0:Y1, X0:X1]
        d = inset(X, Y, w, h, 26)
        plate = np.clip((d - 5) / 6, 0, 1) * .55  # seam groove, bevelled edge, plate at .55
        h_[:] = plate
        s_[:] = .9 + rng.random() * .2
        m = 30  # the frame around the panel's feature
        Xi, Yi, wi, hi = X - m, Y - m, w - 2 * m, h - 2 * m
        di = inset(Xi, Yi, wi, hi, 16)
        inner = di > 0
        if kind == 'grate':  # square holes between bars
            s = 56
            hole = inner & ((Xi % s) > 12) & ((Yi % s) > 12)
            h_[inner] = .5
            h_[hole] = .08
            s_[hole] = .25
        elif kind == 'perf':  # a frame with drilled holes
            h_[inner] = np.minimum(h_[inner], .48)
            s = 48
            r2 = ((Xi % s) - s / 2) ** 2 + ((Yi % s) - s / 2) ** 2
            hole = (di > 10) & (r2 < 110)
            h_[hole] = .12
            s_[hole] = .3
        elif kind == 'slots':  # rows of rounded vent slots
            h_[inner] = .5
            sh = 72
            sw = (wi + 40) / max(1, round((wi + 40) / 300)) - 40  # whole slots across
            ly, lx = Yi % sh, Xi % (sw + 40)
            cx = np.clip(lx, 18, sw - 18)
            slot = (di > 8) & ((lx - cx) ** 2 + (ly - sh / 2) ** 2 < 260) & (lx < sw)
            h_[slot] = .1
            s_[slot] = .3
        elif kind == 'plain':  # a plate with one inset line
            h_[inner & (di < 4)] -= .08
        elif kind == 'quarters':  # four plates and a cross seam
            gx, gy = np.abs(X - w / 2), np.abs(Y - h / 2)
            seam = (np.minimum(gx, gy) < 4) & (d > 12)
            h_[seam] = .28
            for bx, by in ((w / 2, h / 2),):
                b = (X - bx) ** 2 + (Y - by) ** 2
                h_[b < 300] = .62
        elif kind == 'hatch':  # a raised inner plate with a recessed handle
            q = di
            h_[inner] = .55 + np.clip(q[inner] / 8, 0, 1) * .12
            hx, hy, hw, hh = wi / 2 - 70, hi * .2, 140, 36
            hd = inset(Xi - hx, Yi - hy, hw, hh, 10)
            hole = hd > 0
            h_[hole] = .3 + np.clip(hd[hole] / 6, 0, 1) * -.15
            s_[hole] = .55
            lines = inner & (np.abs(Yi - hi * .62) < 3)
            h_[lines] -= .1
        elif kind == 'xbrace':  # an X brace and hub over a recessed plate
            h_[inner] = .36  # a recessed plate under the brace
            s_[inner] = .7
            u, v = Xi / wi, Yi / hi
            bar = inner & ((np.abs(u - v) * min(wi, hi) < 12) | (np.abs(u + v - 1) * min(wi, hi) < 12) |
                           (np.minimum(di, 99) < 10))
            h_[bar] = .56
            s_[bar] = 1
            hub = (Xi - wi / 2) ** 2 + (Yi - hi / 2) ** 2
            h_[hub < 900] = .62
            h_[hub < 200] = .5
        # bolts in the frame's corners
        bolts = [(18, 18), (w - 18, 18), (18, h - 18), (w - 18, h - 18)]
        if max(w, h) > 1.5 * min(w, h):  # long panels: one more each side, midway
            bolts += [(w / 2, 18), (w / 2, h - 18)] if w > h else [(18, h / 2), (w - 18, h / 2)]
        for bx, by in bolts:
            b = (X - bx) ** 2 + (Y - by) ** 2
            h_[b < 100] = .62
            h_[b < 64] = .66 + .1 * np.sqrt(np.clip(64 - b[b < 64], 0, 64)) / 8
            s_[(b >= 64) & (b < 100)] = .5
    # wear: scuffs lighten, grime darkens the low parts
    grime = .78 + .35 * smooth_noise(rng, 8, size) * (.7 + .3 * smooth_noise(rng, 48, size))
    S *= grime * (.55 + .45 * np.clip(H / .55, 0, 1))
    for _ in range(70):
        x0, y0 = rng.integers(0, size), rng.integers(0, size)
        ang, L = rng.random() * np.pi, rng.integers(20, 160)
        t = np.arange(L)
        xs, ys = (x0 + np.cos(ang) * t).astype(int) % size, (y0 + np.sin(ang) * t).astype(int) % size
        S[ys, xs] = np.minimum(S[ys, xs] * 1.15, 1.2)
    _floor[size] = (H, S)
    return H, S


def draw_floor(px, w, h):
    H, S = floor_fields(w)
    px[..., :3] = np.array((.29, .30, .33), np.float32) * S[..., None]


def draw_floor_normal(px, w, h):
    """Tangent-space normals of the height field (+Y up the image, as glTF)."""
    H, _ = floor_fields(w)
    k = 14.0
    gx = (np.roll(H, -1, 1) - np.roll(H, 1, 1)) / 2
    gy = (np.roll(H, -1, 0) - np.roll(H, 1, 0)) / 2  # down the drawn rows
    n = np.stack([-gx * k, gy * k, np.ones_like(H)], -1)
    n /= np.linalg.norm(n, axis=-1, keepdims=True)
    px[..., :3] = n * .5 + .5


def draw_wall(px, w, h):
    """Wall panels, 2 m square (tiles): 1 m panels with inset bevels and rivets,
    a vent panel, a hatch; streaks of grime running down."""
    rng = np.random.default_rng(11)
    yy, xx = np.mgrid[0:h, 0:w]
    ppm = w // 2
    px[..., :3] = (0.27, 0.28, 0.31)
    lx, ly = xx % ppm, yy % (ppm // 2)  # panels 1 m wide, 0.5 m high
    qx, qy = xx // ppm, yy // (ppm // 2)
    px[(lx < 5) | (ly < 4), :3] = 0.06
    inset = ((lx > 18) & (lx < 24)) | ((ly > 16) & (ly < 21))
    px[inset & (lx > 18) & (lx < ppm - 18) & (ly > 16) & (ly < ppm // 2 - 16), :3] = 0.36
    vent = (qx == 1) & (qy == 2) & (lx > 70) & (lx < ppm - 70) & (ly > 50) & (ly < ppm // 2 - 50) & (ly % 22 < 9)
    px[vent, :3] = 0.04
    hatch = (qx == 0) & (qy == 1) & (abs(lx - ppm // 2) < 90) & (abs(ly - ppm // 4) < 70)
    px[hatch & ((abs(lx - ppm // 2) > 84) | (abs(ly - ppm // 4) > 64)), :3] = 0.1
    for cx in (12, ppm - 12):
        for cy in (10, ppm // 2 - 10):
            px[((lx - cx) ** 2 + (ly - cy) ** 2) < 20, :3] = 0.45
    streak = smooth_noise(rng, 24, w)[0][None, :] * np.ones((h, 1))  # vertical grime streaks
    grime = 0.75 + 0.3 * smooth_noise(rng, 8, w) - 0.18 * streak * (yy / h)
    px[..., :3] *= grime[..., None]


def draw_paint(rgb):
    """Worn paint on metal, 1 m per repeat: the colour mottled a little, sparse
    panel seams (a dark groove, a lit lip) with screws beside them, scratches
    and chipped spots down to bare metal, grime. Like the arcade's and the
    noodle machine's: used, never broken."""
    base = np.array([int(rgb[i:i + 2], 16) / 255 for i in (1, 3, 5)])  # sRGB, as the images store it

    def draw(px, w, h):
        rng = np.random.default_rng(int(rgb[1:], 16) % 9973)
        yy, xx = np.mgrid[0:h, 0:w]
        mott = 0.9 + 0.2 * smooth_noise(rng, 6, w) * (0.7 + 0.3 * smooth_noise(rng, 24, w))
        px[..., :3] = base * mott[..., None]
        bare = np.array((0.30, 0.31, 0.33))
        # seams: one vertical per metre, horizontals at 0.35 and 0.75 m
        for c in ((xx % w) < 3, (np.abs(yy - int(h * .35)) < 2), (np.abs(yy - int(h * .75)) < 2)):
            px[c, :3] *= 0.25
        for c in (((xx % w) >= 3) & ((xx % w) < 5), (yy - int(h * .35) == 2), (yy - int(h * .75) == 2)):
            px[c, :3] = np.minimum(px[c, :3] * 1.6 + 0.02, 1)
        for sx in (10, w - 10):  # screws along the vertical seam
            for sy in range(24, h, 96):
                d = (xx - sx) ** 2 + (yy - sy) ** 2
                px[d < 10, :3] = bare * 1.2
                px[(d >= 10) & (d < 16), :3] *= 0.4
        for _ in range(45):  # scratches through the paint
            x0, y0 = rng.integers(0, w), rng.integers(0, h)
            ang, L = rng.random() * np.pi, rng.integers(8, 70)
            for t in range(L):
                x, y = int(x0 + np.cos(ang) * t) % w, int(y0 + np.sin(ang) * t) % h
                px[y, x, :3] = px[y, x, :3] * 0.65 + bare * 0.35
        chips = smooth_noise(rng, 90, w) > 0.95  # a few small chipped spots
        px[chips, :3] = bare * (0.8 + 0.4 * rng.random((chips.sum(), 1)))
        grime = 0.78 + 0.3 * smooth_noise(rng, 10, w)
        px[..., :3] *= grime[..., None]
    return draw


def draw_city(px, w, h):
    r = random.Random(3)
    g = np.linspace(0, 1, h)[:, None]
    for c, (top, bot) in enumerate([(0.016, 0.15), (0.02, 0.165), (0.043, 0.267)]):
        px[:, :, c] = top + (bot - top) * g
    lit = [(1, .85, .63), (.62, .85, 1), (1, 1, 1), (1, .56, .82)]
    for layer, (shade, lo, hi, gap, prob, a) in enumerate([((.047, .06, .118), 120, 380, 2, .18, .45), ((.02, .024, .04), 200, 490, 20, .12, .85)]):
        x = -20
        while x < w:
            bw, bh = int(30 + r.random() * 90), int(lo + r.random() * (hi - lo))
            xa, xb = max(x, 0), min(x + bw, w)
            if xb > xa:
                px[h - bh:h, xa:xb, :3] = shade
                for wy in range(h - bh + 8, h - 6, 9):
                    for wx in range(x + 5, x + bw - 6, 7):
                        if 0 <= wx < w - 3 and r.random() < prob:
                            col = lit[r.randrange(3) if r.random() < .9 else 3]
                            px[wy:wy + 4, wx:wx + 3, :3] = np.array(col) * a + np.array(shade) * (1 - a)
            x += bw + gap + int(r.random() * gap)


# ---------- materials ----------
ACCENT, SECOND = '#22e6ff', '#8b5cff'
screen_img = make_image('RS_ScreenCode', 512, 320, draw_screen)
city_img = make_image('RS_CityNight', 2048, 1024, draw_city)
M = dict(
    neon=mat('RS_Neon_Accent', emit=ACCENT, strength=4),
    neon2=mat('RS_Neon_Secondary', emit=SECOND, strength=4),
    panel=mat('RS_LightPanel', '#ffffff', emit='#eef4ff', strength=8),
    screen=mat('RS_Screen', '#000000', 0, .25, emit=ACCENT, strength=2, image=screen_img, emit_image=True),
    led=mat('RS_LED_Accent', emit=ACCENT, strength=8),
    ledG=mat('RS_LED_Green', emit='#33ff88', strength=8),
    ledA=mat('RS_LED_Amber', emit='#ffaa22', strength=8),
    rubber=mat('RS_Rubber', '#121316', 0, .55),
    rackDisplay=mat('RS_RackDisplay', '#000000', 0, .3, emit='#0b1a24', strength=1),  # blank: the page draws on it
    body=mat('RS_Body', '#ffffff', .55, .45, image=make_image('RS_Paint_Body', 512, 512, draw_paint('#1c1f27'))),
    dark=mat('RS_BodyDark', '#ffffff', .35, .6, image=make_image('RS_Paint_Dark', 512, 512, draw_paint('#101116'))),
    violet=mat('RS_PaintViolet', '#ffffff', .3, .5, image=make_image('RS_Paint_Violet', 512, 512, draw_paint('#3b2470'))),
    trim=mat('RS_Trim', '#2c3039', .85, .3),
    wall=mat('RS_Wall', '#ffffff', .2, .7, image=make_image('RS_Trim_Wall', 1024, 1024, draw_wall)),
    floor=mat('RS_Floor', '#ffffff', .35, .5, image=make_image('RS_Trim_Floor', 2048, 2048, draw_floor),
              normal=make_image('RS_Trim_Floor_Normal', 2048, 2048, draw_floor_normal)),
    fabric=mat('RS_Fabric', '#1b1c23', 0, .95),
    cushion=mat('RS_Cushion', '#2a2c36', 0, .85),
    pot=mat('RS_Pot', '#2b2d33', .2, .55),  # charcoal ceramic
    terracotta=mat('RS_Pot_Terracotta', '#9a5236', 0, .8),
    concrete=mat('RS_Pot_Concrete', '#4f5256', 0, .9),
    soil=mat('RS_Soil', '#17110c', 0, 1),
    leaf=mat('RS_Leaf', '#2f6e3c', 0, .65),
    leaf2=mat('RS_LeafDark', '#1e4a29', 0, .7),
    paper=mat('RS_Paper', '#cfd3da', 0, .9),
    glass=mat('RS_Glass', '#0e1c26', .3, .05, alpha=.22),
    city=mat('RS_City', '#000000', 0, 1, emit='#ffffff', strength=1, image=city_img, emit_image=True),
)
# worn, lived-in accents shared by every set
M.update(
    screenOff=mat('RS_ScreenOff', '#050608', .2, .08),
    beige=mat('RS_BeigePlastic', '#2c2f36', .35, .5),  # retro units in gunmetal, in the family
    cardboard=mat('RS_Cardboard', '#4a3a28', 0, .95),  # dark and grimy
    orange=mat('RS_Orange', '#b84f1a', .1, .5),
    red=mat('RS_Red', '#7e1b1e', .3, .45),
    note=mat('RS_Note', '#d9c45a', 0, .9),
    tape=mat('RS_Tape', '#b9a24a', 0, .7),
    cork=mat('RS_Cork', '#3d2f22', 0, .95),
    warm=mat('RS_Glow_Warm', emit='#ff7a2a', strength=5),
)
for k in ('leaf', 'leaf2'):
    M[k].use_backface_culling = False
BOOKS = [mat(f'RS_Book{i}', c, 0, .8) for i, c in enumerate(['#2b2f3a', '#3c4150', '#6b2f3f', '#d9d7d2', '#24384a', '#4a2b55', '#8a8f9c'])]


def rot(rx=0, ry=0, rz=0, order='XYZ'):
    # three.js order 'XYZ' = matrix Rx·Ry·Rz = Blender Euler order reversed
    return Euler((rx, ry, rz), order[::-1]).to_matrix().to_4x4()


# ---------- builder ----------
class Asset:
    def __init__(self, name):
        self.name, self.bm, self.mats, self.stack = name, bmesh.new(), [], [Matrix()]

    def mi(self, m):
        if m not in self.mats:
            self.mats.append(m)
        return self.mats.index(m)

    @contextmanager
    def at(self, x=0, y=0, z=0, rx=0, ry=0, rz=0, order='XYZ', s=None):
        M_ = Matrix.Translation((x, y, z)) @ rot(rx, ry, rz, order)
        if s is not None:
            s = (s, s, s) if isinstance(s, (int, float)) else s
            M_ = M_ @ Matrix.Diagonal((*s, 1))
        self.stack.append(self.stack[-1] @ M_)
        yield
        self.stack.pop()

    def _tag(self, verts, m, smooth=False):
        idx = self.mi(m)
        for f in {f for v in verts for f in v.link_faces}:
            f.material_index = idx
            f.smooth = smooth

    def box(self, w, h, d, m, x=0, y=0, z=0, rx=0, ry=0, rz=0):
        T = self.stack[-1] @ Matrix.Translation((x, y, z)) @ rot(rx, ry, rz) @ Matrix.Diagonal((w, h, d, 1))
        self._tag(bmesh.ops.create_cube(self.bm, size=1, matrix=T)['verts'], m)

    def cyl(self, rt, rb, h, m, x=0, y=0, z=0, seg=24, rx=0, rz=0):
        T = self.stack[-1] @ Matrix.Translation((x, y, z)) @ rot(rx, 0, rz) @ Matrix.Rotation(-math.pi / 2, 4, 'X')
        v = bmesh.ops.create_cone(self.bm, cap_ends=True, segments=seg, radius1=rb, radius2=rt, depth=h, matrix=T)['verts']
        self._tag(v, m, smooth=True)

    def raw(self, pts, faces, m, smooth=False):
        T = self.stack[-1]
        vs = [self.bm.verts.new(T @ Vector(p)) for p in pts]
        for f in faces:
            try:
                self.bm.faces.new([vs[i] for i in f])
            except ValueError:
                pass
        self._tag(vs, m, smooth)

    def sweep(self, pts, r, m, closed=False, normal=(0, 0, 1), seg=8):
        """Tube through points lying in a plane with the given normal."""
        P, N, n = [Vector(p) for p in pts], Vector(normal), len(pts)
        ring = []
        for i in range(n):
            a = P[(i + 1) % n] if (closed or i < n - 1) else P[i]
            b = P[i - 1] if (closed or i > 0) else P[i]
            t = (a - b).normalized()
            side = t.cross(N).normalized()
            for k in range(seg):
                ang = 2 * math.pi * k / seg
                ring.append(P[i] + r * (math.cos(ang) * N + math.sin(ang) * side))
        faces = []
        for i in range(n if closed else n - 1):
            j = (i + 1) % n
            for k in range(seg):
                k2 = (k + 1) % seg
                faces.append((i * seg + k, i * seg + k2, j * seg + k2, j * seg + k))
        if not closed:
            faces += [tuple(range(seg))[::-1], tuple((n - 1) * seg + k for k in range(seg))]
        self.raw(ring, faces, m, smooth=True)

    def torus(self, R, r, m, seg=40, tseg=8, arc=None):
        """Torus in the XY plane; arc (radians, from +X) makes an open bent tube."""
        full = arc is None
        rings = seg if full else seg + 1
        span = 2 * math.pi if full else arc
        pts, faces = [], []
        for i in range(rings):
            a = span * i / seg
            for k in range(tseg):
                b = 2 * math.pi * k / tseg
                pts.append(((R + r * math.cos(b)) * math.cos(a), (R + r * math.cos(b)) * math.sin(a), r * math.sin(b)))
        for i in range(seg):
            for k in range(tseg):
                i2, k2 = (i + 1) % rings, (k + 1) % tseg
                faces.append((i * tseg + k, i2 * tseg + k, i2 * tseg + k2, i * tseg + k2))
        if not full:
            faces += [tuple(range(tseg))[::-1], tuple(seg * tseg + k for k in range(tseg))]
        self.raw(pts, faces, m, smooth=True)

    def cable(self, ctrl, r, m, seg=5):
        """Round cable through 3D control points (Catmull-Rom, parallel-transport frames)."""
        P = [Vector(p) for p in ctrl]
        n = len(P)
        pts = []
        for i in range(n - 1):
            p0, p1, p2, p3 = P[max(i - 1, 0)], P[i], P[i + 1], P[min(i + 2, n - 1)]
            steps = max(2, int((p2 - p1).length / 0.1))
            for k in range(steps):
                t = k / steps
                pts.append(0.5 * ((2 * p1) + (p2 - p0) * t + (2 * p0 - 5 * p1 + 4 * p2 - p3) * t * t + (3 * p1 - p0 - 3 * p2 + p3) * t ** 3))
        pts.append(P[-1])
        T = [(pts[min(i + 1, len(pts) - 1)] - pts[max(i - 1, 0)]).normalized() for i in range(len(pts))]
        up = Vector((0, 1, 0)) if abs(T[0].y) < 0.9 else Vector((1, 0, 0))
        N = T[0].cross(up).normalized()
        ring = []
        for i, p in enumerate(pts):
            if i:
                axis = T[i - 1].cross(T[i])
                if axis.length > 1e-6:
                    N = Matrix.Rotation(T[i - 1].angle(T[i]), 3, axis.normalized()) @ N
            B = T[i].cross(N)
            for k in range(seg):
                ang = 2 * math.pi * k / seg
                ring.append(p + r * (math.cos(ang) * N + math.sin(ang) * B))
        L = len(pts)
        faces = [(i * seg + k, i * seg + (k + 1) % seg, (i + 1) * seg + (k + 1) % seg, (i + 1) * seg + k) for i in range(L - 1) for k in range(seg)]
        faces += [tuple(range(seg))[::-1], tuple((L - 1) * seg + k for k in range(seg))]
        self.raw(ring, faces, m, smooth=True)

    def ico(self, r, m, x=0, y=0, z=0, jitter=0.0, seed=0):
        """Low-poly sphere; jitter > 0 crumples it (paper balls)."""
        T = self.stack[-1] @ Matrix.Translation((x, y, z))
        vs = bmesh.ops.create_icosphere(self.bm, subdivisions=1, radius=r, matrix=T)['verts']
        if jitter:
            rr = random.Random(seed)
            for v in vs:
                v.co += Vector((rr.uniform(-1, 1), rr.uniform(-1, 1), rr.uniform(-1, 1))) * r * jitter
        self._tag(vs, m)

    def sphere(self, r, m, x=0, y=0, z=0, s=(1, 1, 1), seg=12, rings=8, rx=0, ry=0, rz=0):
        T = self.stack[-1] @ Matrix.Translation((x, y, z)) @ rot(rx, ry, rz) @ Matrix.Diagonal((*s, 1))
        vs = bmesh.ops.create_uvsphere(self.bm, u_segments=seg, v_segments=rings, radius=r, matrix=T)['verts']
        self._tag(vs, m, smooth=True)

    def plane(self, w, h, m, x=0, y=0, z=0):
        self.raw([(x - w / 2, y - h / 2, z), (x + w / 2, y - h / 2, z), (x + w / 2, y + h / 2, z), (x - w / 2, y + h / 2, z)], [(0, 1, 2, 3)], m)

    def leaf(self, m):
        def bez(p0, p1, p2, p3, t):
            u = 1 - t
            return [u ** 3 * p0[i] + 3 * u * u * t * p1[i] + 3 * u * t * t * p2[i] + t ** 3 * p3[i] for i in (0, 1)]
        pts = [bez((0, 0), (.5, .25), (.4, .8), (0, 1), t / 8) for t in range(8)] + \
              [bez((0, 1), (-.4, .8), (-.5, .25), (0, 0), t / 8) for t in range(8)]
        self.raw([(x, y, 0) for x, y in pts], [tuple(range(16))], m)

    # neon helpers ------------------------------------------------------
    def frame(self, w, h, t=0.012, m=None):
        m = m or M['neon']
        self.box(w + t, t, t, m, 0, h / 2, 0); self.box(w + t, t, t, m, 0, -h / 2, 0)
        self.box(t, h, t, m, -w / 2, 0, 0); self.box(t, h, t, m, w / 2, 0, 0)

    def loop(self, w, h, corner=0.15, r=0.014, m=None):
        self.sweep(rounded_pts(w, h, corner), r, m or M['neon'], closed=True)

    def build(self):
        me = bpy.data.meshes.new(self.name)
        self.bm.to_mesh(me)
        self.bm.free()
        for m in self.mats:
            if m is not None:  # collider meshes carry no material
                me.materials.append(m)
        me.transform(Matrix.Rotation(math.pi / 2, 4, 'X'))  # Y-up authoring -> Blender Z-up
        o = bpy.data.objects.new(self.name, me)
        coll.objects.link(o)
        return o


def rounded_pts(w, h, c, n=8):
    c = min(c, w / 2, h / 2)
    out = []
    for cx, cy, a0 in ((w / 2 - c, -h / 2 + c, -90), (w / 2 - c, h / 2 - c, 0), (-w / 2 + c, h / 2 - c, 90), (-w / 2 + c, -h / 2 + c, 180)):
        for i in range(n + 1):
            a = math.radians(a0 + 90 * i / n)
            out.append((cx + c * math.cos(a), cy + c * math.sin(a), 0))
    return out


# ---------- assets (three.js coordinates: Y up, front +Z, meters) ----------
def desk(a, width=2.0, depth=0.8, height=0.75):
    top, pw = 0.04, 0.44
    ph = height - top
    a.box(width, top, depth, M['body'], 0, height - top / 2, 0)
    a.box(width, .008, .008, M['neon'], 0, height - top - .004, depth / 2 - .004)
    a.box(width - 2 * pw - .08, .3, .02, M['dark'], 0, height - top - .15, -depth / 2 + .04)
    for s in (-1, 1):
        x, front = s * (width / 2 - pw / 2 - .02), depth / 2 - .04
        a.box(pw, ph, depth - .06, M['dark'], x, ph / 2, -.01)
        dh = (ph - .06) / 3
        for i in range(3):
            y = .05 + dh * i + dh / 2
            with a.at(x, y, front + .004):
                a.frame(pw - .05, dh - .03, .006)
            a.box(.12, .008, .008, M['neon'], x, y + dh / 2 - .045, front + .01)
        a.box(pw, .006, .006, M['neon'], x, .004, front + .01)


SITSTAND_LOW, SITSTAND_TRAVEL = .74, .36  # a sit-stand desk's top: sitting height, and how far it rises


def sitstand_base(a, width=1.6, depth=.75):
    """A sit-stand desk's frame: T feet, the outer columns, a low crossbar.
    Its top (sitstand_top) is a child node kit.js raises and lowers."""
    leg = width / 2 - .14
    for s in (-1, 1):
        x = s * leg
        a.box(.07, .04, depth - .05, M['trim'], x, .02, 0)                       # foot
        a.box(.004, .004, depth - .1, M['neon'], x, .042, 0)                      # a neon line on it
        a.box(.075, .66 - .04, .075, M['dark'], x, .04 + (.66 - .04) / 2, 0)     # outer column, to 0.66
        a.box(.085, .02, .085, M['trim'], x, .66, 0)                              # its collar
    a.box(2 * leg - .07, .04, .04, M['dark'], 0, .3, -.12)                       # crossbar


def sitstand_top(a, width=1.6, depth=.75, h=SITSTAND_LOW):
    """The top at sitting height h, with the inner columns that slide in the
    base's (down to 0.27, so raised by SITSTAND_TRAVEL they stay in), a frame
    under it and a little control panel at the front right."""
    leg = width / 2 - .14
    a.box(width, .035, depth, M['body'], 0, h - .0175, 0)
    a.box(width, .008, .008, M['neon'], 0, h - .039, depth / 2 - .004)          # the front edge
    a.box(2 * leg + .08, .04, .06, M['dark'], 0, h - .055, 0)                    # frame under the top
    for s in (-1, 1):
        a.box(.06, h - .035 - .27, .06, M['trim'], s * leg, .27 + (h - .035 - .27) / 2, 0)   # inner column
    a.box(.14, .025, .05, M['dark'], width / 2 - .22, h - .05, depth / 2 - .04)  # control panel
    a.box(.03, .006, .004, M['neon2'], width / 2 - .25, h - .045, depth / 2 - .014)   # its up/down light


def monitor(a, width=0.62, height=0.36, stand=True, screen=None):
    y = .2 + height / 2 if stand else 0
    a.box(width, height, .025, M['dark'], 0, y, 0)
    a.plane(width - .02, height - .02, screen or M['screen'], 0, y, .0135)
    a.box(width - .1, .006, .006, M['neon'], 0, y - height / 2 - .004, -.01)
    if stand:
        a.box(.22, .012, .16, M['trim'], 0, .006, -.03)
        a.box(.04, .22, .025, M['trim'], 0, .11, -.05)


def wall_display(a, width=1.6, height=0.9):
    with a.at(0, 0, .03):
        monitor(a, width, height, stand=False)
    a.box(.3, .2, .03, M['trim'], 0, 0, .0)


def keyboard(a):
    a.box(.45, .02, .15, M['dark'], 0, .01, 0)
    a.box(.44, .004, .004, M['neon2'], 0, .004, .074)
    for i in range(60):
        a.box(.022, .008, .022, M['trim'], -.2 + (i % 15) * .0285, .024, -.045 + (i // 15) * .03)


def mug(a):
    a.cyl(.04, .035, .1, M['dark'], 0, .05, 0, 16)
    a.cyl(.032, .032, .002, M['soil'], 0, .099, 0, 16)


def pc_tower(a):
    a.box(.22, .48, .45, M['body'], 0, .24, 0)
    a.box(.004, .42, .4, M['glass'], .112, .25, 0)
    a.box(.006, .4, .006, M['neon'], .06, .25, .226)
    for y in (.15, .33):
        with a.at(.08, y, .12, ry=math.pi / 2):
            a.torus(.065, .006, M['neon'], 24, 6)
        a.cyl(.02, .02, .01, M['dark'], .08, y, .12, 16, rz=math.pi / 2)


def chair(a):
    for i in range(5):
        ang = i / 5 * math.tau
        a.box(.3, .03, .04, M['trim'], math.cos(ang) * .15, .07, -math.sin(ang) * .15, ry=ang)
        a.cyl(.025, .025, .03, M['dark'], math.cos(ang) * .3, .03, -math.sin(ang) * .3, 12, rx=math.pi / 2)
    with a.at(0, .5, -.24, rx=-.12):
        a.box(.46, .6, .07, M['fabric'], 0, .3, 0)
        a.box(.3, .14, .07, M['fabric'], 0, .7, -.02)
        a.box(.006, .5, .006, M['neon2'], 0, .32, -.04)  # the floor's secondary colour
    a.cyl(.025, .025, .36, M['trim'], 0, .26, 0)
    a.box(.5, .08, .48, M['fabric'], 0, .47, 0)
    for s in (-1, 1):
        a.box(.035, .2, .035, M['trim'], s * .27, .6, -.02)
        a.box(.06, .03, .24, M['dark'], s * .27, .71, 0)


def workstation(a, monitors=3, width=2.0, seed=1):
    r, h = random.Random(seed), .75
    desk(a, width, height=h)
    spots = {1: [(0, 0)], 2: [(-.33, .12), (.33, -.12)], 3: [(-.62, .38), (0, 0), (.62, -.38)]}[monitors]
    for x, ry in spots:
        with a.at(x, h, -.15 + abs(ry) * .25, ry=ry):
            monitor(a)
    with a.at(0, h, .15): keyboard(a)
    with a.at(width / 2 - .25, h, .1): mug(a)
    a.box(.21, .006, .3, M['paper'], -width / 2 + .3, h + .003, .12, ry=.2)
    with a.at(width / 2 + .2, 0, -.1): pc_tower(a)
    with a.at(0, 0, .75, ry=math.pi + (r.random() - .5) * .4): chair(a)


def server_rack(a, width=0.6, height=2.0, depth=1.0, seed=1):
    """A stylized rack: a chunky bolted frame in worn paint with a neon edge
    down each side (the floor's secondary colour), an accent bar on top and
    a small status display (RS_RackDisplay, blank: the page draws the
    service on it), then stacked modules: patch panels of ports, LED grids,
    a vent. The LEDs and the bar are RS_LED_Accent: the page lights them in
    the service's state. seed reorders the modules."""
    r = random.Random(seed)
    fw, zf = .045, depth / 2
    inner = width - 2 * fw
    a.box(width - .02, height - .02, depth - .05, M['body'], 0, height / 2, -.025)       # the cabinet
    for s_ in (-1, 1):
        x = s_ * (width / 2 - fw / 2)
        a.box(fw, height, .05, M['dark'], x, height / 2, zf - .025)                        # front posts
        a.box(.008, height - .12, .012, M['neon2'], s_ * (width / 2 + .003), height / 2, zf - .03)
        for y in (.1, height * .36, height * .64, height - .1):                             # bolts
            a.cyl(.01, .01, .012, M['trim'], x, y, zf + .004, 12, rx=math.pi / 2)
    a.box(width, .08, .05, M['dark'], 0, height - .04, zf - .025)                          # head
    a.box(width, .1, .05, M['dark'], 0, .05, zf - .025)                                    # plinth
    a.box(inner - .1, .016, .012, M['led'], 0, height - .04, zf + .004)                    # accent bar
    y = height - .08 - .13
    a.box(inner - .02, .22, .012, M['dark'], 0, y, zf - .03)
    a.box(inner - .08, .16, .004, M['rackDisplay'], 0, y, zf - .022)                       # status display
    mods = ['ports', 'leds', 'leds', 'ports', 'leds']
    if seed != 1:
        r.shuffle(mods)
    mods.append('vent')
    size = {'ports': .2, 'leds': .3, 'vent': .26}
    top = y - .13
    for m in mods:
        h = size[m]
        cy = top - h / 2
        a.box(inner - .02, h - .02, .012, M['dark'], 0, cy, zf - .03)                       # module face
        a.box(inner - .02, .004, .004, M['trim'], 0, cy + h / 2 - .012, zf - .022)          # its lip
        if m == 'ports':
            for row in (.035, -.035):
                for k in range(6):
                    a.box(.028, .022, .006, M['trim'], -inner / 2 + .07 + k * .048, cy + row, zf - .022)
                    a.box(.018, .012, .004, M['dark'], -inner / 2 + .07 + k * .048, cy + row, zf - .018)
            a.box(.01, .01, .004, M['led'], inner / 2 - .05, cy + .05, zf - .02)
        elif m == 'leds':
            cols, rows = 8, 5
            for gx in range(cols):
                for gy in range(rows):
                    if r.random() < .82:
                        a.box(.014, .014, .004, M['led'], -inner / 2 + .06 + gx * .042, cy - .085 + gy * .042, zf - .02)
        else:
            for k in range(7):
                a.box(inner - .08, .014, .006, M['trim'], 0, cy - .1 + k * .033, zf - .022)
        top -= h
    # armoured sides: raised plates, bolts at their corners, vent slots
    for s_ in (-1, 1):
        x = s_ * (width / 2 + .01)
        for py, ph in ((.55, .9), (1.45, .7)):
            a.box(.02, ph, depth - .22, M['body'], x, py, -.02)
            for by in (py - ph / 2 + .05, py + ph / 2 - .05):
                for bz in (-depth / 2 + .16, depth / 2 - .2):
                    a.cyl(.012, .012, .014, M['trim'], x + s_ * .011, by, bz, 10, rz=math.pi / 2)
        for k in range(5):
            a.box(.006, .012, .3, M['dark'], x + s_ * .011, 1.25 + k * .03, -.1)
        # cable bundles out of the side, looping down to the floor
        for k in range(3 + r.randrange(2)):
            y0, z0 = .9 + k * .28 + r.random() * .1, .1 - k * .14
            out = .2 + r.random() * .14
            a.cyl(.032, .032, .05, M['trim'], s_ * (width / 2 + .045), y0, z0, 12, rz=math.pi / 2)   # collar
            a.cable([(s_ * (width / 2 + .05), y0, z0), (s_ * (width / 2 + out), y0 - .12, z0 + .05),
                     (s_ * (width / 2 + out * .8), y0 * .45, z0 - .05), (s_ * (width / 2 + .12 + k * .03), .03, z0 - .12)],
                    .022, M['rubber'], seg=7)
    # a bundle up from the top into the ceiling's cable tray (4 m up)
    for k in range(4):
        x0, z0 = -.18 + k * .12, -.2 + (k % 2) * .12
        a.cable([(x0, height - .01, z0), (x0 + .05, height + .5, z0 + .15), (x0 * 1.6, height + 1.3, z0 - .1), (x0 * 1.3, 3.98, z0 - .2)],
                .024, M['rubber'], seg=7)


def sofa(a, width=2.2, depth=0.9):
    arm = .18
    inner = width - 2 * arm
    n = max(1, round(inner / .7))
    cw = inner / n
    a.box(width - .04, .22, depth - .04, M['dark'], 0, .16, 0)
    a.box(width, .46, .22, M['cushion'], 0, .58, -depth / 2 + .11)
    a.box(width - .1, .006, .006, M['neon'], 0, .05, depth / 2 - .02)
    for i in range(n):
        x = -inner / 2 + cw * (i + .5)
        a.box(cw - .02, .14, depth - .24, M['cushion'], x, .34, .1)
        a.box(cw - .06, .32, .12, M['fabric'], x, .6, -depth / 2 + .27)
    for s in (-1, 1):
        a.box(arm, .32, depth, M['cushion'], s * (width / 2 - arm / 2), .43, 0)


def coffee_table(a, width=1.2, depth=0.7, height=0.42):
    a.box(width, .05, depth, M['trim'], 0, height - .025, 0)
    with a.at(0, height - .04, 0, rx=-math.pi / 2):
        a.loop(width + .02, depth + .02, .08)
    a.box(.3, height - .09, .3, M['dark'], 0, (height - .09) / 2 + .04, 0)
    a.box(width * .75, .04, depth * .75, M['body'], 0, .02, 0)
    with a.at(0, .012, 0, rx=-math.pi / 2):
        a.loop(width * .75 + .02, depth * .75 + .02, .06)
    a.box(.21, .004, .29, M['paper'], -.2, height + .002, .05, ry=-.3)
    with a.at(.3, height, -.1): mug(a)


def plant(a, big=True, seed=1, pot=None):
    r = random.Random(seed * 31)
    ph, pr, h = (.42, .2, 1.25) if big else (.12, .06, .3)
    a.cyl(pr, pr * .8, ph, pot or M['pot'], 0, ph / 2, 0)
    a.cyl(pr * .92, pr * .92, .01, M['soil'], 0, ph - .02, 0)
    with a.at(0, ph, 0, rx=math.pi / 2):
        a.torus(pr * 1.01, .004 if big else .002, M['neon2'], seg=32, tseg=6)  # a thin neon rim
    k = 1 if big else .5
    a.cyl(.012 * k, .015 * k, h * .6, M['leaf2'], 0, ph + h * .3, 0, 6)
    for _ in range(26 if big else 9):
        L = (.22 if big else .08) + r.random() * (.18 if big else .06)
        with a.at(0, ph + (.05 + r.random() * .9) * h * .65, 0, .5 + r.random() * .9, r.random() * math.tau, 0, 'YXZ', s=(L * .55, L, 1)):
            a.leaf(M['leaf'] if r.random() < .5 else M['leaf2'])


def hanging_planter(a, drop=1.1, seed=2):
    a.cyl(.004, .004, drop - .2, M['trim'], 0, -(drop - .2) / 2, 0, 6)
    with a.at(0, -drop + .15, 0, s=(1, 1.7, 1)):
        a.torus(.2, .008, M['trim'], 48)
    with a.at(0, -drop, 0, s=1.4):
        plant(a, False, seed, M['dark'])


def bookshelf(a, width=1.6, height=1.9, shelves=4, seed=4):
    r = random.Random(seed * 97)
    for s in range(shelves):
        y = .45 + s * (height - .45) / max(1, shelves - 1)
        a.box(width, .03, .28, M['body'], 0, y, .14)
        a.box(width - .04, .006, .006, M['neon'], 0, y - .02, .27)
        x = -width / 2 + .04
        while x < width / 2 - .06:
            roll = r.random()
            if roll < .08 and x < width / 2 - .2:
                with a.at(x + .07, y + .015, .14):
                    plant(a, False, seed * 10 + s * 7 + int(x * 10))
                x += .18
            elif roll < .18:
                x += .06 + r.random() * .12
            else:
                bw, bh = .025 + r.random() * .03, .17 + r.random() * .11
                lean = r.random() < .06
                a.box(bw, bh, .18 + r.random() * .05, r.choice(BOOKS), x + bw / 2 + (.03 if lean else 0), y + .015 + bh / 2, .14, rz=-.25 if lean else 0)
                x += bw + .003


def neon_sign(a, size=0.8):
    s, m = size, M['neon2']
    a.box(1.5 * s, .9 * s, .04, M['dark'], 0, 0, .02)
    a.sweep([(-.5 * s, .25 * s, .05), (.5 * s, .25 * s, .05), (0, -.3 * s, .05)], .012 * s, m, closed=True)
    a.sweep([(-.28 * s, .14 * s, .05), (0, -.08 * s, .05), (.28 * s, .14 * s, .05)], .012 * s, m)
    for i in range(9):
        a.box(.012 * s, .1 * s, .01, m, (-.4 + i * .1) * s, .36 * s, .045, rz=-.5)


def door(a, width=1.1, height=2.3):
    a.box(width, height, .06, M['dark'], 0, height / 2, 0)
    a.box(.01, height - .1, .07, M['trim'], 0, height / 2, 0)
    a.box(width - .2, .012, .012, M['neon2'], 0, height * .55, .035)
    a.box(.08, .12, .02, M['neon'], width / 2 + .15, 1.2, .08)
    with a.at(0, height / 2, .06):
        a.frame(width + .06, height + .03)


def ceiling_panel(a, width=2.4, depth=1.2):
    a.box(width + .3, .05, depth + .3, M['dark'], 0, -.025, 0)
    a.box(width, .02, depth, M['panel'], 0, -.045, 0)
    with a.at(0, -.03, 0, rx=-math.pi / 2):
        a.frame(width + .16, depth + .16)


def window_insert(a, w, h, corner=.2, depth=.15):
    with a.at(0, 0, -depth / 2):
        a.raw(rounded_pts(w, h, corner), [tuple(range(len(rounded_pts(w, h, corner))))], M['glass'])
        a.loop(w - .02, h - .02, corner, .035, M['trim'])
        panes = max(1, round(w / 1.6))
        for i in range(1, panes):
            a.box(.05, h - .04, .06, M['trim'], -w / 2 + i * w / panes, 0, 0)
    with a.at(0, 0, .015):
        a.loop(w + .06, h + .06, corner + .03, .016)


def wall_trim(a, width, height, windows=(), doors=()):
    # the panel seams are in the wall texture (RS_Trim_Wall)
    segs = [(-width / 2, width / 2)]
    for dx, dw, _ in doors:
        for i, (s0, s1) in enumerate(segs):
            if s0 < dx < s1:
                segs[i:i + 1] = [(s0, dx - dw / 2 - .08), (dx + dw / 2 + .08, s1)]
                break
    for s0, s1 in segs:
        if s1 - s0 > .05:
            a.box(s1 - s0, .026, .018, M['neon'], (s0 + s1) / 2, .03, .009)
    a.box(width, .026, .018, M['neon'], 0, height - .04, .009)
    for wx, wy, ww, wh, c in windows:
        with a.at(wx, wy + wh / 2, 0):
            window_insert(a, ww, wh, c)
    for dx, dw, dh in doors:
        with a.at(dx, 0, -.075):
            door(a, dw, dh)


def make_wall(name, width=4.0, height=3.2, depth=.15, windows=(), doors=()):
    """Wall slab with rounded window holes (boolean), trim, windows, doors. Front face at y=0."""
    a = Asset(name + '_slab')
    a.box(width, height, depth, M['wall'], 0, height / 2, -depth / 2)
    slab = a.build()
    cutters = []
    for wx, wy, ww, wh, c in windows:
        b = Asset('cut'); b.box(ww, wh, depth + .6, M['wall'], wx, wy + wh / 2, -depth / 2); co = b.build()
        bv = co.modifiers.new('bv', 'BEVEL'); bv.width = c; bv.segments = 8
        cutters.append(co)
    for dx, dw, dh in doors:
        b = Asset('cut'); b.box(dw, dh + .1, depth + .6, M['wall'], dx, (dh - .1) / 2, -depth / 2); cutters.append(b.build())
    for co in cutters:
        bo = slab.modifiers.new('cut', 'BOOLEAN'); bo.operation = 'DIFFERENCE'; bo.object = co; bo.solver = 'EXACT'
    bpy.context.view_layer.update()
    dg = bpy.context.evaluated_depsgraph_get()
    me = bpy.data.meshes.new_from_object(slab.evaluated_get(dg))
    slab.modifiers.clear(); old = slab.data; slab.data = me; bpy.data.meshes.remove(old)
    for co in cutters:
        bpy.data.objects.remove(co, do_unlink=True)
    t = Asset(name)
    wall_trim(t, width, height, windows, doors)
    o = t.build()
    join(o, [slab])
    return o


def join(target, others):
    bpy.ops.object.select_all(action='DESELECT')
    for x in [target, *others]:
        x.select_set(True)
    bpy.context.view_layer.objects.active = target
    bpy.ops.object.join()
    return target


def city_backdrop(a, width=30, height=14):
    a.plane(width, height, M['city'], 0, height / 2, 0)
    # UVs are set after build


def floor_tile(a, size=4.0):
    a.plane(size, size, M['floor'])  # authored in XY; rotated flat below; RS_Trim_Floor draws the plates


# ---------- metaballs: smooth organic shapes (animals, cushions) ----------
K = 1 / 0.575  # Blender draws a metaball's surface at 0.575 × its radius (stiffness 2, threshold 0.6)


def chain(pts, r0, r1):
    """Balls along a polyline, radius tapering r0 -> r1 (tails)."""
    V = [Vector(p) for p in pts]
    total = sum((V[i + 1] - V[i]).length for i in range(len(V) - 1))
    out, acc = [], 0.0
    for i in range(len(V) - 1):
        seg = V[i + 1] - V[i]
        n = max(1, int(seg.length / (min(r0, r1) * .8)))
        for k in range(n):
            t = k / n
            out.append(('b', tuple(V[i] + seg * t), r0 + (r1 - r0) * (acc + seg.length * t) / total))
        acc += seg.length
    out.append(('b', tuple(V[-1]), r1))
    return out


def meta(name, elems, res, mats, color=None, target=2000, details=None):
    """Mesh object `name` at the origin from metaball elements in three.js coordinates:
    ('b', p, r) ball, ('e', p, r, (sx, sy, sz)) ellipsoid, ('c', p0, p1, r) capsule."""
    mb = bpy.data.metaballs.new('MB_' + name)
    mb.resolution = mb.render_resolution = res
    mb.threshold = .6
    for e in elems:
        if e[0] == 'b':
            el = mb.elements.new(type='BALL'); el.co = to_bl(*e[1]); el.radius = e[2] * K
        elif e[0] == 'e':
            el = mb.elements.new(type='ELLIPSOID'); el.co = to_bl(*e[1]); el.radius = e[2] * K
            sx, sy, sz = e[3]
            el.size_x, el.size_y, el.size_z = sx, sz, sy  # three.js Y/Z -> Blender Z/Y
        else:
            a_, b_ = to_bl(*e[1]), to_bl(*e[2])
            el = mb.elements.new(type='CAPSULE'); el.co = (a_ + b_) / 2; el.radius = e[3] * K
            el.size_x = (b_ - a_).length / 2
            el.rotation = Vector((1, 0, 0)).rotation_difference((b_ - a_).normalized())
    tmp = bpy.data.objects.new('MBX_' + name, mb)  # unique base name: its own metaball family
    coll.objects.link(tmp)
    bpy.context.view_layer.update()
    me = bpy.data.meshes.new_from_object(tmp.evaluated_get(bpy.context.evaluated_depsgraph_get()))
    bpy.data.objects.remove(tmp); bpy.data.metaballs.remove(mb)
    obj = bpy.data.objects.new(name, me)
    coll.objects.link(obj)
    tris = sum(len(p.vertices) - 2 for p in me.polygons)
    if tris > target:
        obj.modifiers.new('dec', 'DECIMATE').ratio = target / tris
        bpy.context.view_layer.update()
        me2 = bpy.data.meshes.new_from_object(obj.evaluated_get(bpy.context.evaluated_depsgraph_get()))
        obj.modifiers.clear(); obj.data = me2; bpy.data.meshes.remove(me)
    obj.data.shade_smooth()
    for m in mats:
        obj.data.materials.append(m)
    if color:
        for p in obj.data.polygons:
            c, n = p.center, p.normal
            p.material_index = color(Vector((c.x, c.z, -c.y)), Vector((n.x, n.z, -n.y)))
    if details:
        a = Asset(name + '_det'); details(a)
        join(obj, [a.build()])
    return obj


def attach(obj, parent, pivot):
    obj.parent = parent
    obj.location = to_bl(*pivot)
    bpy.context.view_layer.update()
    return obj
