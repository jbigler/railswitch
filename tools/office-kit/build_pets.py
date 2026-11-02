# Pets: one per floor. Stylized low-poly; heads, tails, fish and jellyfish are
# separate named nodes (pivot at the joint) for idle motion in three.js.
# exec build_kit.py first, in the same globals.
use_collection('CyberOfficePets')


def fur(name, hexcol, rough=.9, emit=None, strength=0.0, alpha=1.0, cull=True):
    m = mat(name, hexcol, 0, rough, emit=emit, strength=strength, alpha=alpha)
    m.use_backface_culling = cull
    return m


P = dict(
    black=fur('RS_Fur_Black', '#17171c'), ginger=fur('RS_Fur_Ginger', '#b5652a'), tan=fur('RS_Fur_Tan', '#c07a3c'),
    cream=fur('RS_Fur_Cream', '#e6d6b8'), pink=fur('RS_Pink', '#d47a8a', .6),
    eye=fur('RS_EyeDark', '#050505', .15), eyeW=fur('RS_EyeWhite', '#eeeeee', .4),
    red=fur('RS_Feather_Red', '#c4202b', .7), blue=fur('RS_Feather_Blue', '#1f5fbf', .7),
    yellow=fur('RS_Feather_Yellow', '#e3b51e', .7), beak=fur('RS_Beak', '#e8e0c8', .5),
    water=fur('RS_Water', '#0b4a5c', .05, emit='#0e6a80', strength=.4, alpha=.3),
    bubble=fur('RS_Bubble', '#cfefff', .1, alpha=.5),
    gravel=fur('RS_Gravel', '#4c4843'), rock=fur('RS_Rock', '#3a3d42', .8), sand=fur('RS_Sand', '#a8865a'),
    wood=fur('RS_Wood', '#4a3220', .8), gecko=fur('RS_Gecko', '#c9a33c', .6), spots=fur('RS_GeckoSpot', '#5a3a1a', .6),
    fishO=fur('RS_Fish_Orange', '#ff7a1a', .4, emit='#ff7a1a', strength=.6, cull=False),
    fishB=fur('RS_Fish_Blue', '#3a8bff', .4, emit='#3a8bff', strength=.6, cull=False),
    jelly=fur('RS_Jelly', '#ff7ad9', .3, emit='#ff5ad1', strength=2.2, alpha=.85, cull=False),
    jellyCore=fur('RS_JellyCore', '#ff3fb4', .3, emit='#ff3fb4', strength=3.5),
    tentacle=fur('RS_JellyTentacle', '#ffc2ee', .3, emit='#ff8be0', strength=2.5),
    kibble=fur('RS_Kibble', '#5a3a20'),
)


# ---------- metaball animals ----------
# Bodies are metaballs (smooth, connected forms) converted to mesh, decimated to a
# triangle target and colored per face by a rule; ears, eyes, whiskers, beaks are
# small hand-placed details joined on. Radii below are the visible radii.
P.update(white=fur('RS_Fur_White', '#e8e6e0'), gingerDark=fur('RS_Fur_GingerDark', '#7a3a14'),
         whisker=fur('RS_Whisker', '#cfcfcf', .5), feet=fur('RS_Feet', '#3a3a40', .7))


def ear(a, m, x, y, z, s, tilt=.3, h=.055, r=.026, flat=.5, back=-.15):
    with a.at(x, y, z, rx=back, rz=-s * tilt, s=(1, 1, flat)):
        a.cyl(.002, r, h, m, 0, 0, 0, 4)


# --- sitting black cat: glowing eyes follow the accent, white chest blaze
def cat_sitting():
    B, W = P['black'], P['white']
    body = meta('Pet_Cat_Sitting', [
        ('e', (0, .095, -.035), .095, (.85, .8, 1)), ('e', (0, .165, .015), .075, (.8, 1.15, .85)), ('b', (0, .205, .055), .055),
        ('e', (.055, .075, -.02), .06, (.6, .9, 1.1)), ('e', (-.055, .075, -.02), .06, (.6, .9, 1.1)),
        ('e', (.05, .016, .045), .028, (.6, .4, 1.3)), ('e', (-.05, .016, .045), .028, (.6, .4, 1.3)),
        ('c', (.033, .025, .085), (.03, .17, .06), .02), ('c', (-.033, .025, .085), (-.03, .17, .06), .02),
        ('e', (.034, .014, .095), .024, (.9, .55, 1.2)), ('e', (-.034, .014, .095), .024, (.9, .55, 1.2)),
        ('b', (0, .245, .06), .042)], .004, [B, W],
        color=lambda c, n: 1 if c.z > .07 and .13 < c.y < .25 and abs(c.x) < .035 and n.z > .3 else 0,
        target=2200, details=lambda a: cat_collar(a))
    head = meta('Cat_Head', [
        ('e', (0, .035, 0), .055, (1.1, .92, 1)), ('e', (.028, .012, .028), .028, (1, .8, 1)), ('e', (-.028, .012, .028), .028, (1, .8, 1)),
        ('e', (0, .01, .05), .022, (1.15, .75, .9)), ('b', (0, -.008, .042), .014)], .003, [B],
        target=1400, details=cat_face)
    attach(head, body, (0, .27, .075))
    tail = meta('Cat_Tail', chain([(0, 0, 0), (.05, -.02, -.05), (.13, -.03, -.04), (.17, -.03, .04), (.15, -.03, .12)], .014, .009),
                .003, [B], target=500)
    attach(tail, body, (0, .04, -.12))
    return body


