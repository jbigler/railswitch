# Decor set: worn, lived-in cyberpunk office clutter with lots of screens.
# exec build_kit.py first, in the same globals. Same conventions: three.js
# coordinates (Y up, front +Z), meters, origin on the floor or mount surface.
use_collection('CyberOfficeDecor')


def draw_ui(px, w, h):
    r = random.Random(11)
    yy, xx = np.mgrid[0:h, 0:w]
    px[0:6, :, :3] = 0.9
    cx, cy, R = w * 0.24, h * 0.56, h * 0.33
    d = np.hypot(xx - cx, yy - cy)
    for rr in (R, R * 0.66, R * 0.33):
        px[np.abs(d - rr) < 1.3, :3] = 0.8
    px[(np.abs(xx - cx) < 0.8) & (d < R), :3] = 0.5
    px[(np.abs(yy - cy) < 0.8) & (d < R), :3] = 0.5
    ang = np.arctan2(yy - cy, xx - cx)
    wedge = (d < R) & (ang > 0.2) & (ang < 0.8)
    px[wedge, :3] = np.maximum(px[wedge, :3], 0.3)
    for _ in range(7):
        bx, by = int(cx + (r.random() - .5) * R * 1.3), int(cy + (r.random() - .5) * R * 1.3)
        px[by:by + 4, bx:bx + 4, :3] = 1.0
    gx0, gy0, cw, ch = int(w * 0.52), 20, int(w * 0.07), int(h * 0.13)
    for i in range(6):
        for j in range(4):
            x0, y0 = gx0 + i * (cw + 4), gy0 + j * (ch + 4)
            px[y0:y0 + ch, x0:x0 + cw, :3] = 0.1 + r.random() * 0.5
            px[y0:y0 + 2, x0:x0 + cw, :3] = 0.9
    for i in range(24):
        bh = int(6 + r.random() * h * 0.2)
        x0 = gx0 + i * 9
        px[h - 12 - bh:h - 12, x0:x0 + 6, :3] = 0.5 + r.random() * 0.5


def draw_term(px, w, h):
    r = random.Random(5)
    for y in range(8, h - 6, 9):
        x = 6 + (14 if r.random() < 0.3 else 0)
        while x < w - 20 and r.random() > 0.08:
            L = int(4 + r.random() * 36)
            px[y:y + 5, x:min(x + L, w - 4), :3] = 0.35 + r.random() * 0.6
            x += L + 5
    px[h - 16:h - 9, 6:14, :3] = 1.0  # cursor


def draw_poster(px, w, h):
    yy, xx = np.mgrid[0:h, 0:w]
    g = yy / h
    px[..., 0], px[..., 1], px[..., 2] = 0.22 + 0.1 * g, 0.03, 0.06 + 0.1 * g
    d = np.hypot(xx - w * 0.5, yy - h * 0.36)
    px[d < w * 0.3, :3] = (0.95, 0.42, 0.1)
    tri = (yy > h * 0.2) & (yy < h * 0.58) & (np.abs(xx - w * 0.5) < (yy - h * 0.2) * 0.75)
    px[tri, :3] = (0.05, 0.02, 0.04)
    r = random.Random(2)
    for i, y in enumerate(range(int(h * 0.66), int(h * 0.92), 14)):
        L = int(w * (0.8 if i == 0 else 0.3 + r.random() * 0.5))
        px[y:y + (10 if i == 0 else 5), int(w * 0.1):int(w * 0.1) + L, :3] = 0.92
    px[:8, :, :3] = 0.95; px[-8:, :, :3] = 0.95


