# The modern set (docs/office-kit.md, Sets): the kit as a bright, clean office,
# cyberpunk's light mode. build_all.py runs this after build_kit.py, in the
# same globals, when OFFICE_KIT_SET=modern. Three things:
#   - MATS: the kit's materials as white walls, grey and graphite furniture,
#     oak and aluminium; the accent materials keep their names (each floor
#     still has its colours) but are paint, not neon: the page colours them.
#   - textures: oak planks for the floor, plain plaster for the walls, a
#     framed print, a white whiteboard, a daytime skyline.
#   - @shape: the pieces that are cyberpunk by shape, rebuilt to the same box
#     (pieces.json, ±2 cm): BOX has each piece's, for the ones that fit to it.
import json

with open(os.path.join(os.path.dirname(__file__), '..', '..', 'internal', 'web', 'static', 'office', 'kit', 'pieces.json')) as fh:
    BOX = {n: (p['min'], p['max']) for n, p in json.load(fh)['pieces'].items()}


# ---------- textures ----------
def draw_oak(px, w, h):
    """Oak boards, 4 m per repeat: 0.25 m wide and 2 m long, each row's joints
    shifted, each board its own tone, grain along it."""
    rng = np.random.default_rng(5)
    rows, L = 16, w // 2
    yy, xx = np.mgrid[0:h, 0:w]
    row = yy // (h // rows)
    along = (xx + rng.integers(0, w, rows)[row]) % w
    tone = rng.random((rows, 2))[row, along // L]
    grain = smooth_noise(rng, 256, h)[:, :1] * (.6 + .4 * smooth_noise(rng, 4, w))  # streaks along the boards
    px[..., :3] = np.array((.70, .54, .36)) * (.9 + .12 * tone + .07 * grain)[..., None]
    px[(yy % (h // rows) < 1) | (along % L < 1), :3] *= .62


def draw_plaster(px, w, h):
    """Painted plaster: off-white, barely mottled."""
    px[..., :3] = np.array((.88, .88, .86)) * (.985 + .03 * smooth_noise(np.random.default_rng(11), 12, w))[..., None]


def draw_print(px, w, h):
    """A framed print: a terracotta sun over a sage arch on cream paper."""
    yy, xx = np.mgrid[0:h, 0:w]
    px[..., :3] = (.96, .95, .92)
    px[np.hypot(xx - w * .62, yy - h * .3) < w * .26, :3] = (.87, .55, .38)
    arch = (np.abs(xx - w * .4) < w * .3) & (yy < h * .84) & ((yy > h * .6) | (np.hypot(xx - w * .4, yy - h * .6) < w * .3))
    px[arch, :3] = (.52, .63, .58)
    px[(np.abs(yy - h * .9) < 2) & (np.abs(xx - w * .5) < w * .4), :3] = (.15, .2, .3)


def draw_day(px, w, h):
    """The flat backdrop by day: a pale sky, two rows of towers in haze."""
    r = random.Random(3)
    px[..., :3] = np.array((.55, .74, .93)) + (np.array((.9, .94, .97)) - (.55, .74, .93)) * np.linspace(0, 1, h)[:, None, None]
    for shade, lo, hi in (((.74, .8, .86), 120, 380), ((.56, .62, .69), 200, 490)):
        x = -20
        while x < w:
            bw, bh = int(30 + r.random() * 90), int(lo + r.random() * (hi - lo))
            px[h - bh:h, max(x, 0):max(min(x + bw, w), 0), :3] = shade
            x += bw + int(r.random() * 30)


DRAWS.update(
    RS_Poster=draw_print,
    RS_Whiteboard=lambda px, w, h: draw_whiteboard(px, w, h, .97, (.9, .91, .93), (.1, .3, .75), (.85, .15, .2), (.12, .12, .15)),
)
make_image('RS_CityNight', 2048, 1024, draw_day)  # the kit's image, repainted: its name stays

# ---------- materials: each replaces the kit's of that name, whole ----------
MATS.update(
    RS_Neon_Accent=dict(base='#2f7f86', rough=.5),      # paint: the page gives it the floor's colour
    RS_Neon_Secondary=dict(base='#b0603c', rough=.5),
    RS_LED_Accent=dict(emit=ACCENT, strength=3),
    RS_LightPanel=dict(base='#ffffff', emit='#fff6e8', strength=6),
    RS_Glow_Warm=dict(emit='#ffd9a8', strength=3),
    RS_Body=dict(base='#cfd2d6', rough=.45),            # light grey laminate, off the walls' white
    RS_BodyDark=dict(base='#4b4f57', rough=.55),        # graphite: frames, bases, housings
    RS_PaintViolet=dict(base='#2f6f7f', rough=.5),      # a deep teal
    RS_Trim=dict(base='#b9bdc3', metal=.8, rough=.35),  # brushed aluminium
    RS_Wall=dict(base='#ffffff', rough=.9, image=make_image('RS_Trim_Wall', 1024, 1024, draw_plaster)),
    RS_Floor=dict(base='#ffffff', rough=.55, image=make_image('RS_Trim_Floor', 1024, 1024, draw_oak)),
    RS_Rubber=dict(base='#3a3d42', rough=.6),
    RS_Fabric=dict(base='#3f5873', rough=.95),
    RS_Cushion=dict(base='#8c95a3', rough=.9),
    RS_Pot=dict(base='#eceae4', rough=.4),              # white ceramic
    RS_Pot_Concrete=dict(base='#b8b8b4', rough=.9),
    RS_Glass=dict(base='#cfe3ec', rough=.05, alpha=.14),
    RS_BeigePlastic=dict(base='#b9bcc2', rough=.5),
    RS_Cardboard=dict(base='#ece9e2', rough=.9),        # white archive boxes: kraft was the floor's colour
    RS_Cork=dict(base='#b98f5e', rough=.95),
    RS_Red=dict(base='#c8372d', rough=.45),
    RS_Tape=dict(base='#3f6fb0', rough=.7),
    RS_Poster=dict(base='#ffffff', rough=.8, image=make_image('RS_Poster', 256, 384, draw_print)),
    RS_Leather_Dark=dict(base='#9c6238', rough=.5),     # cognac
    RS_Oxblood=dict(base='#3f6b73', rough=.85),         # a teal throw
    RS_Wood_Panel=dict(base='#b08a60', rough=.55),
    RS_Rug_Base=dict(base='#d9d4c8', rough=.97),
    RS_Rug_Line=dict(base='#7d9aa5', rough=.95),
    RS_Rug_Line2=dict(base='#c49a2c', rough=.95),
    RS_Glass_Smoked=dict(base='#9fb0bb', rough=.08, alpha=.4),
    RS_Steel_Dark=dict(base='#8f949b', metal=.85, rough=.35),
    RS_ClockFace=dict(base='#f4f4f2', rough=.5),
    RS_ClockHand=dict(base='#22252a', rough=.4),
    RS_Android_Shell_White=dict(base='#eef0f2', rough=.55),   # satin, untextured; the agent's partner
    RS_Android_Shell_Ivory=dict(base='#d96a4f', rough=.55),   # the wanderers: coral,
    RS_Android_Shell_Black=dict(base='#2c4a6e', rough=.55),   #   navy
    RS_Android_Shell_Silver=dict(base='#e0aa3e', rough=.55),  #   and ochre
    RS_Android_Core=dict(base='#33373d', metal=.5, rough=.45),
    RS_Android_Core_Blue=dict(base='#33373d', metal=.5, rough=.45),
)
for i, c in enumerate(['#3f6fb0', '#d9d7d2', '#c8372d', '#e8c95a', '#52a08f', '#8a8f9c', '#f1f1ee']):
    MATS[f'RS_Book{i}'] = dict(base=c, rough=.8)
for n in MATS:
    mat(n)  # the kit made its own before this file ran

T = dict(  # the set's own
    oak=mat('RS_Oak', '#b08a60', 0, .55),
    bezel=mat('RS_Bezel', '#1d1f23', .2, .4),            # screens' surrounds, dark glass
    frame=mat('RS_WindowFrame', '#2a2c30', .6, .4),
    blue=mat('RS_Cable_Blue', '#3f6fb0', 0, .6),
    rack=mat('RS_Rack', '#8a9099', .7, .4),              # silver-grey cabinets
    rackFace=mat('RS_RackFace', '#2b2e33', .3, .5),      # their module faces: the lights read on dark
)


# ---------- shapes: the kit's builders of these names, replaced ----------
@shape
def desk(a, width=2.0, depth=0.8, height=0.75):
    """An oak top on white: three drawers on the right, a panel leg on the
    left, a modesty panel between; the accent in one line under the front edge."""
    top, pw = .04, .44
    ph = height - top
    a.box(width, top, depth, T['oak'], 0, height - top / 2, 0)
    a.box(width - .1, .006, .006, M['neon'], 0, height - top - .003, depth / 2 - .02)
    x, front = width / 2 - pw / 2 - .02, depth / 2 - .05
    a.box(pw, ph, depth - .08, M['body'], x, ph / 2, -.01)
    dh = (ph - .06) / 3
    for i in range(3):
        y = .05 + dh * i + dh / 2
        a.box(pw - .03, dh - .012, .012, M['body'], x, y, front + .006)
        a.box(.12, .012, .012, M['trim'], x, y + dh / 2 - .04, front + .018)
    left = -width / 2 + .035
    a.box(.03, ph, depth - .08, M['body'], left, ph / 2, -.01)
    a.box(x - pw / 2 - left, .3, .018, M['body'], (x - pw / 2 + left) / 2, height - top - .15, -depth / 2 + .06)


@shape
def monitor(a, width=0.62, height=0.36, stand=True, screen=None):
    """A flat panel in a dark surround on an aluminium foot; no light bar."""
    y = .2 + height / 2 if stand else 0
    a.box(width, height, .025, T['bezel'], 0, y, 0)
    a.plane(width - .02, height - .02, screen or M['screen'], 0, y, .0135)
    if stand:
        a.box(.22, .012, .16, M['trim'], 0, .006, -.03)
        a.box(.05, .22, .012, M['trim'], 0, .11, -.045)


@shape
def coffee_table(a, width=1.2, depth=0.7, height=0.42):
    """An oak top on a white block and plinth, the accent in a line under the top's edge."""
    a.box(width + .04, .05, depth + .04, T['oak'], 0, height - .025, 0)
    a.box(width + .048, .006, depth + .048, M['neon'], 0, height - .053, 0)
    a.box(.3, height - .09, .3, M['body'], 0, (height - .09) / 2 + .04, 0)
    a.box(width * .75, .04, depth * .75, M['body'], 0, .02, 0)
    a.box(.21, .004, .29, M['paper'], -.2, height + .002, .05, ry=-.3)
    with a.at(.3, height, -.1): mug(a)


@shape
def pc_tower(a):
    a.box(.22, .48, .45, M['body'], 0, .24, 0)
    a.box(.004, .4, .36, T['bezel'], .112, .25, 0)   # a dark side window
    a.box(.006, .3, .006, M['neon'], .06, .28, .226)


_kit_rack = server_rack


@shape
def server_rack(a, *args, **kw):
    """The kit's rack in silver-grey with dark faces, to stand out from the
    white walls and let its lights read: the kit's builder, its two paints swapped."""
    kit_mats = {k: M[k] for k in ('body', 'dark')}
    M.update(body=T['rack'], dark=T['rackFace'])
    try:
        _kit_rack(a, *args, **kw)
    finally:
        M.update(kit_mats)


@shape
def rack_dressing(a, r, width, height, depth):
    """A server room's cable management, no loose cable: a finger duct up
    each side of the cabinet's front, a wire basket over it as wide as the
    piece's box (so a row's baskets meet) with a combed, tied bundle lying in
    it, and a closed riser from the cabinet's top into the ceiling."""
    (x0, _, _), (x1, y1, _) = BOX[a.name]
    w, cx, y = x1 - x0, (x0 + x1) / 2, height + .32
    for s_ in (-1, 1):
        x = s_ * (width / 2 + .045)
        a.box(.07, height - .2, .1, M['dark'], x, height / 2, depth / 2 - .08)
        for k in range(18):  # its fingers
            a.box(.072, .006, .102, T['bezel'], x, .2 + k * .095, depth / 2 - .08)
        a.box(.02, .32, .02, M['trim'], s_ * .25, height + .16, -.3)   # the basket's stand-offs
    for z in (-.44, -.16):  # the basket: rails, then rungs over the cabinet
        for dy in (0, .07):
            a.box(w, .008, .008, M['trim'], cx, y + dy, z)
    for x in np.arange(-.3, .31, .15):
        a.box(.008, .008, .28, M['trim'], x, y, -.3)
    for k, m in enumerate((M['rubber'], T['blue'], M['rubber'], T['blue'])):
        a.cyl(.014, .014, w, m, cx, y + .02, -.36 + k * .03, 8, rz=math.pi / 2)
    for x in (x0 + .15, cx, x1 - .15):  # ties
        a.box(.012, .036, .13, M['body'], x, y + .022, -.315)
    a.box(.26, y1 - height, .1, M['body'], 0, (height + y1) / 2, -.02)


@shape
def neon_sign(a, size=0.8):
    """A sign board, not a neon: white in an oak frame, the mark (a ring and
    three bars) cut from acrylic in the floor's secondary colour."""
    s, m = size, M['neon2']
    a.box(1.5 * s, .9 * s, .03, M['body'], 0, 0, .015)
    with a.at(0, 0, .03):
        a.frame(1.5 * s - .02, .9 * s - .02, .02, T['oak'])
    with a.at(-.32 * s, 0, .05):
        a.torus(.2 * s, .01, m, 32, 6)
    for k in range(3):
        a.box((.5 - k * .12) * s, .06 * s, .02, m, (.3 - k * .06) * s, (.18 - k * .18) * s, .05)


@shape
def door(a, width=1.1, height=2.3):
    """An oak door with a narrow glass light and a lever, in a dark frame; a
    card reader beside it where the kit has its keypad."""
    a.box(width, height, .06, T['oak'], 0, height / 2, 0)
    a.box(.14, 1.2, .07, M['glass'], -width / 2 + .25, 1.35, 0)
    a.box(.03, .03, .05, M['trim'], width / 2 - .1, 1.05, .055)
    a.box(.14, .02, .02, M['trim'], width / 2 - .16, 1.05, .08)
    a.box(.08, .12, .02, M['body'], width / 2 + .15, 1.2, .08)
    a.box(.04, .01, .004, M['led'], width / 2 + .15, 1.23, .09)
    with a.at(0, height / 2, .06):
        a.frame(width + .06, height + .03, .012, T['frame'])


@shape
def window_insert(a, w, h, corner=.2, depth=.15):
    """A square window: clear glass in a slim dark frame, a mullion every
    1.6 m, a white reveal on the room's side."""
    with a.at(0, 0, -depth / 2):
        a.plane(w, h, M['glass'])
        a.frame(w - .05, h - .05, .05, T['frame'])
        panes = max(1, round(w / 1.6))
        for i in range(1, panes):
            a.box(.04, h - .06, .05, T['frame'], -w / 2 + i * w / panes, 0, 0)
    with a.at(0, 0, .0155):
        a.frame(w + .03, h + .03, .031, M['body'])


_kit_wall = make_wall


def make_wall(name, **kw):
    """The kit's wall with square window openings."""
    kw['windows'] = [(x, y, w, h, .01) for x, y, w, h, _ in kw.get('windows', ())]
    return _kit_wall(name, **kw)


@shape
def skirting(a, w, x):
    a.box(w, .09, .018, M['dark'], x, .045, .009)


@shape
def crt(a, size=0.38, screen=None, shell=None):
    """Where the kit has a CRT: a small flat display on an arm over a mini PC."""
    s = size
    a.box(s * .8, .05, s * .7, M['body'], 0, .025, -s * .4)
    a.box(.03, s * .3, .02, M['trim'], 0, .05 + s * .15, -s * .1)
    with a.at(0, s * .47, s * .2 - .012):
        a.box(s * 1.1, s * .86, .024, T['bezel'], 0, 0, 0)
        a.plane(s * 1.1 - .02, s * .86 - .02, screen or M['screenTerm'], 0, 0, .018)


@shape
def monitor_wall(a, seed=1):
    """A pole rig of six matched screens, two rows of three, a camera on top."""
    deck = screen_deck(random.Random(seed))
    a.box(.7, .05, .45, M['trim'], 0, .025, 0)
    a.cyl(.035, .035, 2.15, M['body'], 0, 1.1, 0, 16)
    for y in (1.25, 1.73):
        a.box(2.0, .04, .04, M['trim'], 0, y, -.06)
        for x in (-.66, 0, .66):
            with a.at(x, y, 0, ry=-x * .25):
                monitor(a, .62, .36, stand=False, screen=next(deck))
    a.box(.3, .05, .05, T['bezel'], 0, 2.22, .03)


@shape
def monitor_bank(a, cols=4, rows=3, seed=3):
    """A video wall of matched screens on a white panel, a credenza under it
    with an oak top. Origin at the floor, back on the wall."""
    deck = screen_deck(random.Random(seed))
    cw, ch = .62, .38
    W, H = cols * (cw + .03) + .1, rows * (ch + .03) + .1
    y0 = 1.0
    a.box(W + .01, H + .01, .06, M['body'], 0, y0 + H / 2, .03)
    for i in range(cols * rows):
        x = -W / 2 + .05 + (i % cols) * (cw + .03) + cw / 2
        y = y0 + .05 + (i // cols) * (ch + .03) + ch / 2
        with a.at(x, y, .08):
            monitor(a, cw, ch, stand=False, screen=next(deck))
    a.box(.6, .4, .25, M['dark'], 0, y0 + H / 2, -.125)   # its mount, let into the wall as far as the kit's CRTs
    a.box(W - .4, .6, .46, M['body'], 0, .3, .265)
    a.box(W - .36, .03, .49, T['oak'], 0, .615, .26)
    a.box(W - .5, .006, .006, M['neon'], 0, .05, .497)


@shape
def network_shelf(a, seed=1):
    """The wall shelf of switches, its patch cables in a closed trunking down
    to the floor, where a mat marks its place. Origin at the floor, back on the wall."""
    (x0, _, _), (x1, _, z1) = BOX[a.name]
    r = random.Random(seed)
    a.box(.9, .025, .3, M['trim'], 0, 1.6, .15)
    for x in (-.4, .4):
        a.box(.025, .2, .03, M['trim'], x, 1.5, .02)
    for y in (1.635, 1.685):
        a.box(.44, .045, .25, M['dark'], -.15, y, .14)
        for i in range(12):
            a.box(.018, .014, .003, M['trim'], -.34 + i * .025, y, .266)
            a.box(.005, .004, .003, M['led'] if r.random() < .7 else M['ledG'], -.34 + i * .025, y + .013, .266)
    a.box(.25, .05, .18, M['body'], .25, 1.638, .14)
    for dx in (-.08, .08):
        a.cyl(.006, .006, .18, M['dark'], .25 + dx, 1.75, .06, 6, rz=dx * 2)
    a.box(.1, 1.59, .05, M['body'], -.15, .795, .025)
    a.box(x1 - x0, .008, z1, M['rubber'], (x0 + x1) / 2, .01, z1 / 2)


@shape
def cable_run(a, length=2.5, seed=1):
    """Where the kit lays cables across the floor: a rubber cord cover over them."""
    (x0, _, z0), (x1, y1, z1) = BOX[a.name]
    a.box(x1 - x0, y1 * .5, z1 - z0, M['rubber'], (x0 + x1) / 2, y1 * .25, (z0 + z1) / 2)
    a.box(x1 - x0, y1 * .5, (z1 - z0) * .5, M['rubber'], (x0 + x1) / 2, y1 * .75, (z0 + z1) / 2)
    a.box(x1 - x0, .002, .02, M['note'], (x0 + x1) / 2, y1, (z0 + z1) / 2)


@shape
def sofa_l(name):
    """The L-shaped sectional, plainer to read: a plinth, deep seat cushions
    with a gap between them, one solid back with a grey cushion per seat
    leaning on it, an arm at the open end, two throw pillows. The kit's
    footprint; the chaise runs forward on the right."""
    a = Asset(name)
    lth, ox = L['leatherDark'], L['oxblood']
    a.box(2.9, .12, .95, M['dark'], 0, .08, 0)
    a.box(.95, .12, 1.5, M['dark'], .95, .08, 1.2)
    for x in (-.93, -.02):
        a.box(.88, .26, .64, lth, x, .27, .11)
    a.box(.88, .26, 2.12, lth, .95, .27, .85)
    a.box(2.9, .6, .17, lth, 0, .44, -.39)
    for x in (-.93, -.02, .95):
        a.box(.84, .32, .14, M['cushion'], x, .57, -.25, rx=-.18)
    a.box(.16, .5, .95, lth, -1.37, .39, 0)
    a.box(.4, .32, .12, ox, -1.02, .54, -.1, rx=-.3, ry=.25)
    a.box(.4, .32, .12, ox, .6, .54, -.1, rx=-.3, ry=-.15)
    return a.build()


@shape
def crate(a):
    """Where the kit has a crate: a storage bench, oak under a cushion, its top at the crate's."""
    a.box(.92, .3, .58, T['oak'], 0, .2, 0)
    a.box(.92, .11, .58, M['fabric'], 0, .405, 0)
    for x in (-.4, .4):
        for z in (-.23, .23):
            a.cyl(.02, .015, .05, M['trim'], x, .025, z, 8)


@shape
def poster(a, w=0.56, h=0.81, tilt=0.03):
    """A print in an oak frame, hung straight."""
    (x0, y0, _), (x1, y1, _) = BOX['Deco_Poster']
    a.plane(x1 - x0 - .03, y1 - y0 - .03, M['poster'], 0, 0, .008)
    with a.at(0, 0, .01):
        a.frame(x1 - x0 - .02, y1 - y0 - .02, .02, T['oak'])


@shape
def wall_pipes(a, length=4.0, top=4.0):
    """Where the kit runs pipes along a wall: a light cove, white with an oak
    fascia, that washes the wall above it."""
    y = top - .62
    a.box(length + .04, .42, .03, M['body'], 0, y, .015)
    a.box(length + .04, .03, .139, M['body'], 0, y - .195, .0695)
    a.box(length + .04, .16, .02, T['oak'], 0, y - .115, .129)
    a.box(length, .01, .05, M['panel'], 0, y - .17, .07)


@shape
def ceiling_duct(a, length=4.0):
    """A white duct with a slot diffuser, and a linear pendant light hung beside it. Origin at the ceiling."""
    a.box(length, .32, .5, M['body'], 0, -.2, 0)
    for x in (-length / 2, 0, length / 2):
        a.box(.03, .36, .54, M['dark'], x, -.2, 0)
    a.box(length - .6, .004, .06, T['bezel'], 0, -.362, 0)
    a.box(length, .035, .035, M['dark'], 0, -.588, .29)
    a.box(length - .04, .006, .025, M['panel'], 0, -.607, .29)
    for x in (-length / 2 + .5, length / 2 - .5):
        a.cyl(.002, .002, .55, M['trim'], x, -.295, .29, 4)


@shape
def cable_tray(a, length=4.0, seed=1):
    """The ceiling's tray as a server room keeps it: a wire basket on drop
    rods, two combed bundles tied every half metre, one closed drop at its end."""
    y = -.4
    for z in (-.15, .15):
        for dy in (0, .06):
            a.box(length, .008, .008, M['trim'], 0, y + dy, z)
    for x in np.arange(-length / 2 + .1, length / 2, .2):
        a.box(.008, .008, .3, M['trim'], x, y, 0)
    for x in (-length / 2 + .3, 0, length / 2 - .3):
        for s in (-1, 1):
            a.cyl(.006, .006, -y, M['trim'], x, y / 2, s * .17, 6)
        a.box(.04, .03, .4, M['trim'], x, y - .02, 0)
    for z, m in ((-.09, M['rubber']), (-.06, M['rubber']), (-.03, M['rubber']), (.05, T['blue']), (.08, T['blue'])):
        a.cyl(.013, .013, length, m, 0, y + .018, z, 8, rz=math.pi / 2)
    for x in np.arange(-length / 2 + .25, length / 2, .5):
        a.box(.012, .034, .1, M['body'], x, y + .02, -.06)
        a.box(.012, .034, .07, M['body'], x, y + .02, .065)
    a.box(.07, .45, .05, M['body'], length / 2 - .2, -.633, .185)


@shape
def pipe_run(a, length=4.0):
    """Where the kit runs pipes under the ceiling: two lighting tracks with spot heads."""
    for z, n in ((-.2, 4), (.22, 3)):
        a.box(length, .05, .04, T['bezel'], 0, -.025, z)
        for x in np.linspace(-length / 2 + .5, length / 2 - .5, n):
            a.cyl(.012, .012, .14, T['bezel'], x, -.12, z, 8)
            a.cyl(.045, .045, .16, M['body'], x, -.27, z, 14)
            a.cyl(.036, .036, .004, M['panel'], x, -.351, z, 14)


@shape
def hanging_cables(a, seed=1):
    """Where the kit droops cables: globe pendants at mixed heights, spread over the same box."""
    (x0, y0, z0), (x1, _, z1) = BOX[a.name]
    R, zm = .09, (z0 + z1) / 2
    for x, z, drop in ((x0 + R, zm - .1, .5), (x1 - R, zm + .1, .58), (-.4, z0 + R, .4), (.35, z1 - R, .47), (0, zm, -y0 - R)):
        a.cyl(.03, .03, .02, M['trim'], x, -.01, z, 10)
        a.cyl(.003, .003, drop - R, M['rubber'], x, -(drop - R) / 2, z, 4)
        a.sphere(R, M['warm'], x, -drop, z)


@shape
def holo_table(a):
    """The meeting table, standing height: an oak top round the kit's dark
    glass bed (RS_HoloBed, where the kit has it: the page draws on and above
    it) with the accent in a line round it, on two white slab legs, on a rug
    the size of the kit's plinth; a room console on an arm at the near edge."""
    a.box(3.4, .02, 2.4, L['rug'], 0, .01, 0)
    for x in (-1.0, 1.0):
        a.box(.12, .82, 1.4, M['body'], x, .43, 0)
    a.box(1.9, .12, .06, M['body'], 0, .7, 0)
    a.box(2.5, .04, 1.5, L['holoBed'], 0, .86, 0)
    for s_ in (-1, 1):
        a.box(3.0, .06, .25, T['oak'], 0, .87, s_ * .875)
        a.box(.25, .06, 1.5, T['oak'], s_ * 1.375, .87, 0)
        a.box(2.5, .004, .006, M['neon'], 0, .901, s_ * .752)
        a.box(.006, .004, 1.5, M['neon'], s_ * 1.252, .901, 0)
    a.box(.06, .02, .2, M['trim'], 0, .91, 1.08)
    a.cyl(.012, .012, .3, M['trim'], 0, 1.06, 1.17, 8)
    with a.at(0, 1.2, 1.175, rx=-.5):
        a.box(.3, .2, .015, T['bezel'], 0, 0, 0)
        a.box(.2, .01, .002, M['led'], 0, -.07, .009)


@shape
def vending(a, seed=1):
    """A glass-front vending machine, 1.0 × 1.9 × 0.8 m, in graphite: five
    shelves behind the glass (snack packs for seed 1, bottles otherwise), a
    touch screen beside them (RS_VendScreen: the page draws the menu), a card
    reader under it, a pick-up flap with a rail, the mount for the page's sign."""
    w, h, d = 1.0, 1.9, .8
    zf = d / 2
    r = random.Random(seed)
    a.box(w, h, d, M['dark'], 0, h / 2, 0)
    a.box(w + .02, .08, d + .02, T['bezel'], 0, .04, 0)
    wx, ww = -.17, .58  # the window
    a.box(ww, 1.2, .01, M['body'], wx, 1.2, zf + .005)
    for row in range(5):
        y = .68 + row * .235
        a.box(ww, .012, .04, M['trim'], wx, y, zf + .03)
        for col in range(6):
            x = wx - ww / 2 + .06 + col * .092
            if seed == 1:
                a.box(.07, .15, .03, r.choice(BOOKS), x, y + .081, zf + .03)
            else:
                a.cyl(.028, .028, .16, r.choice(BOOKS), x, y + .086, zf + .03, 10)
    a.box(ww + .02, 1.24, .006, M['glass'], wx, 1.2, zf + .055)
    a.box(.28, .38, .02, T['bezel'], .33, 1.45, zf + .005)
    a.plane(.25, .31, M['vend'], .33, 1.45, zf + .017)
    a.box(.1, .14, .03, T['bezel'], .33, 1.1, zf + .015)
    a.box(.06, .02, .004, M['led'], .33, 1.14, zf + .032)
    a.box(ww, .22, .02, T['bezel'], wx, .36, zf)
    a.box(.5, .03, .08, M['trim'], wx, .5, zf + .04)
    a.box(.7, .05, .12, M['dark'], 0, h + .025, zf - .1)


@shape
def arcade_bartop(a):
    """The bartop arcade, clean: white between oak sides, the kit's angled
    screen (RS_ArcadeScreen) and controls, a lit marquee on top."""
    a.box(.62, .16, .55, M['body'], 0, .08, 0)
    a.box(.62, .05, .3, T['bezel'], 0, .18, .12, rx=-.12)
    a.cyl(.012, .012, .08, M['trim'], -.15, .25, .14, 8)
    a.sphere(.035, M['neon2'], -.15, .3, .14)
    for bx, bz in [(.04, .1), (.11, .1), (.18, .1), (.04, .18), (.11, .18), (.18, .18)]:
        a.cyl(.02, .02, .02, M['neon2'], bx, .21, bz, 12)
    with a.at(0, .4, -.05, rx=-.3):
        a.box(.56, .42, .14, M['body'], 0, 0, 0)
        a.box(.48, .36, .02, T['bezel'], 0, 0, .07)
        a.plane(.4, .32, M['arcadeScreen'], 0, 0, .082)
    for s_ in (-1, 1):
        a.box(.026, .5, .5, T['oak'], s_ * .316, .3, 0)
    a.box(.5, .1, .05, M['body'], 0, .66, -.18)
    a.box(.44, .06, .004, M['led'], 0, .66, -.153)
    a.box(.3, .004, .004, M['dark'], 0, .1, .277)


# ---------- androids: the kit's, in colour ----------
@shape
def droid_trim(shell):
    """What an android is dressed in here: white, or on the white one the floor's colour."""
    white = bpy.data.materials['RS_Android_Shell_White']
    return M['neon'] if shell is white else white