def cat_collar(a):
    with a.at(0, .24, .06, rx=math.pi / 2 + .3):
        a.torus(.044, .006, M['neon'], 24, 6)
    a.box(.012, .016, .004, M['warm'], 0, .222, .1)


def cat_face(a):
    for s in (-1, 1):
        ear(a, P['black'], s * .036, .085, -.005, s)
        ear(a, P['pink'], s * .035, .083, .004, s, h=.04, r=.017, flat=.3)
        a.sphere(.012, M['led'], s * .024, .04, .05, s=(1, .75, .5), seg=10, rings=6)
        a.box(.003, .014, .002, P['eye'], s * .024, .04, .0565)
        for k in range(3):
            a.box(.06, .0008, .0008, P['whisker'], s * .05, .012 - k * .004, .055, ry=-s * .15, rz=s * (k - 1) * .12)
    a.box(.012, .008, .006, P['pink'], 0, .022, .069)


# --- curled ginger tabby on a cushion, asleep
def cat_curled():
    def color(c, n):
        if (c.z > .09 and c.y < .075 and c.x > .05) or (c.y < .035 and c.z > .08):
            return 2  # cream muzzle, chest, paws
        head = (c - Vector((.1, .085, .085))).length < .055
        if not head and n.y > .25 and math.sin(math.atan2(c.z + .01, c.x) * 10) > .45:
            return 1  # tabby stripes
        return 0
    body = meta('CatCurled_Body', [
        ('e', (0, .0, -.01), .12, (1.15, .5, .8)), ('e', (-.085, .005, -.03), .075, (.9, .75, 1)), ('e', (.075, .0, .03), .065, (.9, .7, 1)),
        ('e', (.1, .025, .085), .05, (1.05, .85, 1)), ('e', (.112, .008, .125), .02, (1.1, .75, .9)),
        ('e', (.03, -.032, .105), .026, (1.3, .55, .8)), ('e', (.07, -.034, .115), .024, (1.1, .55, .9)),
        *chain([(-.15, -.03, -.02), (-.14, -.035, .07), (-.06, -.038, .14), (.02, -.038, .16)], .02, .014)],
        .004, [P['ginger'], P['gingerDark'], P['cream']],
        color=lambda c, n: color(c + Vector((0, .06, 0)), n), target=2600, details=curled_face)
    return body


def curled_face(a):
    with a.at(.1, .025, .085, ry=-.5):
        for s in (-1, 1):
            ear(a, P['ginger'], s * .03, .04, -.005, s, h=.045, r=.022)
            a.box(.016, .0025, .003, P['eye'], s * .022, .012, .047, rz=s * .15)
        a.box(.01, .007, .005, P['pink'], 0, -.004, .063)