M.update(
    screenUI=mat('RS_Screen_UI', '#000000', 0, .25, emit='#22e6ff', strength=2, image=make_image('RS_ScreenUI', 512, 320, draw_ui), emit_image=True),
    screenTerm=mat('RS_Screen_Term', '#000000', 0, .25, emit='#22e6ff', strength=2, image=make_image('RS_ScreenTerm', 512, 384, draw_term), emit_image=True),
    screenOff=mat('RS_ScreenOff', '#050608', .2, .08),
    poster=mat('RS_Poster', '#000000', 0, .8, emit='#ffffff', strength=0.35, image=make_image('RS_Poster', 256, 384, draw_poster), emit_image=True),
    beige=mat('RS_BeigePlastic', '#a9a18c', 0, .6),
    cardboard=mat('RS_Cardboard', '#7d5f3e', 0, .92),
    orange=mat('RS_Orange', '#b84f1a', .1, .5),
    red=mat('RS_Red', '#7e1b1e', .3, .45),
    note=mat('RS_Note', '#d9c45a', 0, .9),
    tape=mat('RS_Tape', '#b9a24a', 0, .7),
    cork=mat('RS_Cork', '#6e5236', 0, .95),
    warm=mat('RS_Glow_Warm', emit='#ff7a2a', strength=5),
)
# the poster's base color also comes from its image so it reads lit or unlit
SCREENS = [M['screen'], M['screenUI'], M['screenTerm']]


def note(a, x, y, z, rz=0.0, s=0.07):
    with a.at(x, y, z, rz=rz):
        a.plane(s, s, M['note'])


# ---------- screens ----------
def crt(a, size=0.38, screen=None, shell=None):
    s = size
    shell = shell or M['beige']
    a.box(s * 1.1, s * .9, s * .5, shell, 0, s * .45, -s * .05)
    a.box(s * .8, s * .66, s * .5, shell, 0, s * .46, -s * .5)
    a.box(s * .86, s * .67, .006, M['dark'], 0, s * .47, s * .2 + .002)
    a.plane(s * .8, s * .6, screen or M['screenTerm'], 0, s * .47, s * .2 + .006)
    for i in range(6):
        a.box(s * .6, .004, .012, M['dark'], 0, s * .79 + .001, -s * .3 - i * .03)
    a.box(.014, .008, .004, M['warm'], s * .42, s * .07, s * .2 + .003)
    for i in range(3):
        a.box(.02, .012, .006, M['dark'], s * .2 + i * .03, s * .07, s * .2 + .003)


def laptop(a, screen=None, open_angle=0.25):
    a.box(.34, .018, .24, M['body'], 0, .009, 0)
    a.box(.3, .002, .1, M['dark'], 0, .019, .01)
    a.box(.1, .002, .06, M['trim'], 0, .019, .085)
    with a.at(0, .018, -.115, rx=-open_angle):
        a.box(.34, .23, .01, M['body'], 0, .115, 0)
        a.plane(.31, .2, screen or M['screenUI'], 0, .118, .0056)
        with a.at(.08, .14, -.0056, ry=math.pi):
            a.plane(.05, .05, M['orange'])  # sticker on the lid


def monitor_wall(a, seed=1):
    """Pole rig with six mismatched screens, one dead, sticky notes and dangling cables."""
    r = random.Random(seed)
    a.box(.7, .05, .45, M['trim'], 0, .025, 0)
    a.cyl(.035, .035, 2.15, M['body'], 0, 1.1, 0, 16)
    sizes = [(.62, .36), (.7, .4), (.55, .33), (.62, .36), (.48, .3), (.62, .36)]
    slots = [(-.68, 1.25), (0, 1.27), (.66, 1.24), (-.68, 1.72), (0, 1.74), (.66, 1.71)]
    for y in (1.25, 1.73):
        a.box(2.0, .04, .04, M['trim'], 0, y, -.06)
    dead = r.randrange(6)
    for i, ((x, y), (w, h)) in enumerate(zip(slots, sizes)):
        ry = -x * .25
        with a.at(x, y, 0, ry=ry, rz=(r.random() - .5) * .04):
            monitor(a, w, h, stand=False, screen=M['screenOff'] if i == dead else SCREENS[r.randrange(3)])
            a.box(.12, .1, .04, M['dark'], 0, 0, -.03)  # VESA mount
            if r.random() < .5:
                note(a, w / 2 - .05, -h / 2 + .06, .0137, (r.random() - .5) * .3)
            if i == dead:
                a.box(w * .9, .03, .002, M['tape'], 0, .05, .014, rz=.3)  # taped-over crack
        a.cable([(x, y - .2, -.05), (x * .7, y - .35, -.12), (.05, y - .45, -.1), (.06, .6, -.05), (.25, .01, .2)], .006, M['dark'])
    a.box(.04, .2, .3, M['trim'], 0, 2.1, 0)  # top cap / camera rail
    a.box(.08, .05, .05, M['dark'], 0, 2.22, .05)
    a.box(.01, .006, .002, M['warm'], .02, 2.22, .076)


