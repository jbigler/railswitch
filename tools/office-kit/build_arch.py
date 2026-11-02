# Architecture set: elevator (stackable shaft piece + cab) and ceiling gear.
# exec build_kit.py first, in the same globals. Ceiling pieces have their origin
# at the ceiling plane and hang down (-Y).
use_collection('CyberOfficeArch')

M['floorDisplay'] = mat('RS_FloorDisplay', '#000000', 0, .3)  # three.js puts a canvas texture here
H, OW, OH = 4.0, 1.2, 2.4  # floor height, elevator opening


# ---------- elevator ----------
def elevator_frame(a):
    """One floor of the elevator: 4 m front wall with the door surround, call panel and
    a 2 × 2.15 m shaft section behind it (stack one per floor, 4 m apart). Origin at the
    floor, front face at z = 0; the cab parks at (0, floor, -0.2)."""
    t, side = .15, (4 - OW) / 2
    for s in (-1, 1):
        a.box(side, H, t, M['wall'], s * (OW / 2 + side / 2), H / 2, -t / 2)
        a.box(side - .08, .026, .018, M['neon'], s * (OW / 2 + .08 + (side - .08) / 2), .03, .009)
        a.box(.12, OH + .12, .07, M['trim'], s * (OW / 2 + .06), (OH + .12) / 2, .035)
        a.box(.04, H, .08, M['trim'], s * .98, H / 2, -1.2)  # guide rails
        a.box(.15, H, 2.3, M['wall'], s * 1.075, H / 2, -1.3)  # shaft side
    a.box(OW, H - OH, t, M['wall'], 0, OH + (H - OH) / 2, -t / 2)
    a.box(4, .026, .018, M['neon'], 0, H - .04, .009)
    a.box(OW + .24, .12, .07, M['trim'], 0, OH + .06, .035)
    with a.at(0, (OH + .12) / 2, .075):
        a.frame(OW + .26, OH + .14, .012)
    a.box(OW + .24, .02, .25, M['trim'], 0, .01, .0)  # sill
    a.box(2.3, H, .15, M['wall'], 0, H / 2, -2.375)  # shaft back
    for i in range(3):
        a.cyl(.008, .008, H, M['dark'], -.1 + i * .1, H / 2, -2.2, 6)  # hoist cables
    a.box(.3, .02, .02, M['warm'], 0, H - .2, -2.29)
    # call panel
    a.box(.14, .3, .03, M['trim'], OW / 2 + .35, 1.2, .015)
    a.box(.1, .26, .004, M['dark'], OW / 2 + .35, 1.2, .031)
    a.plane(.3, .12, M['floorDisplay'], 0, OH + .3, .072)
    a.box(.34, .16, .03, M['dark'], 0, OH + .3, .055)
    # worn: scuffed kick plates and a taped notice
    for s in (-1, 1):
        a.box(.5, .25, .004, M['body'], s * (OW / 2 + .4), .15, .002)
    with a.at(-(OW / 2 + .45), 1.45, .002, rz=.05):
        a.plane(.21, .3, M['paper'])
        a.box(.08, .02, .002, M['tape'], 0, .15, .001)


def elevator_door(a, s):
    """One sliding landing door; slides toward s by OW/2 to open."""
    a.box(OW / 2, OH, .04, M['body'], 0, 0, 0)
    a.box(.012, OH - .1, .012, M['neon'], -s * (OW / 4 - .01), 0, .021)
    a.box(OW / 2 - .1, .25, .004, M['trim'], 0, -OH / 2 + .2, .021)


def call_button(a, m):
    a.cyl(.022, .022, .01, m, 0, 0, 0, 12, rx=math.pi / 2)