# --- shiba lying on its bed: cream urajiro underside, curled fluffy tail
def dog(prefix='Dog', root=None):
    T, C = P['tan'], P['cream']
    body = meta(root or f'{prefix}_Body', [
        ('e', (0, .13, .11), .1, (.95, 1, 1)), ('e', (0, .11, -.04), .11, (.9, .85, 1.4)), ('e', (0, .1, -.2), .095, (1, .9, 1)),
        ('e', (.075, .075, -.18), .065, (.6, .9, 1.2)), ('e', (-.075, .075, -.18), .065, (.6, .9, 1.2)),
        ('e', (.085, .02, -.07), .028, (.8, .5, 1.6)), ('e', (-.085, .02, -.07), .028, (.8, .5, 1.6)),
        ('c', (.055, .035, .15), (.06, .028, .31), .028), ('c', (-.055, .035, .15), (-.06, .028, .31), .028),
        ('e', (.06, .022, .335), .03, (1, .6, 1.2)), ('e', (-.06, .022, .335), .03, (1, .6, 1.2)),
        ('e', (0, .2, .17), .065, (.9, 1, .9))], .005, [T, C],
        color=lambda c, n: 1 if n.y < -.3 or (c.z > .16 and c.y < .17 and abs(c.x) < .06) or c.y < .045 else 0,
        target=1900, details=dog_collar)
    head = meta(f'{prefix}_Head', [
        ('e', (0, .04, .025), .07, (1.05, .9, 1)), ('e', (.04, .012, .055), .038, (1, .85, 1)), ('e', (-.04, .012, .055), .038, (1, .85, 1)),
        ('e', (0, .003, .11), .036, (.85, .72, 1.25))], .004, [T, C],
        color=lambda c, n: 1 if (c.z > .06 and c.y < .02) or (abs(c.x) > .03 and c.y < .015 and c.z > .03)
        or min((c - Vector((s * .025, .07, .08))).length for s in (-1, 1)) < .012 else 0,
        target=1100, details=dog_face)
    attach(head, body, (0, .23, .2))
    tail = meta(f'{prefix}_Tail', chain([(0, 0, 0), (0, .06, -.02), (0, .1, .02), (.03, .09, .07), (.04, .06, .06)], .032, .022),
                .004, [T, C], color=lambda c, n: 1 if n.y < -.2 else 0, target=450)
    attach(tail, body, (0, .17, -.27))
    return body


def dog_collar(a):
    with a.at(0, .19, .18, rx=-.83):
        a.torus(.07, .01, M['neon'], 24, 6)
    a.box(.02, .025, .006, M['warm'], 0, .13, .22)


def dog_face(a):
    a.sphere(.015, P['eye'], 0, .015, .155, seg=10, rings=6)
    a.box(.03, .002, .002, P['eye'], 0, -.012, .13)
    for s in (-1, 1):
        a.sphere(.011, P['eye'], s * .035, .05, .085, seg=10, rings=6)
        a.sphere(.003, P['eyeW'], s * .033, .054, .094, seg=6, rings=4)
        ear(a, P['tan'], s * .045, .1, .015, s, tilt=.22, h=.065, r=.032, flat=.55, back=-.1)
        ear(a, P['cream'], s * .044, .097, .021, s, tilt=.22, h=.045, r=.02, flat=.3, back=-.1)


# --- macaw on its perch: red body, yellow coverts, blue flight feathers
def parrot(prefix='Parrot', root=None, folded=True):
    """folded: the wings are part of the body; otherwise they are <prefix>_Wing_L/R nodes."""
    def color(c, n):
        if abs(c.x) > .03 and c.z < .02 and c.y > .04:
            return 1 if c.y > .115 else 2
        return 0
    wings = [('e', (.043, .1, -.02), .04, (.42, 1.35, .9)), ('e', (-.043, .1, -.02), .04, (.42, 1.35, .9))] if folded else []
    body = meta(root or f'{prefix}_Body', [
        ('e', (0, .065, .012), .05, (.9, 1.1, .9)), ('e', (0, .12, .018), .045, (.9, 1, .9)), ('e', (0, .1, -.018), .045, (.85, 1.3, .8)),
        *wings, ('b', (0, .165, .02), .035)],
        .003, [P['red'], P['yellow'], P['blue']], color=color, target=1800 if folded else 1300, details=parrot_tail)
    if not folded:
        for s, side in ((1, 'L'), (-1, 'R')):  # Wing_L at +x
            w = meta(f'{prefix}_Wing_{side}', [('e', (s * .006, -.035, -.008), .04, (.42, 1.35, .9))], .003, [P['yellow'], P['blue']],
                     color=lambda c, n: 0 if c.y > -.02 else 1, target=350,
                     details=lambda a, s=s: a.box(.01, .08, .045, P['blue'], s * .008, -.085, -.012))
            attach(w, body, (s * .037, .135, -.012))
    head = meta(f'{prefix}_Head', [('e', (0, .025, .005), .04, (.95, 1, 1.05)), ('e', (0, .012, .032), .024, (.9, .85, .8))], .0025,
                [P['red'], P['eyeW']], color=lambda c, n: 1 if abs(c.x) > .018 and c.z > .005 and c.y < .04 else 0,
                target=1000, details=parrot_face)
    attach(head, body, (0, .18, .025))
    return body


def parrot_tail(a):
    for k, (m, w, L) in enumerate([(P['red'], .022, .22), (P['blue'], .02, .2), (P['red'], .02, .18)]):
        with a.at((k - 1) * .014, .045, -.05, rx=.42, rz=(k - 1) * .08):
            a.box(w, L, .006, m, 0, -L / 2, 0)
    for s in (-1, 1):
        a.box(.01, .025, .01, P['feet'], s * .02, .015, .005)
        for z in (.012, -.012):
            a.box(.007, .007, .024, P['feet'], s * .02, .004, z)