def monitor_bank(a, cols=4, rows=3, seed=3):
    """Wall-mounted bank of screens with two CRTs inset. Origin at the floor, back on the wall."""
    r = random.Random(seed)
    cw, ch = .62, .38
    W, H = cols * (cw + .03) + .1, rows * (ch + .03) + .1
    y0 = 1.0
    a.box(W, H, .06, M['dark'], 0, y0 + H / 2, .03)
    with a.at(0, y0 + H / 2, .062):
        a.frame(W, H, .01)
    crts = {r.randrange(cols * rows), r.randrange(cols * rows)}
    for i in range(cols * rows):
        c, rw = i % cols, i // cols
        x = -W / 2 + .05 + c * (cw + .03) + cw / 2
        y = y0 + .05 + rw * (ch + .03) + ch / 2
        if i in crts:
            a.box(cw, ch, .02, M['dark'], x, y, .07)
            with a.at(x, y - ch * .45, .02):
                crt(a, .36, SCREENS[r.randrange(3)], M['dark'])
        else:
            with a.at(x, y, .08):
                monitor(a, cw, ch, stand=False, screen=M['screenOff'] if r.random() < .08 else SCREENS[r.randrange(3)])
    for k in range(5):  # cable fall from the bank to the floor
        x = -W / 2 + .2 + k * (W - .4) / 4
        a.cable([(x, y0 + .05, .04), (x + .05, y0 - .3, .1), (x - .03, .4, .12), (x + .1, .01, .3 + k * .05)], .007, M['dark'])


def monitor_stack(a, seed=2):
    """Desk-clamped pole with a 2×2 screen stack and a CRT beside. Origin on the desk surface."""
    r = random.Random(seed)
    a.box(.12, .03, .12, M['trim'], 0, .015, -.32)
    a.cyl(.022, .022, .95, M['trim'], 0, .49, -.32, 12)
    for y in (.35, .74):
        a.box(1.3, .03, .03, M['trim'], 0, y, -.3)
    for x, y, rx in ((-.33, .35, 0), (.33, .35, 0), (-.33, .74, .14), (.33, .74, .14)):
        with a.at(x, y, -.25, ry=-x * .35, rx=rx):
            monitor(a, .62, .36, stand=False, screen=SCREENS[r.randrange(3)])
        a.cable([(x, y - .18, -.28), (x * .5, y - .3, -.33), (0, .05, -.36), (.1, .0, -.38)], .005, M['dark'])
    note(a, -.62, .27, -.235, .1)
    note(a, .58, .64, -.205, -.15)
    with a.at(.9, 0, -.15, ry=-.5):
        crt(a)


# ---------- desk life ----------
def desk_lamp(a):
    a.cyl(.07, .075, .02, M['dark'], 0, .01, 0)
    a.box(.02, .3, .02, M['trim'], -.045, .17, 0, rz=.3)
    a.box(.02, .28, .02, M['trim'], .04, .36, 0, rz=-1.2)
    a.cyl(.012, .012, .03, M['dark'], -.089, .307, 0, 8, rx=math.pi / 2)
    with a.at(.172, .408, 0, rz=.5):
        a.cyl(.025, .06, .1, M['orange'], 0, 0, 0, 16)
        a.cyl(.05, .05, .004, M['warm'], 0, -.05, 0, 16)