def cab(a):
    """Elevator car: origin at the floor, front opening at z = 0, interior toward -Z."""
    W, D, Hc = 1.9, 2.0, 2.6
    a.box(W, .08, D, M['body'], 0, -.04, -D / 2)
    a.box(W - .1, .004, D - .1, M['trim'], 0, .002, -D / 2)
    for s in (-1, 1):
        a.box(.05, Hc, D, M['body'], s * (W / 2 - .025), Hc / 2, -D / 2)
        a.box(.4, Hc, .05, M['body'], s * .75, Hc / 2, -.025)
        a.cyl(.015, .015, D - .3, M['trim'], s * (W / 2 - .08), .95, -D / 2, 8, rx=math.pi / 2)
        a.box(.012, Hc - .2, .012, M['neon'], s * (W / 2 - .06), Hc / 2, -D + .06)
    a.box(W, Hc, .05, M['body'], 0, Hc / 2, -D + .025)
    a.cyl(.015, .015, W - .2, M['trim'], 0, .95, -D + .1, 8, rz=math.pi / 2)
    a.box(1.1, Hc - 2.3, .05, M['body'], 0, 2.3 + (Hc - 2.3) / 2, -.025)
    a.box(W, .06, D, M['body'], 0, Hc + .03, -D / 2)
    a.box(W - .4, .01, D - .4, M['panel'], 0, Hc - .006, -D / 2)
    a.cyl(.08, .08, .2, M['trim'], 0, Hc + .16, -D / 2, 12)  # hoist hitch
    # button panel on the inside of the front wall, facing the back
    with a.at(.72, 1.25, -.051, ry=math.pi):
        a.box(.18, .5, .02, M['trim'], 0, 0, -.01)
        a.plane(.14, .06, M['floorDisplay'], 0, .18, .001)
        for i in range(8):
            a.cyl(.015, .015, .006, M['led'] if i % 3 else M['warm'], -.035 + (i % 2) * .07, .08 - (i // 2) * .06, .003, 10, rx=math.pi / 2)
    # lived-in: sticker-bombed ad screen and a torn poster
    with a.at(W / 2 - .051, 1.6, -1.1, ry=-math.pi / 2):
        monitor(a, .55, .32, stand=False, screen=M['screen'])
    with a.at(-W / 2 + .051, 1.5, -1.0, ry=math.pi / 2):
        a.plane(.4, .55, M['paper'])
        a.box(.4, .02, .002, M['tape'], 0, .27, .001)


def cab_door(a, s):
    a.box(.55, 2.3, .03, M['body'], 0, 0, 0)
    a.box(.01, 2.2, .01, M['neon'], -s * .27, 0, .016)


def build_elevator():
    out = {}
    a = Asset('Arch_Elevator_Floor'); elevator_frame(a); frame = a.build()
    side = (4 - OW) / 2
    add_colliders(frame, [(side, H, .15, -(OW / 2 + side / 2), H / 2, -.075), (side, H, .15, OW / 2 + side / 2, H / 2, -.075),
                          (OW, H - OH, .15, 0, OH + (H - OH) / 2, -.075),
                          (.15, H, 2.3, -1.075, H / 2, -1.3), (.15, H, 2.3, 1.075, H / 2, -1.3), (2.3, H, .15, 0, H / 2, -2.375)])
    for s, n in ((-1, 'Door_L'), (1, 'Door_R')):
        d = part(frame, n, lambda a, s=s: elevator_door(a, s), (s * OW / 4, OH / 2, .02))
        add_colliders(d)
    part(frame, 'Btn_Up', lambda a: call_button(a, M['led']), (OW / 2 + .35, 1.26, .033))
    part(frame, 'Btn_Down', lambda a: call_button(a, M['warm']), (OW / 2 + .35, 1.14, .033))
    out['Arch_Elevator_Floor'] = frame

    a = Asset('Arch_Elevator_Cab'); cab(a); car = a.build()
    W, D, Hc = 1.9, 2.0, 2.6
    add_colliders(car, [(W, .08, D, 0, -.04, -D / 2), (.05, Hc, D, -(W / 2 - .025), Hc / 2, -D / 2), (.05, Hc, D, W / 2 - .025, Hc / 2, -D / 2),
                        (W, Hc, .05, 0, Hc / 2, -D + .025), (W, .06, D, 0, Hc + .03, -D / 2),
                        (.4, Hc, .05, -.75, Hc / 2, -.025), (.4, Hc, .05, .75, Hc / 2, -.025), (1.1, Hc - 2.3, .05, 0, 2.45, -.025)])
    for s, n in ((-1, 'CabDoor_L'), (1, 'CabDoor_R')):
        d = part(car, n, lambda a, s=s: cab_door(a, s), (s * .275, 1.15, .015))
        add_colliders(d)
    out['Arch_Elevator_Cab'] = car
    return out


# ---------- ceiling gear (origin at the ceiling, hangs down) ----------
def cable_tray(a, length=4.0, seed=1):
    r = random.Random(seed)
    y = -.4
    a.box(length, .006, .3, M['trim'], 0, y, 0)
    for s in (-1, 1):
        a.box(length, .06, .006, M['trim'], 0, y + .03, s * .15)
    for x in (-length / 2 + .3, 0, length / 2 - .3):
        for s in (-1, 1):
            a.cyl(.006, .006, -y, M['dark'], x, y / 2, s * .17, 6)
        a.box(.04, .03, .4, M['dark'], x, y - .02, 0)
    mats = [M['dark'], M['dark'], M['orange'], M['trim'], M['red']]
    for k in range(5):
        rad = .008 + r.random() * .008
        z = -.11 + k * .055
        a.cable([(x, y + .004 + rad, z + (r.random() - .5) * .03) for x in np.linspace(-length / 2, length / 2, 6)], rad, mats[k])
    e = length / 2
    a.cable([(e - .6, y + .02, .1), (e - .4, y - .45, .2), (e - .15, y - .3, .16), (e - .05, -.02, .1)], .01, M['dark'])


def fan_blades(a):
    a.cyl(.06, .06, .04, M['dark'], 0, 0, 0, 16)
    for k in range(4):
        with a.at(0, 0, 0, ry=k * math.pi / 2):
            a.box(.6, .008, .12, M['trim'], .36, 0, 0, rx=.12)


def fan(a):
    a.cyl(.08, .08, .02, M['trim'], 0, -.01, 0, 16)
    a.cyl(.015, .015, .5, M['trim'], 0, -.26, 0, 8)
    a.cyl(.12, .1, .12, M['dark'], 0, -.56, 0, 20)
    with a.at(0, -.63, 0, rx=math.pi / 2):
        a.torus(.09, .008, M['neon'], 24, 6)


def pipe_run(a, length=4.0):
    for z, y, r_, m in ((-.15, -.28, .06, M['trim']), (.05, -.24, .04, M['body']), (.22, -.2, .03, None)):
        if m:
            a.cyl(r_, r_, length, m, 0, y, z, 12, rz=math.pi / 2)
        else:
            a.cyl(r_, r_, length, M['glass'], 0, y, z, 12, rz=math.pi / 2)
            a.cyl(r_ * .45, r_ * .45, length, M['neon'], 0, y, z, 8, rz=math.pi / 2)
        for x in np.linspace(-length / 2 + .4, length / 2 - .4, 3):
            a.cyl(.005, .005, -y - r_, M['dark'], x, (y + r_) / 2, z, 6)
            a.box(.03, .012, r_ * 2 + .02, M['dark'], x, y - r_ - .006, z)
    for x in (-1.1, .4, 1.5):
        a.cyl(.062, .062, .06, M['tape'], x, -.28, -.15, 12, rz=math.pi / 2)  # label bands
    a.cyl(.06, .06, .28, M['trim'], length / 2 - .5, -.14, -.15, 12)  # riser into the ceiling
    a.cyl(.09, .09, .02, M['dark'], length / 2 - .5, -.01, -.15, 12)
    with a.at(-.6, -.24, .05 + .07, rx=0):
        a.torus(.06, .008, M['red'], 16, 6)
    a.cyl(.012, .012, .07, M['red'], -.6, -.24, .085, 8, rx=math.pi / 2)


def vent(a):
    a.box(.62, .05, .62, M['dark'], 0, -.025, 0)
    with a.at(0, -.052, 0, rx=-math.pi / 2):
        a.frame(.6, .6, .02, M['trim'])
    for i in range(10):
        a.box(.56, .004, .03, M['trim'], 0, -.04, -.25 + i * .055, rx=.6)


def light_run(a, length=4.0, drop=.6):
    for x in (-length / 2 + .3, length / 2 - .3):
        a.cyl(.003, .003, drop, M['dark'], x, -drop / 2, 0, 4)
    a.box(length, .05, .08, M['dark'], 0, -drop - .025, 0)
    a.box(length - .04, .012, .04, M['neon'], 0, -drop - .056, 0)


def sprinkler(a, length=4.0):
    a.cyl(.025, .025, length, M['red'], 0, -.15, .0, 10, rz=math.pi / 2)
    for x in np.linspace(-length / 2 + .5, length / 2 - .5, 3):
        a.cyl(.004, .004, .14, M['dark'], x, -.07, 0, 4)
        a.cyl(.01, .01, .08, M['trim'], x, -.2, 0, 8)
        a.cyl(.025, .025, .004, M['trim'], x, -.245, 0, 10)


def hanging_cables(a, seed=1):
    r = random.Random(seed)
    for k in range(4):
        x0, x1 = -1.2 + r.random() * .6, .6 + r.random() * .6
        z = -.3 + k * .2
        sag = .35 + r.random() * .5
        a.cable([(x0, -.01, z), (x0 + .2, -sag * .7, z), ((x0 + x1) / 2, -sag, z + .05), (x1 - .2, -sag * .6, z), (x1, -.01, z)],
                .007 + r.random() * .006, [M['dark'], M['orange'], M['trim'], M['dark']][k])
        for x in (x0, x1):
            a.box(.03, .02, .03, M['trim'], x, -.01, z)


arch = build_elevator()
for name, fn in [('Arch_Ceiling_CableTray_4m', cable_tray), ('Arch_Ceiling_PipeRun_4m', pipe_run), ('Arch_Ceiling_Vent', vent),
                 ('Arch_Ceiling_LightRun_4m', light_run), ('Arch_Ceiling_Sprinkler_4m', sprinkler),
                 ('Arch_Ceiling_HangingCables', hanging_cables), ('Arch_Ceiling_Fan', fan)]:
    a = Asset(name)
    fn(a)
    arch[name] = a.build()
part(arch['Arch_Ceiling_Fan'], 'Fan_Blades', fan_blades, (0, -.6, 0))

quad_uvs(arch, prefixes=('RS_Screen', 'RS_Poster', 'RS_City', 'RS_FloorDisplay'))
export_all(arch)
layout(arch, y0=-24)
print('built', len(arch), 'arch assets ->', OUT)