def parrot_face(a):
    a.sphere(.02, P['beak'], 0, .018, .05, s=(.8, 1, 1.1), seg=10, rings=6)
    a.cyl(.002, .012, .03, P['beak'], 0, -.002, .066, 8, rx=math.pi - .3)
    a.sphere(.013, P['eye'], 0, .0, .045, s=(.8, .7, 1), seg=8, rings=6)
    for s in (-1, 1):
        a.sphere(.008, P['eye'], s * .031, .035, .03, seg=8, rings=6)


# --- leopard gecko: fat tail, spots, cream belly
def gecko():
    def color(c, n):
        if n.y < -.3:
            return 2
        if n.y > .2 and math.sin(c.x * 420) + math.sin(c.z * 390 + 1.3) > 1.1:
            return 1
        return 0
    return meta('Gecko', [
        ('e', (0, .014, .06), .017, (1, .7, 1.3)), ('e', (0, .013, .035), .014, (1, .7, 1)), ('e', (0, .013, 0), .022, (.85, .5, 1.5)),
        *chain([(0, .011, -.03), (.01, .01, -.06), (.03, .009, -.085), (.05, .008, -.1), (.065, .007, -.105)], .016, .005),
        ('c', (.015, .01, .03), (.035, .004, .04), .006), ('c', (-.015, .01, .03), (-.035, .004, .04), .006),
        ('c', (.015, .01, -.02), (.035, .004, -.03), .007), ('c', (-.015, .01, -.02), (-.035, .004, -.03), .007)],
        .0018, [P['gecko'], P['spots'], P['cream']], color=color, target=1400, details=gecko_face)


def gecko_face(a):
    for s in (-1, 1):
        a.sphere(.006, P['eye'], s * .012, .022, .07, seg=8, rings=6)
        for z in (.04, -.03):
            for k in range(3):
                a.box(.008, .002, .002, P['gecko'], s * .039, .003, z + (k - 1) * .005, ry=s * (k - 1) * .5)


# ---------- walkers: standing poses with leg nodes (<prefix>_Leg_FL/FR/BL/BR, pivot at the hip) ----------
CAT_LEGS = {'F': [('c', (0, 0, 0), (0, -.17, 0), .02), ('e', (0, -.19, .012), .022, (1, .6, 1.3))],
            'B': [('e', (0, -.03, -.005), .045, (.6, 1, 1.1)), ('c', (0, -.05, -.02), (0, -.18, 0), .018),
                  ('e', (0, -.193, .012), .022, (1, .6, 1.3))]}
DOG_LEGS = {'F': [('c', (0, 0, 0), (0, -.2, 0), .028), ('e', (0, -.222, .015), .032, (1, .6, 1.3))],
            'B': [('e', (0, -.04, -.01), .06, (.6, 1, 1.15)), ('c', (0, -.07, -.03), (0, -.21, 0), .024),
                  ('e', (0, -.226, .015), .03, (1, .6, 1.3))]}


def legs(body, prefix, shapes, pivots, mats, color=None, target=300, res=.003):
    for fb, (x, y, z) in pivots.items():
        for s, side in ((1, 'L'), (-1, 'R')):
            attach(meta(f'{prefix}_Leg_{fb}{side}', shapes[fb], res, mats, color=color, target=target), body, (s * x, y, z))


def cat_walk():
    B, W = P['black'], P['white']
    body = meta('Pet_Cat_Walk', [
        ('e', (0, .215, -.01), .075, (.75, .75, 1.9)), ('e', (0, .22, .08), .068, (.85, .95, 1)),
        ('e', (0, .215, -.1), .065, (.85, .9, 1)), ('e', (0, .265, .13), .045, (1, 1, 1.1))], .004, [B, W],
        color=lambda c, n: 1 if c.z > .1 and .17 < c.y < .27 and abs(c.x) < .035 and n.z > .3 else 0, target=1400,
        details=lambda a: walk_collar(a, (0, .265, .14), .046, -.5))
    head = meta('CatWalk_Head', [
        ('e', (0, .035, 0), .055, (1.1, .92, 1)), ('e', (.028, .012, .028), .028, (1, .8, 1)), ('e', (-.028, .012, .028), .028, (1, .8, 1)),
        ('e', (0, .01, .05), .022, (1.15, .75, .9)), ('b', (0, -.008, .042), .014)], .003, [B], target=1200, details=cat_face)
    attach(head, body, (0, .29, .165))
    attach(meta('CatWalk_Tail', chain([(0, 0, 0), (0, .04, -.05), (0, .12, -.08), (0, .19, -.06), (0, .22, -.02)], .013, .009),
                .003, [B], target=400), body, (0, .225, -.165))
    legs(body, 'CatWalk', CAT_LEGS, {'F': (.04, .205, .1), 'B': (.045, .205, -.12)}, [B], target=260)
    return body