def headphones(a):
    with a.at(0, .03, 0, rx=math.pi / 2):
        a.torus(.09, .01, M['dark'], 16, 6, arc=math.pi)
    for s in (-1, 1):
        a.cyl(.045, .045, .03, M['dark'], s * .09, .015, 0, 16)
    a.box(.01, .002, .006, M['neon'], .09, .05, 0)


def can(a, crushed=False):
    if crushed:
        with a.at(0, .03, 0, rz=math.pi / 2, s=(1, .9, .6)):
            a.cyl(.033, .033, .12, M['red'], 0, 0, 0, 12)
    else:
        a.cyl(.033, .033, .12, M['red'], 0, .06, 0, 12)
        a.cyl(.029, .029, .003, M['trim'], 0, .1215, 0, 12)


def takeout(a):
    a.box(.11, .09, .11, M['paper'], 0, .045, 0)
    a.box(.115, .01, .115, M['paper'], 0, .093, 0, rz=.15)
    for dx in (-.008, .008):
        a.box(.006, .22, .006, M['cardboard'], dx, .12, .02, rz=.35)


def data_pad(a, screen=None):
    a.box(.2, .01, .14, M['dark'], 0, .005, 0)
    with a.at(0, .0105, 0, rx=-math.pi / 2):
        a.plane(.18, .12, screen or M['screenUI'])


def desk_clutter(a, seed=1):
    """A desk's worth of mess. Origin on the desk surface, spread over about 1.2 × 0.5 m."""
    r = random.Random(seed)
    for i in range(5):
        a.box(.21, .004, .297, M['paper'], -.45 + r.random() * .05, .002 + i * .004, .05, ry=(r.random() - .5) * .5)
    with a.at(-.2, 0, .12): can(a)
    with a.at(-.12, 0, .17, ry=1.0): can(a, crushed=True)
    with a.at(.25, 0, -.05): takeout(a)
    with a.at(.45, 0, .12): mug(a)
    with a.at(-.05, 0, -.1, ry=.4): data_pad(a)
    with a.at(.1, 0, .15, ry=-.3): headphones(a)
    for i in range(4):
        with a.at(-.6 + r.random() * 1.1, .002, -.15 + r.random() * .3, rx=-math.pi / 2):
            a.plane(.07, .07, M['note'])
    a.box(.14, .006, .006, M['orange'], .35, .003, .2, ry=.7)  # pen
    a.box(.05, .008, .018, M['trim'], -.3, .004, -.15)  # usb stick


def workstation_lived(a, seed=3):
    """Hero desk: four-screen stack, CRT, laptop, clutter, lamp, cables, chair pushed back."""
    r = random.Random(seed)
    h, width = .75, 2.2
    desk(a, width, .85, h)
    with a.at(-.1, h, 0): monitor_stack(a, seed)
    with a.at(-.75, h, .15, ry=.35): laptop(a)
    with a.at(0, h, .2): keyboard(a)
    with a.at(.2, h, .1): desk_clutter(a, seed)
    with a.at(1.0, h, -.25, ry=2.6): desk_lamp(a)
    with a.at(width / 2 + .2, 0, -.15): pc_tower(a)
    with a.at(-width / 2 - .35, 0, .1, ry=.3): trash_bin(a, seed)
    with a.at(.4, 0, -.1, ry=.2):
        a.box(.5, .3, .35, M['cardboard'], 0, .15, 0)  # box shoved under the desk
        a.box(.06, .002, .36, M['tape'], 0, .301, 0)
    a.cable([(width / 2 + .2, .3, -.37), (width / 2, .02, -.4), (.5, .01, -.3), (-.2, .01, .5), (-1.4, .01, .9)], .008, M['dark'])
    a.cable([(width / 2 + .25, .2, -.37), (width / 2 + .1, .01, -.2), (1.3, .01, .7), (.6, .01, 1.4)], .006, M['orange'])
    with a.at(.25, 0, 1.0, ry=math.pi + .5 + r.random() * .3): chair(a)


