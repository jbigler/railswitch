# Lounge, room dividers, plants in dark pots, and the clutter that makes a floor
# look lived in. exec build_kit.py first, in the same globals.
use_collection('CyberOfficeLounge')


def line(px, x0, y0, x1, y1, col, w=3):
    """A marker stroke on a top-down image."""
    n = int(max(abs(x1 - x0), abs(y1 - y0))) + 1
    for t in np.linspace(0, 1, n):
        x, y = int(x0 + (x1 - x0) * t), int(y0 + (y1 - y0) * t)
        px[max(0, y - w // 2):y + w // 2 + 1, max(0, x - w // 2):x + w // 2 + 1, :3] = col


def draw_whiteboard(px, w, h):
    """A used glass board, dark, in glowing marker: a service diagram, a
    checklist, scribbles, ghosts of old ones."""
    r = random.Random(4)
    px[..., :3] = 0.05
    for _ in range(30):  # half-erased ghosts
        x, y = r.randrange(w), r.randrange(h)
        line(px, x, y, x + r.randrange(-60, 60), y + r.randrange(-20, 20), (0.1, 0.11, 0.13), 4)
    blue, red, black = (0.2, 0.85, 1.0), (1.0, 0.3, 0.6), (0.82, 0.88, 0.96)  # cyan, pink, near-white marker
    boxes = [(30, 40, 120, 90), (190, 40, 280, 90), (110, 150, 200, 200)]
    for x0, y0, x1, y1 in boxes:
        for a, b, c, d in ((x0, y0, x1, y0), (x1, y0, x1, y1), (x1, y1, x0, y1), (x0, y1, x0, y0)):
            line(px, a, b, c, d, blue)
        for k in range(2):
            line(px, x0 + 10, y0 + 15 + k * 14, x0 + 10 + r.randrange(40, 70), y0 + 15 + k * 14, black, 2)
    line(px, 120, 65, 190, 65, black); line(px, 180, 58, 190, 65, black); line(px, 180, 72, 190, 65, black)
    line(px, 75, 90, 150, 150, black); line(px, 142, 140, 150, 150, black)
    line(px, 235, 90, 165, 150, red, 3)
    for k in range(5):  # a checklist
        y = 40 + k * 26
        line(px, 330, y, 345, y, black, 2); line(px, 345, y, 345, y + 14, black, 2)
        line(px, 345, y + 14, 330, y + 14, black, 2); line(px, 330, y + 14, 330, y, black, 2)
        if k < 3:
            line(px, 333, y + 7, 338, y + 12, red, 2); line(px, 338, y + 12, 344, y + 2, red, 2)
        line(px, 355, y + 7, 355 + r.randrange(60, 130), y + 7, black, 2)
    for k in range(4):  # scribbles
        x0, y0 = 30 + r.randrange(220), 230 + k * 18
        pts = [(x0 + i * 9, y0 + r.randrange(-4, 5)) for i in range(r.randrange(8, 22))]
        for (ax, ay), (bx, by) in zip(pts, pts[1:]):
            line(px, ax, ay, bx, by, black if k % 2 else blue, 2)


L = dict(
    fabric=mat('RS_Fabric_Light', '#9fa3ad', 0, .95),
    leather=mat('RS_Leather_Dark', '#2a2326', .1, .55),
    wood=mat('RS_Wood_Panel', '#5b3521', 0, .55),
    frosted=mat('RS_Glass_Frosted', '#dfe8f0', 0, .35, alpha=.35),
    pink=mat('RS_Fabric_Pink', '#c2557e', 0, .9),
    teal=mat('RS_Fabric_Teal', '#2f8f8a', 0, .9),
    mustard=mat('RS_Fabric_Mustard', '#c49a2c', 0, .9),
    rug=mat('RS_Rug_Base', '#25202e', 0, .97),
    rug2=mat('RS_Rug_Line', '#6b3a6e', 0, .95),
    rug3=mat('RS_Rug_Line2', '#2c6c78', 0, .95),
    board=mat('RS_Whiteboard', '#000000', .2, .15, emit='#ffffff', strength=1.1, image=make_image('RS_Whiteboard', 512, 320, draw_whiteboard), emit_image=True),
    jacket=mat('RS_Jacket', '#5c1f26', 0, .8),
    denim=mat('RS_Denim', '#2e3f5c', 0, .85),
    leafLight=mat('RS_Leaf_Light', '#4f8f3a', 0, .6),
    smoked=mat('RS_Glass_Smoked', '#0c1018', .4, .08, alpha=.55),
    steel=mat('RS_Steel_Dark', '#262a31', .85, .35),
    trunk=mat('RS_Trunk', '#5a4630', 0, .9),
    leatherDark=mat('RS_Leather_Dark', '#1d1b20', 0, .42),
    oxblood=mat('RS_Oxblood', '#4a1518', 0, .85),
    holoBed=mat('RS_HoloBed', '#05070c', .3, .15),
)
for k_ in ('leafLight',):
    L[k_].use_backface_culling = False


# ---------- couches: dark leather, the set's worn paint below ----------
def sofa_l(name):
    """L-shaped sectional in charcoal leather: a painted plinth with a thin
    secondary-neon strip along its base, seat cushions, a channel-quilted
    back (three rolls), an arm at the open end, an oxblood throw on the
    chaise, two pillows. Origin at the floor, seat facing +Z; the chaise
    runs forward on the right."""
    a = Asset(name)
    lth, ox = L['leatherDark'], L['oxblood']
    a.box(2.9, .12, .95, M['dark'], 0, .08, 0)
    a.box(.95, .12, 1.5, M['dark'], .95, .08, 1.2)
    a.box(2.8, .006, .006, M['neon2'], 0, .03, .48)
    a.box(.006, .006, 1.4, M['neon2'], .47, .03, 1.2)
    for x in (-.93, -.02):
        a.box(.88, .16, .62, lth, x, .23, .1)                     # seat cushions
    a.box(.88, .16, 1.9, lth, .95, .23, .92)                      # the chaise's
    for y in (.4, .54, .68):
        a.box(2.8, .12, .17, lth, 0, y, -.37)                     # channel-quilted back
    a.box(.16, .5, .95, lth, -1.37, .36, 0)                       # arm
    a.box(.62, .035, .52, ox, .95, .33, 1.42, ry=.1)              # throw over the chaise
    a.box(.62, .3, .025, ox, .97, .2, 1.9, ry=.1)                 # its end hanging down
    a.box(.42, .36, .12, lth, -1.0, .5, -.2, rx=-.25, ry=.15)     # pillows
    a.box(.4, .34, .12, ox, .55, .5, -.2, rx=-.25, ry=-.1)
    return a.build()


def armchair(a):
    """Charcoal leather lounge chair between painted metal side panels, to
    match the sectional."""
    for s in (-1, 1):
        a.box(.12, .62, .8, M['dark'], s * .39, .31, -.03)
        a.cyl(.06, .06, .62, M['dark'], s * .39, .31, .37, 16)
    a.box(.66, .16, .78, L['leatherDark'], 0, .26, 0)
    a.box(.64, .12, .7, L['leatherDark'], 0, .4, .04)
    a.box(.6, .004, .004, M['neon2'], 0, .462, .39)  # piping
    with a.at(0, .66, -.33, rx=-.16):
        a.box(.66, .62, .15, L['leatherDark'], 0, 0, 0)


def leaf_at(a, m, x, y, z, length, width, tilt, turn):
    with a.at(x, y, z, rx=tilt, ry=turn, order='YXZ', s=(width, length, 1)):
        a.leaf(m)


def snake_plant(a, seed=1):
    r = random.Random(seed)
    a.cyl(.16, .14, .42, M['concrete'], 0, .21, 0, 16)
    a.cyl(.15, .15, .01, M['soil'], 0, .41, 0, 16)
    for i in range(16):
        leaf_at(a, M['leaf2'] if i % 3 else L['leafLight'], (r.random() - .5) * .14, .4, (r.random() - .5) * .14,
                .6 + r.random() * .45, .07 + r.random() * .03, (r.random() - .5) * .35, r.random() * 6)


def monstera(a, seed=2):
    r = random.Random(seed)
    a.cyl(.21, .16, .4, M['terracotta'], 0, .2, 0, 16)
    a.cyl(.19, .19, .01, M['soil'], 0, .39, 0, 16)
    for i in range(9):
        ang = i * 2.4 + r.random()
        h = .75 + r.random() * .55
        d = .2 + r.random() * .3
        tip = (math.cos(ang) * d, h, math.sin(ang) * d)
        a.cable([(0, .4, 0), (tip[0] * .4, .4 + (h - .4) * .7, tip[2] * .4), tip], .008, M['leaf2'], seg=4)
        leaf_at(a, M['leaf'], *tip, .36 + r.random() * .14, .34 + r.random() * .1, 1.0 + r.random() * .4, -ang + math.pi / 2)


def palm(a, seed=3):
    r = random.Random(seed)
    a.cyl(.22, .18, .5, M['pot'], 0, .25, 0, 16)
    a.cyl(.2, .2, .01, M['soil'], 0, .49, 0, 16)
    a.cable([(0, .5, 0), (.03, 1.0, .02), (.06, 1.55, 0)], .035, L['trunk'], seg=6)
    for i in range(11):
        ang = i * 2 * math.pi / 11 + r.random() * .3
        leaf_at(a, r.choice([M['leaf'], L['leafLight']]), .06, 1.55, 0, .8 + r.random() * .3, .16, 1.2 + r.random() * .5, ang)


def bush(a, seed=4):
    r = random.Random(seed)
    a.cyl(.25, .19, .36, M['terracotta'], 0, .18, 0, 16)
    a.cyl(.23, .23, .01, M['soil'], 0, .35, 0, 16)
    for i in range(70):
        u, v = r.random() * 2 * math.pi, math.acos(2 * r.random() - 1)
        d = .3 + r.random() * .08
        x, y, z = d * math.sin(v) * math.cos(u), .75 + d * math.cos(v) * .8, d * math.sin(v) * math.sin(u)
        if y < .42:
            continue
        leaf_at(a, r.choice([M['leaf'], M['leaf2'], L['leafLight']]), x, y, z, .16, .1, v, -u + math.pi / 2)


def trailing_planter(a, seed=5, drop=.9):
    """Hanging pot with vines trailing down. Origin at the ceiling."""
    r = random.Random(seed)
    for k in range(3):
        ang = k * 2 * math.pi / 3
        a.cable([(0, 0, 0), (.13 * math.cos(ang), -drop + .12, .13 * math.sin(ang))], .003, M['trim'], seg=4)
    a.sphere(.15, M['pot'], 0, -drop, 0, s=(1, .7, 1), seg=14, rings=8)
    for k in range(7):
        ang = k * 2 * math.pi / 7 + r.random() * .3
        L_ = .5 + r.random() * .7
        pts = [(.13 * math.cos(ang), -drop + .05, .13 * math.sin(ang))]
        for j in range(1, 6):
            f = j / 5
            pts.append(((.16 + .05 * f) * math.cos(ang) + (r.random() - .5) * .05, -drop + .05 - L_ * f, (.16 + .05 * f) * math.sin(ang) + (r.random() - .5) * .05))
        a.cable(pts, .004, M['leaf2'], seg=3)
        for j in range(1, 10):
            p = pts[min(5, j * 5 // 9)]
            leaf_at(a, r.choice([M['leaf'], L['leafLight']]), p[0], p[1] + (r.random() - .5) * .05, p[2], .08, .06, 1.9 + r.random() * .6, r.random() * 6)


# ---------- lived-in ----------
def book_stack(a, n=4, seed=1):
    r = random.Random(seed)
    y = 0
    for _ in range(n):
        h = .03 + r.random() * .03
        a.box(.17 + r.random() * .06, h, .24 + r.random() * .05, r.choice(BOOKS), 0, y + h / 2, 0, ry=(r.random() - .5) * .5)
        y += h


def side_table(a, seed=1):
    a.cyl(.28, .28, .03, L['wood'], 0, .55, 0, 24)
    a.cyl(.03, .03, .52, M['trim'], 0, .27, 0, 8)
    a.cyl(.2, .22, .02, M['dark'], 0, .01, 0, 20)
    with a.at(-.08, .565, .02): book_stack(a, 3, seed)
    with a.at(.13, .565, -.06): mug(a)
    with a.at(.06, .565, .14, s=.9): plant(a, False, seed + 30, M['terracotta'])


def floor_lamp(a):
    """Arc floor lamp: its shade reaches 0.9 m forward (+Z)."""
    a.cyl(.18, .2, .04, M['dark'], 0, .02, 0, 20)
    a.cable([(0, .04, 0), (0, 1.4, 0), (0, 1.85, .2), (0, 1.88, .55), (0, 1.74, .86)], .014, M['trim'], seg=6)
    a.cyl(.04, .18, .16, M['dark'], 0, 1.66, .9, 20)
    a.cyl(.15, .15, .006, M['warm'], 0, 1.585, .9, 20)


def rug_geo(a):
    """A patterned rug, 2.6 × 1.8 m."""
    a.box(2.6, .008, 1.8, L['rug'], 0, .004, 0)
    for inset, m in ((.1, L['rug2']), (.18, L['rug3'])):
        w, d = 2.6 - 2 * inset, 1.8 - 2 * inset
        a.box(w, .009, .04, m, 0, .0045, d / 2); a.box(w, .009, .04, m, 0, .0045, -d / 2)
        a.box(.04, .009, d, m, w / 2, .0045, 0); a.box(.04, .009, d, m, -w / 2, .0045, 0)
    for i in range(5):  # a row of diamonds
        x = -1.0 + i * .5
        with a.at(x, .0046, 0, ry=math.pi / 4):
            a.box(.28, .009, .28, L['rug2'] if i % 2 else L['rug3'], 0, 0, 0)
            a.box(.16, .0095, .16, L['rug'], 0, .0002, 0)


def coat_rack(a):
    a.cyl(.2, .22, .03, M['dark'], 0, .015, 0, 16)
    a.cyl(.02, .02, 1.75, M['trim'], 0, .89, 0, 8)
    for k in range(4):
        ang = k * math.pi / 2
        a.box(.18, .015, .015, M['trim'], .08 * math.cos(ang), 1.68, .08 * math.sin(ang), ry=-ang, rz=.4)
    with a.at(.16, 1.68, 0, rz=.08):  # a jacket on its hook
        a.box(.42, .64, .12, L['jacket'], .05, -.36, 0)
        for s in (-1, 1):
            a.box(.1, .55, .1, L['jacket'], .05 + s * .24, -.4, 0, rz=s * .08)
        a.box(.12, .04, .13, L['jacket'], .05, -.03, 0)
    with a.at(-.12, 1.74, .02):  # a cap
        a.cyl(.09, .1, .07, L['denim'], 0, 0, 0, 14)
        a.box(.1, .01, .12, L['denim'], -.06, -.03, 0)
    a.cable([(0, 1.66, -.1), (-.04, 1.4, -.12), (.02, 1.15, -.1)], .025, L['pink'], seg=5)  # a scarf


def whiteboard(a):
    """Rolling glass board, 2.4 × 1.2 m, covered in glowing diagrams. Its
    writing surface is one quad of RS_Whiteboard mapped to the full image,
    so a drawing can replace it later."""
    for s in (-1, 1):
        a.box(.05, 2.05, .05, M['trim'], s * 1.26, 1.03, 0)
        a.box(.07, .04, .55, M['dark'], s * 1.26, .05, 0)
        for z in (-.24, .24):
            a.cyl(.03, .03, .03, M['dark'], s * 1.26, .03, z, 8, rz=math.pi / 2)
    a.box(2.48, 1.28, .03, M['trim'], 0, 1.4, 0)
    a.plane(2.4, 1.2, L['board'], 0, 1.4, .016)
    a.box(2.4, .006, .006, M['neon2'], 0, 2.045, .016)          # a neon edge on top
    a.box(1.6, .03, .06, M['trim'], 0, .77, .04)                # pen tray
    for i, m in enumerate((M['led'], M['neon2'], M['ledG'])):
        a.cyl(.008, .008, .12, m, -.3 + i * .05, .8, .05, 6, rz=math.pi / 2)


def backpack(a):
    a.sphere(.17, L['denim'], 0, .24, 0, s=(1, 1.35, .55), seg=14, rings=10)
    a.sphere(.1, L['denim'], 0, .16, .08, s=(1, .9, .5), seg=12, rings=8)
    a.box(.2, .01, .004, M['orange'], 0, .2, .13)
    for s in (-1, 1):
        a.box(.04, .38, .02, M['dark'], s * .08, .25, -.1)
    with a.at(0, .47, 0, rx=math.pi / 2):
        a.torus(.05, .008, M['dark'], 12, 5, arc=math.pi)


def stool(a):
    a.cyl(.18, .18, .05, L['leather'], 0, .72, 0, 20)
    a.cyl(.03, .03, .68, M['trim'], 0, .36, 0, 8)
    with a.at(0, .28, 0, rx=math.pi / 2):
        a.torus(.15, .012, M['trim'], 24, 6)
    a.cyl(.2, .22, .02, M['dark'], 0, .01, 0, 20)


def holo_table(a):
    """The meeting room's holo-table, 3.0 × 2.0 m: a chunky console on a
    plinth, thick bevelled rim panels around a recessed holo bed (dark glass:
    the page draws its grid and the hologram above it), a control column
    with a glowing orb at the near edge, grab rails, vents. Every glow is
    RS_Neon_Secondary: the floor's secondary colour. Origin at the floor,
    the control column at +Z."""
    n2, bd, dk, tr = M['neon2'], M['body'], M['dark'], M['trim']
    a.box(3.4, .08, 2.4, dk, 0, .04, 0)                                     # plinth
    for z in (-1.2, 1.2):
        a.box(3.3, .01, .01, n2, 0, .085, z * .985)
    for x in (-1.7, 1.7):
        a.box(.01, .01, 2.3, n2, x * .985, .085, 0)
    for sx in (-1, 1):                                                      # angled legs
        for sz in (-1, 1):
            a.box(.34, .72, .34, bd, sx * 1.05, .44, sz * .58, ry=sx * sz * .4)
            for k in range(4):
                a.box(.004, .3, .02, dk, sx * 1.05 + sx * .172, .4 + k * .07 - .1, sz * .58, ry=sx * sz * .4)
    a.box(1.1, .6, .7, bd, 0, .38, 0)                                       # core
    for k in range(6):
        a.box(.9, .015, .004, dk, 0, .2 + k * .06, .352)                    # its vents
    a.box(2.5, .04, 1.5, L['holoBed'], 0, .86, 0)                           # holo bed
    for x in (-1.22, 1.22):                                                 # dashed bed edges
        for k in range(9):
            a.box(.008, .006, .1, n2, x, .885, -.64 + k * .16)
    for z in (-.72, .72):
        for k in range(15):
            a.box(.1, .006, .008, n2, -1.12 + k * .16, .885, z)
    for z in (-1, 1):                                                       # rim: long panels
        a.box(3.0, .16, .26, bd, 0, .9, z * .87)
        a.box(2.9, .05, .2, dk, 0, 1.0, z * .87)
        a.box(2.86, .008, .008, n2, 0, .97, z * .74)
    for x in (-1, 1):                                                       # rim: short panels
        a.box(.26, .16, 1.48, bd, x * 1.37, .9, 0)
        a.box(.2, .05, 1.4, dk, x * 1.37, 1.0, 0)
        a.box(.008, .008, 1.36, n2, x * 1.24, .97, 0)
    for sx in (-1, 1):                                                      # corner blocks
        for sz in (-1, 1):
            a.box(.36, .22, .36, bd, sx * 1.36, .93, sz * .86)
            a.box(.2, .006, .2, tr, sx * 1.36, 1.042, sz * .86)
    a.box(.46, .26, .34, bd, 0, 1.0, 1.06)                                  # control column
    a.box(.38, .02, .26, dk, 0, 1.14, 1.06)
    a.sphere(.07, n2, 0, 1.22, 1.04)                                        # the orb
    a.cyl(.012, .012, .12, tr, .14, 1.2, 1.1, 10)                           # a stick
    a.sphere(.025, dk, .14, 1.27, 1.1)
    for x in (-.9, .9):                                                     # grab rails
        a.cyl(.016, .016, .7, tr, x, 1.07, 1.02, 10, rz=math.pi / 2)


def pizza_boxes(a, seed=3):
    r = random.Random(seed)
    for i in range(3):
        a.box(.4, .045, .4, M['cardboard'], (r.random() - .5) * .04, .0225 + i * .046, (r.random() - .5) * .04, ry=(r.random() - .5) * .3)
    a.box(.12, .002, .08, M['red'], .05, .14, .05)  # the logo


lounge = {}
lounge['Furn_Sofa_L'] = sofa_l('Furn_Sofa_L')
add_colliders(lounge['Furn_Sofa_L'], [(2.9, .9, .95, 0, .45, 0), (.95, .6, 1.5, .95, .3, 1.2)])
for name, fn, col in [
    ('Furn_Armchair', armchair, 'aabb'),
    ('Deco_Plant_Snake', snake_plant, 'aabb'), ('Deco_Plant_Monstera', monstera, 'aabb'),
    ('Deco_Plant_Palm', palm, 'aabb'), ('Deco_Plant_Bush', bush, 'aabb'),
    ('Deco_Plant_Trailing', trailing_planter, None),
    ('Furn_SideTable', side_table, 'aabb'), ('Deco_FloorLamp', floor_lamp, [(.42, 1.9, .42, 0, .95, 0)]),
    ('Deco_Rug_Geo', rug_geo, None), ('Deco_CoatRack', coat_rack, 'aabb'), ('Deco_Whiteboard', whiteboard, 'aabb'),
    ('Deco_Backpack', backpack, None), ('Furn_Stool', stool, 'aabb'), ('Deco_BookStack', book_stack, None),
    ('Deco_PizzaBoxes', pizza_boxes, None), ('Furn_HoloTable', holo_table, [(3.0, 1.05, 2.0, 0, .525, 0), (3.4, .1, 2.4, 0, .05, 0)]),
]:
    a = Asset(name)
    fn(a)
    lounge[name] = a.build()
    add_colliders(lounge[name], col)

quad_uvs(lounge, prefixes=('RS_Whiteboard',))
export_all(lounge)
layout(lounge, y0=-48)
print('built', len(lounge), 'lounge assets ->', OUT)
for o in lounge.values():
    tris = sum(len(p.vertices) - 2 for c in [o, *o.children_recursive] if '_COL' not in c.name for p in c.data.polygons)
    print(f'{o.name}: {tris} tris')