def walk_collar(a, at, r, tilt):
    with a.at(*at, rx=tilt):
        a.torus(r, .007, M['neon'], 24, 6)


def dog_walk():
    T, C = P['tan'], P['cream']
    body = meta('Pet_Dog_Walk', [
        ('e', (0, .275, -.02), .11, (.85, .85, 1.75)), ('e', (0, .285, .13), .1, (.95, 1, .95)),
        ('e', (0, .275, -.18), .095, (.9, .9, 1)), ('e', (0, .345, .19), .065, (.9, 1.1, .9))], .005, [T, C],
        color=lambda c, n: 1 if n.y < -.3 or (c.z > .17 and c.y < .34 and abs(c.x) < .06) else 0, target=1500,
        details=lambda a: walk_collar(a, (0, .35, .2), .068, -.7))
    head = meta('DogWalk_Head', [
        ('e', (0, .04, .025), .07, (1.05, .9, 1)), ('e', (.04, .012, .055), .038, (1, .85, 1)), ('e', (-.04, .012, .055), .038, (1, .85, 1)),
        ('e', (0, .003, .11), .036, (.85, .72, 1.25))], .004, [T, C],
        color=lambda c, n: 1 if (c.z > .06 and c.y < .02) or (abs(c.x) > .03 and c.y < .015 and c.z > .03)
        or min((c - Vector((s * .025, .07, .08))).length for s in (-1, 1)) < .012 else 0,
        target=1000, details=dog_face)
    attach(head, body, (0, .385, .215))
    attach(meta('DogWalk_Tail', chain([(0, 0, 0), (0, .06, -.02), (0, .1, .02), (.03, .09, .07), (.04, .06, .06)], .032, .022),
                .004, [T, C], color=lambda c, n: 1 if n.y < -.2 else 0, target=400), body, (0, .33, -.27))
    legs(body, 'DogWalk', DOG_LEGS, {'F': (.06, .245, .14), 'B': (.065, .25, -.2)}, [T, C],
         color=lambda c, n: 1 if c.y < -.13 else 0, target=300)
    return body


# ---------- props the animals sit on ----------
def cat_curled_cushion(a):
    a.cyl(.22, .24, .06, M['cushion'], 0, .03, 0, 24)
    with a.at(0, .055, 0, rx=math.pi / 2):
        a.torus(.22, .012, M['fabric'], 32, 6)


def dog_bed(a):
    with a.at(0, .07, 0, rx=math.pi / 2):
        a.torus(.34, .08, M['cushion'], 24, 6)
    a.cyl(.32, .32, .05, M['fabric'], 0, .03, 0, 24)
    with a.at(.28, .0, .32, ry=.6):  # chewed toy bone
        a.cyl(.012, .012, .1, P['beak'], 0, .015, 0, 8, rz=math.pi / 2)
        for s in (-1, 1):
            a.sphere(.02, P['beak'], s * .055, .015, 0)


def perch(a):
    a.cyl(.2, .22, .03, M['dark'], 0, .015, 0, 24)
    a.cyl(.19, .19, .01, P['gravel'], 0, .035, 0, 24)
    a.cyl(.015, .015, 1.25, M['trim'], 0, .655, 0, 8)
    a.cyl(.012, .012, .5, P['wood'], 0, 1.27, 0, 8, rz=math.pi / 2)
    a.cyl(.04, .035, .04, M['trim'], .22, 1.25, 0, 12)
    for i in range(3):
        a.ico(.008, P['kibble'], .21 + i * .01, 1.27, .005 * i)
    a.cable([(-.2, 1.27, 0), (-.21, 1.18, .0), (-.2, 1.1, .01)], .004, M['dark'], seg=4)  # hanging toy
    a.box(.03, .03, .03, P['yellow'], -.2, 1.08, .01)


# ---------- tanks ----------
AQ_W, AQ_D, AQ_C, AQ_T = 1.2, .45, .75, .55