# ---------- room fill ----------
def cable_run(a, length=2.5, seed=1):
    r = random.Random(seed)
    mats = [M['dark'], M['dark'], M['trim'], M['orange']]
    for k in range(4):
        pts = [(-length / 2 + i * length / 6, .0, (r.random() - .5) * .3 + k * .04) for i in range(7)]
        rad = .006 + r.random() * .006
        a.cable([(x, rad, z) for x, _, z in pts], rad, mats[k])
    a.box(.12, .02, .04, M['dark'], .3, .01, .05, ry=.4)  # power brick


def boxes_stack(a, seed=1):
    r = random.Random(seed)
    a.box(.5, .35, .4, M['cardboard'], 0, .175, 0)
    a.box(.06, .002, .41, M['tape'], 0, .351, 0)
    with a.at(.02, .35, 0, ry=.2):
        a.box(.4, .3, .35, M['cardboard'], 0, .15, 0)
        a.box(.05, .002, .36, M['tape'], 0, .301, 0)
        a.box(.12, .08, .002, M['paper'], .05, .15, .176)  # shipping label
    with a.at(.55, 0, .15, ry=-.4):  # an open box on the floor
        a.box(.36, .26, .3, M['cardboard'], 0, .13, 0)
        a.box(.34, .01, .28, M['dark'], 0, .25, 0)
        for s in (-1, 1):
            a.box(.36, .005, .14, M['cardboard'], 0, .3, s * .19, rx=s * .9)
        a.cable([(-.1, .25, 0), (0, .35, .05), (.08, .27, -.04), (.2, .4, .02)], .006, M['dark'])


def crate(a):
    a.box(.9, .45, .55, M['body'], 0, .225, 0)
    for x in (-.44, .44):
        for z in (-.27, .27):
            a.box(.04, .46, .04, M['trim'], x, .23, z)
    a.box(.92, .04, .57, M['trim'], 0, .44, 0)
    a.box(.7, .06, .002, M['tape'], 0, .12, .276)
    for i in range(8):
        a.box(.025, .07, .002, M['dark'], -.3 + i * .085, .12, .277, rz=.6)
    for x in (-.25, .25):
        a.box(.08, .05, .02, M['dark'], x, .38, .28)
    a.box(.3, .1, .002, M['orange'], .2, .28, .276)


def trash_bin(a, seed=1):
    r = random.Random(seed)
    a.cyl(.15, .12, .4, M['trim'], 0, .2, 0, 16)
    a.cyl(.135, .135, .004, M['dark'], 0, .37, 0, 16)
    for i in range(4):
        a.ico(.045, M['paper'], (r.random() - .5) * .14, .4 + r.random() * .03, (r.random() - .5) * .14, .35, seed * 10 + i)
    a.ico(.04, M['paper'], .25, .04, .1, .35, seed * 10 + 9)
    with a.at(-.22, 0, .05): can(a, crushed=True)


def poster(a, w=0.56, h=0.81, tilt=0.03):
    with a.at(0, 0, 0, rz=tilt):
        a.plane(w, h, M['poster'], 0, 0, .004)
        for x in (-1, 1):
            for y in (-1, 1):
                a.box(.06, .025, .002, M['tape'], x * (w / 2 - .01), y * (h / 2 - .01), .006, rz=x * y * .7)


def cork_board(a, seed=1):
    r = random.Random(seed)
    a.box(1.0, .6, .02, M['cork'], 0, 0, .01)
    with a.at(0, 0, .021):
        a.frame(1.0, .6, .015, M['trim'])
    for i in range(9):
        x, y = -.4 + r.random() * .8, -.22 + r.random() * .44
        big = r.random() < .4
        with a.at(x, y, .021 + i * .0004, rz=(r.random() - .5) * .3):
            a.plane(.14 if big else .07, .19 if big else .07, M['paper'] if big else M['note'])
        a.ico(.008, M['red'], x, y + (.08 if big else .03), .026)
    a.cable([(-.35, .15, .03), (0, .05, .033), (.3, .18, .03)], .0015, M['red'], seg=4)  # red string


def extinguisher(a):
    a.box(.2, .3, .03, M['trim'], 0, .9, -.08)
    a.cyl(.075, .075, .5, M['red'], 0, .8, 0, 16)
    a.cyl(.04, .075, .06, M['red'], 0, 1.08, 0, 16)
    a.box(.03, .08, .03, M['dark'], 0, 1.14, 0)
    a.box(.12, .015, .03, M['dark'], .03, 1.18, 0)
    a.cable([(.0, 1.12, .02), (.08, 1.05, .08), (.1, .8, .09), (.06, .62, .07)], .01, M['dark'])
    a.box(.15, .1, .002, M['paper'], 0, .78, .076)


def mini_fridge(a, seed=1):
    r = random.Random(seed)
    a.box(.5, .85, .55, M['body'], 0, .425, 0)
    a.box(.48, .003, .002, M['dark'], 0, .62, .276)
    a.box(.02, .2, .03, M['trim'], .2, .5, .29)
    a.box(.01, .006, .002, M['led'], -.2, .8, .276)
    for i in range(6):
        with a.at(-.15 + r.random() * .3, .2 + r.random() * .55, .2765, rz=(r.random() - .5) * .6):
            a.plane(.05 + r.random() * .05, .04 + r.random() * .04, [M['orange'], M['note'], M['paper'], M['red']][i % 4])
    for i in range(3):
        with a.at(-.15 + i * .08, .85, .1): can(a)
    with a.at(.12, .85, -.05): plant(a, False, 8, M['dark'])


def tool_cart(a, seed=1):
    for y in (.15, .55, .85):
        a.box(.65, .025, .42, M['trim'], 0, y, 0)
    for x in (-.3, .3):
        for z in (-.19, .19):
            a.box(.025, .8, .025, M['body'], x, .5, z)
            a.cyl(.035, .035, .03, M['dark'], x, .035, z, 12, rx=math.pi / 2)
    a.box(.32, .18, .26, M['beige'], -.12, .955, 0)  # oscilloscope
    a.plane(.14, .1, M['screenUI'], -.18, .97, .1305)
    for i in range(4):
        a.cyl(.012, .012, .01, M['dark'], -.02, .93 + (i % 2) * .04, .13 + .005, 8, rx=math.pi / 2)
    a.box(.15, .08, .12, M['dark'], .2, .905, 0)  # soldering station
    a.box(.05, .02, .002, M['warm'], .2, .92, .061)
    a.cyl(.01, .01, .14, M['trim'], .27, .93, -.03, 8, rz=.6)
    with a.at(.1, .58, 0, rx=math.pi / 2):
        a.torus(.07, .012, M['orange'], 24, 6)  # wire spool
    a.box(.3, .04, .2, M['dark'], -.15, .585, .0)  # parts bin
    for i in range(5):
        a.box(.002, .04, .2, M['trim'], -.27 + i * .06, .59, 0)
    a.box(.45, .2, .22, M['red'], 0, .265, 0)  # toolbox
    a.box(.2, .03, .03, M['dark'], 0, .38, 0)