def aquarium(a, seed=1):
    r = random.Random(seed)
    W, D, ch, th = AQ_W, AQ_D, AQ_C, AQ_T
    a.box(W, ch, D, M['body'], 0, ch / 2, 0)
    a.box(.003, ch - .1, .002, M['dark'], 0, ch / 2, D / 2 + .001)
    for s in (-1, 1):
        a.box(.02, .1, .02, M['trim'], s * .06, ch / 2, D / 2 + .01)
    a.box(W - .1, .006, .006, M['neon'], 0, .01, D / 2)
    y0 = ch
    for x in (-W / 2 + .01, W / 2 - .01):
        for z in (-D / 2 + .01, D / 2 - .01):
            a.box(.02, th, .02, M['trim'], x, y0 + th / 2, z)
    a.box(W, .02, D, M['trim'], 0, y0 + .01, 0)
    a.box(W - .03, th - .08, D - .03, P['water'], 0, y0 + .02 + (th - .08) / 2, 0)
    a.box(W - .04, .05, D - .04, P['gravel'], 0, y0 + .045, 0)
    for i in range(4):
        with a.at(-.4 + i * .27 + r.random() * .1, y0 + .08, -.1 + r.random() * .15, s=(1.4, .8, 1)):
            a.ico(.05 + r.random() * .03, P['rock'], jitter=.3, seed=i + seed * 5)
    for i in range(12):
        with a.at(-.5 + r.random() * 1.0, y0 + .07, -.15 + r.random() * .12, rx=(r.random() - .5) * .4, ry=r.random() * 6, s=(.05, .18 + r.random() * .2, 1)):
            a.leaf(M['leaf'] if i % 2 else M['leaf2'])
    a.box(W + .02, .04, D + .02, M['dark'], 0, y0 + th + .02, 0)
    a.box(W - .05, .01, .01, M['neon'], 0, y0 + th - .005, D / 2 - .02)
    a.cyl(.015, .015, .04, M['trim'], .45, y0 + .09, -.15, 8)
    a.box(.12, .04, .002, M['note'], -.45, ch - .1, D / 2 + .002)  # feeding schedule note


def fish(a, m):
    a.sphere(.024, m, 0, 0, 0, s=(.45, 1, 1.7), seg=10, rings=6)
    a.raw([(0, 0, -.035), (0, .03, -.075), (0, .006, -.06)], [(0, 1, 2)], m)  # forked tail
    a.raw([(0, 0, -.035), (0, -.006, -.06), (0, -.03, -.075)], [(0, 1, 2)], m)
    a.raw([(0, .02, .01), (0, .04, -.015), (0, .02, -.03)], [(0, 1, 2)], m)  # dorsal
    for s in (-1, 1):
        a.raw([(s * .009, -.005, .015), (s * .028, -.014, -.002), (s * .009, -.01, 0)], [(0, 1, 2)], m)  # pectoral
        a.sphere(.004, P['eye'], s * .009, .006, .025, seg=6, rings=4)


def bubbles(a, height, seed=1):
    r = random.Random(seed)
    y = 0.0
    while y < height:
        a.ico(.006 + r.random() * .006, P['bubble'], (r.random() - .5) * .02, y, (r.random() - .5) * .02)
        y += .03 + r.random() * .05


JT_R, JT_H, JT_B = .3, 1.6, .25


def jelly_tank(a):
    R, Ht, base = JT_R, JT_H, JT_B
    a.cyl(R + .06, R + .08, base, M['dark'], 0, base / 2, 0, 32)
    with a.at(0, base - .02, 0, rx=math.pi / 2):
        a.torus(R + .065, .012, M['neon'], 32, 6)
    a.cyl(R, R, Ht, P['water'], 0, base + Ht / 2, 0, 32)
    a.cyl(R + .06, R + .06, .15, M['dark'], 0, base + Ht + .075, 0, 32)
    with a.at(0, base + Ht + .01, 0, rx=math.pi / 2):
        a.torus(R + .065, .012, M['neon'], 32, 6)
    for k in range(3):
        ang = k * 2 * math.pi / 3 + .3
        a.box(.03, Ht, .03, M['trim'], (R + .02) * math.cos(ang), base + Ht / 2, (R + .02) * math.sin(ang))
    for s in (-1, 1):
        a.cable([(s * .15, base + Ht + .15, 0), (s * .25, base + Ht + .4, -.1), (s * .2, base + Ht + .7, -.3)], .02, M['dark'])
    with a.at(0, .004, 0, rx=math.pi / 2):
        a.torus(.65, .008, M['neon2'], 32, 4)  # floor ring
    a.box(.2, .1, .004, M['dark'], 0, .12, R + .07)
    a.box(.15, .02, .002, M['led'], 0, .14, R + .073)