def terminal_kiosk(a):
    a.box(.5, .9, .45, M['body'], 0, .5, 0)
    with a.at(0, 1.05, .02, rx=-.3):
        a.box(.5, .35, .1, M['body'], 0, 0, 0)
        a.plane(.42, .28, M['screen'], 0, .0, .0505)
    a.box(.45, .03, .2, M['dark'], 0, .82, .3)
    for s in (-1, 1):
        a.box(.012, 1.1, .012, M['warm'], s * .245, .62, .227)
    for x in (-.2, .2):
        for z in (-.18, .18):
            a.cyl(.03, .03, .03, M['dark'], x, .03, z, 12, rx=math.pi / 2)
    a.box(.3, .12, .002, M['dark'], 0, .3, .226)
    for i in range(4):
        a.box(.015, .015, .004, M['led'] if i else M['warm'], -.1 + i * .05, .3, .228)


def network_shelf(a, seed=1):
    """Wall shelf of switches with blinking ports and a cable waterfall. Origin at the floor, back on the wall."""
    r = random.Random(seed)
    a.box(.9, .025, .3, M['trim'], 0, 1.6, .15)
    for x in (-.4, .4):
        a.box(.025, .2, .03, M['trim'], x, 1.5, .02)
    for k, y in enumerate((1.635, 1.685)):
        a.box(.44, .045, .25, M['dark'], -.15, y, .14)
        for i in range(12):
            a.box(.018, .014, .003, M['trim'], -.34 + i * .025, y, .266)
            led = M['led'] if r.random() < .7 else M['ledG'] if r.random() < .6 else M['ledA']
            a.box(.005, .004, .003, led, -.34 + i * .025, y + .013, .266)
    a.box(.25, .05, .18, M['body'], .25, 1.638, .14)
    for dx in (-.08, .08):
        a.cyl(.006, .006, .18, M['dark'], .25 + dx, 1.75, .06, 6, rz=dx * 2)
    for i in range(6):
        x = -.33 + i * .05
        a.cable([(x, 1.64, .27), (x + .02, 1.5, .33), (x - .05 + r.random() * .1, 1.0, .25 + r.random() * .1), (x * 1.5, .3, .2), (x * 2, .01, .4 + r.random() * .3)], .004, [M['dark'], M['trim'], M['orange']][i % 3], seg=5)


# ---------- light fixtures ----------
def pendant_box(a, drop=0.85):
    """Boxy pendant on two cables. Origin at the ceiling."""
    for x in (-.18, .18):
        a.cyl(.004, .004, drop - .07, M['dark'], x, -(drop - .07) / 2, 0, 6)
    a.box(.5, .14, .3, M['dark'], 0, -drop, 0)
    a.box(.44, .01, .24, M['panel'], 0, -drop - .071, 0)
    a.box(.5, .006, .006, M['neon'], 0, -drop + .07, .151)
    for i in range(5):
        a.box(.004, .08, .002, M['trim'], -.1 + i * .05, -drop, .151)


def hex_cluster(a):
    """Three hexagonal panel lights on an arm (med-bay style). Origin at the ceiling."""
    a.cyl(.03, .03, .5, M['trim'], 0, -.25, 0, 12)
    with a.at(0, -.55, 0, rx=-math.pi / 2 + .35):
        for k in range(3):
            ang = math.pi / 2 + k * 2 * math.pi / 3
            cx, cy = .21 * math.cos(ang), .21 * math.sin(ang)
            hexp = [(cx + .17 * math.cos(math.pi / 6 + j * math.pi / 3), cy + .17 * math.sin(math.pi / 6 + j * math.pi / 3), 0) for j in range(6)]
            a.raw(hexp, [tuple(range(6))[::-1]], M['panel'])
            a.sweep(hexp, .018, M['dark'], closed=True)
            a.box(.04, .04, .06, M['trim'], cx * .4, cy * .4, .03)


# ---------- architecture add-ons ----------
def ceiling_duct(a, length=4.0):
    """Exposed duct with flanges and a vent grille. Origin at the ceiling."""
    a.box(length, .32, .5, M['trim'], 0, -.2, 0)
    n = int(length / .6)
    for i in range(n + 1):
        a.box(.03, .36, .54, M['dark'], -length / 2 + i * length / n, -.2, 0)
    for i in range(12):
        a.box(.4, .004, .015, M['dark'], .5, -.362, -.18 + i * .033)
    a.cable([(-length / 2, -.38, .27), (-length / 4, -.6, .3), (0, -.4, .3), (length / 4, -.55, .28), (length / 2, -.38, .27)], .008, M['dark'])


def wall_pipes(a, length=4.0, top=4.0):
    """Three pipes along a wall, one a glowing coolant line. Origin at the wall's floor line."""
    dy = top - 3.2  # pipes ride 0.45–0.75 m below the ceiling
    for y, r_, m in ((2.45 + dy, .05, M['trim']), (2.6 + dy, .035, M['body']), (2.74 + dy, .03, None)):
        if m:
            a.cyl(r_, r_, length, m, 0, y, r_ + .03, 12, rz=math.pi / 2)
        else:
            a.cyl(r_, r_, length, M['glass'], 0, y, r_ + .03, 12, rz=math.pi / 2)
            a.cyl(r_ * .45, r_ * .45, length, M['neon'], 0, y, r_ + .03, 8, rz=math.pi / 2)
    for i in range(int(length) + 1):
        a.box(.04, .38, .03, M['dark'], -length / 2 + i, 2.6 + dy, .015)
    with a.at(.8, 2.45 + dy, .13):
        a.torus(.07, .01, M['red'], 16, 6)
    a.cyl(.02, .02, .05, M['red'], .8, 2.45 + dy, .105, 8, rx=math.pi / 2)


DECOR = [
    ('Tech_MonitorWall_A', monitor_wall), ('Tech_MonitorWall_B', lambda a: monitor_wall(a, 5)),
    ('Tech_MonitorBank', monitor_bank), ('Tech_MonitorStack', monitor_stack),
    ('Tech_CRT', crt), ('Tech_Laptop', laptop), ('Tech_DataPad', data_pad),
    ('Tech_TerminalKiosk', terminal_kiosk), ('Tech_NetworkShelf', network_shelf), ('Tech_ToolCart', tool_cart),
    ('Furn_Workstation_Lived', workstation_lived), ('Furn_Workstation_Lived_B', lambda a: workstation_lived(a, 8)),
    ('Deco_DeskClutter', desk_clutter), ('Deco_DeskLamp', desk_lamp), ('Deco_Headphones', headphones),
    ('Deco_CableRun', cable_run), ('Deco_Boxes', boxes_stack), ('Deco_Crate', crate), ('Deco_TrashBin', trash_bin),
    ('Deco_Poster', poster), ('Deco_CorkBoard', cork_board), ('Deco_Extinguisher', extinguisher), ('Deco_MiniFridge', mini_fridge),
    ('Light_PendantBox', pendant_box), ('Light_HexCluster', hex_cluster),
    ('Arch_CeilingDuct_4m', ceiling_duct), ('Arch_WallPipes_4m', wall_pipes),
]
# pieces the player bumps into; the rest (wall-hung, desk-top, ceiling, cables) get no collider
SOLID = {'Tech_MonitorWall_A', 'Tech_MonitorWall_B', 'Tech_TerminalKiosk', 'Tech_ToolCart',
         'Furn_Workstation_Lived', 'Furn_Workstation_Lived_B', 'Deco_Boxes', 'Deco_Crate', 'Deco_TrashBin', 'Deco_MiniFridge'}
decor = {}
for name, fn in DECOR:
    a = Asset(name)
    fn(a)
    decor[name] = a.build()
    if name in SOLID:
        add_colliders(decor[name])
quad_uvs(decor)
export_all(decor)
layout(decor, y0=-14)
print('built', len(decor), 'decor assets ->', OUT)
for o in decor.values():
    print(f'{o.name}: {len(o.data.polygons)} faces, {sum(len(p.vertices) - 2 for p in o.data.polygons)} tris')