def jelly(a, seed):
    """Open bell with a rippled rim, four gonads and four ruffled oral arms.
    The tentacles are separate nodes (jelly_tentacle) so they sway on their own."""
    r = random.Random(seed)
    seg, rings, R, Hb = 16, 5, .07, .045
    pts, faces = [(0, Hb, 0)], []
    for i in range(1, rings + 1):
        th = math.pi / 2 * 1.1 * i / rings  # just past the equator: the rim flares down
        for k in range(seg):
            ph = 2 * math.pi * k / seg
            rr = R * math.sin(th) * (1 + (.07 * math.sin(ph * 9) if i == rings else 0))
            pts.append((rr * math.cos(ph), Hb * math.cos(th), rr * math.sin(ph)))
    faces += [(0, 1 + (k + 1) % seg, 1 + k) for k in range(seg)]
    for i in range(rings - 1):
        for k in range(seg):
            a0, a1 = 1 + i * seg + k, 1 + i * seg + (k + 1) % seg
            faces.append((a0, a1, a1 + seg, a0 + seg))
    a.raw(pts, faces, P['jelly'], smooth=True)
    for k in range(4):
        ang = k * math.pi / 2 + math.pi / 4
        a.sphere(.012, P['jellyCore'], .02 * math.cos(ang), .018, .02 * math.sin(ang), s=(1, .6, 1), seg=6, rings=3)
        x, z = .008 * math.cos(ang), .008 * math.sin(ang)
        L = .13 + r.random() * .05
        a.cable([(x, 0, z), (x * 2 + .01 * math.sin(ang * 3), -L * .3, z * 2), (x * 1.5, -L * .6, z * 2.5 + .008), (x * 2.5, -L, z * 1.5)],
                .004, P['jelly'], seg=3)


def jelly_tentacle(a, ang, seed):
    """One tentacle hanging from its root on the rim, wavy, drifting outward."""
    r = random.Random(seed)
    L, n, ph = .2 + r.random() * .14, 7, r.random() * 6
    ox, oz = math.cos(ang), math.sin(ang)
    pts = []
    for j in range(n):
        f = j / (n - 1)
        wave = math.sin(f * 7 + ph) * .012 * f
        pts.append((ox * (.01 * f + wave), -L * f, oz * (.01 * f + wave) + math.cos(f * 5 + ph) * .006 * f))
    a.cable(pts, .0016, P['tentacle'], seg=3)


def terrarium(a):
    W, Hh, D = .6, .4, .4
    for x in (-W / 2, W / 2):
        for z in (-D / 2, D / 2):
            a.box(.015, Hh, .015, M['trim'], x, Hh / 2, z)
    a.box(W, .015, D, M['trim'], 0, .0075, 0)
    a.box(W, .015, D, M['dark'], 0, Hh, 0)
    for z in (-D / 2, D / 2):
        a.box(W - .015, Hh - .015, .004, M['glass'], 0, Hh / 2, z)
    for x in (-W / 2, W / 2):
        a.box(.004, Hh - .015, D - .015, M['glass'], x, Hh / 2, 0)
    a.box(W - .02, .04, D - .02, P['sand'], 0, .035, 0)
    with a.at(.1, .06, -.05, s=(1.4, .7, 1)):
        a.ico(.08, P['rock'], jitter=.25, seed=3)
    a.cable([(-.27, .05, .12), (-.1, .15, 0), (.05, .25, -.08), (.26, .3, -.15)], .014, P['wood'])
    a.cyl(.05, .045, .02, M['trim'], -.18, .065, .1, 12)
    a.cyl(.042, .042, .002, P['water'], -.18, .075, .1, 12)
    with a.at(-.2, .055, -.12, s=.8):
        plant(a, False, 21)
    a.cyl(.03, .07, .08, M['dark'], .15, Hh + .05, 0, 16)
    a.cyl(.06, .06, .004, M['warm'], .15, Hh + .012, 0, 16)


# ---------- pet stuff ----------
def pet_bowls(a):
    r = random.Random(1)
    a.box(.42, .005, .26, M['orange'], 0, .0025, 0)
    for x, food in ((-.1, True), (.1, False)):
        a.cyl(.07, .055, .045, M['trim'], x, .0275, 0, 16)
        if food:
            for i in range(10):
                a.ico(.009, P['kibble'], x + (r.random() - .5) * .08, .045, (r.random() - .5) * .08)
        else:
            a.cyl(.06, .06, .002, P['water'], x, .04, 0, 16)


def cat_tree(a):
    """Cat tree: dark painted posts wrapped in worn rope, charcoal platforms,
    a cubby, small neon edges on the platforms."""
    a.box(.55, .04, .55, M['dark'], 0, .02, 0)
    a.box(.36, .3, .36, M['cushion'], -.05, .19, -.05)
    a.cyl(.1, .1, .002, M['dark'], -.05, .19, .131, 16, rx=math.pi / 2)
    for x, z, h, y in ((.15, .14, .7, .38), (-.12, -.12, .65, .66)):
        a.cyl(.05, .05, h, M['body'], x, y, z, 12)
        a.cyl(.056, .056, h * .7, M['cardboard'], x, y, z, 12)    # the rope wrap, worn
    a.cyl(.21, .21, .035, M['cushion'], .12, .74, .1, 20)
    a.cyl(.2, .2, .035, M['cushion'], -.1, 1.0, -.1, 20)
    for x, y, z, rr in ((.12, .74, .1, .215), (-.1, 1.0, -.1, .205)):
        with a.at(x, y - .019, z, rx=math.pi / 2):
            a.torus(rr, .003, M['neon2'], seg=40, tseg=6)
    a.cable([(.3, .74, .2), (.31, .6, .22), (.3, .5, .2)], .003, M['dark'], seg=4)
    a.ico(.025, M['red'], .3, .48, .2, .3, 4)


pets = {}


def pet(name, root_fn, parts=(), col=None):
    a = Asset(name)
    root_fn(a)
    root = pets[name] = a.build()
    for node, fn, pivot, parent in parts:
        part(root if parent is None else bpy.data.objects[parent], node, fn, pivot)
    add_colliders(root, col)
    return root


pets['Pet_Cat_Sitting'] = cat_sitting()
pet('Pet_Cat_Curled', cat_curled_cushion)
attach(cat_curled(), pets['Pet_Cat_Curled'], (0, .09, 0))  # sinks 3 cm into the cushion
pet('Pet_Dog_Bed', dog_bed)
attach(dog(), pets['Pet_Dog_Bed'], (0, .06, 0))
add_colliders(pets['Pet_Dog_Bed'])
pet('Pet_Parrot_Perch', perch, col=[(.44, .05, .44, 0, .025, 0), (.06, 1.3, .06, 0, .65, 0)])
attach(parrot(), pets['Pet_Parrot_Perch'], (0, 1.284, 0))
fr = random.Random(9)
fish_parts = []
for i in range(6):
    pos = (-.45 + fr.random() * .9, AQ_C + .14 + fr.random() * .28, -.12 + fr.random() * .24)
    fish_parts.append((f'Fish_{i + 1}', lambda a, i=i: fish(a, P['fishO'] if i % 2 else P['fishB']), pos, None))
fish_parts.append(('Aquarium_Bubbles', lambda a: bubbles(a, AQ_T - .2), (.45, AQ_C + .12, -.15), None))
aq = pet('Pet_Aquarium', aquarium, fish_parts, col='aabb')
for i in range(6):
    bpy.data.objects[f'Fish_{i + 1}'].rotation_euler.z = fr.random() * math.tau  # Blender Z = three.js Y
jelly_parts = []
for i, p in enumerate([(.08, JT_B + 1.3, .05), (-.1, JT_B + .95, -.06), (.05, JT_B + .62, -.1)]):
    jelly_parts.append((f'Jelly_{i + 1}', lambda a, i=i: jelly(a, i), p, None))
    for k in range(8):
        ang = k * 2 * math.pi / 8 + i * .4
        root = (.064 * math.cos(ang), -.004, .064 * math.sin(ang))
        jelly_parts.append((f'Jelly_{i + 1}_T{k + 1}', lambda a, ang=ang, i=i, k=k: jelly_tentacle(a, ang, i * 10 + k), root, f'Jelly_{i + 1}'))
pet('Pet_JellyfishTank', jelly_tank,
    jelly_parts + [('JellyTank_Bubbles', lambda a: bubbles(a, JT_H - .1, 3), (-.18, JT_B + .05, .1), None)], col='aabb')
# roaming pets (kit.js createRoamer / createFlyer) and the furniture they use
pets['Pet_Cat_Walk'] = cat_walk()
pets['Pet_Dog_Walk'] = dog_walk()
pets['Pet_Dog_Lying'] = dog('DogLie', 'Pet_Dog_Lying')
pets['Pet_Parrot_Fly'] = parrot('ParrotFly', 'Pet_Parrot_Fly', folded=False)
pet('Furn_DogBed', dog_bed)  # no collider: the dog walks onto it
pet('Furn_ParrotPerch', perch, col=[(.44, .05, .44, 0, .025, 0), (.06, 1.3, .06, 0, .65, 0)])
pet('Pet_Gecko_Terrarium', terrarium)
attach(gecko(), pets['Pet_Gecko_Terrarium'], (.1, .118, -.05))
pet('Deco_PetBowls', pet_bowls)
pet('Furn_CatTree', cat_tree, col='aabb')

quad_uvs(pets)
export_all(pets)
layout(pets, y0=-32)
print('built', len(pets), 'pet assets ->', OUT)
for o in pets.values():
    tris = sum(len(p.vertices) - 2 for c in [o, *o.children_recursive] if '_COL' not in c.name for p in c.data.polygons)
    print(f'{o.name}: {tris} tris, nodes {[c.name for c in o.children_recursive]}')
