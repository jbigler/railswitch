# Androids: humanoid, after the references: anatomical armour plates (metaball
# shapes) over an exposed mechanical core, cables at the neck and waist,
# glowing joints, a sculpted face with glowing eyes and round ear units.
# Nodes Head, Arm_L/R, Leg_L/R (pivots at the neck, shoulders, hips), each
# limb with its lower half as a child, Forearm_L/R and Shin_L/R (pivots at
# the elbows and knees), for kit.js: a walk that bends the knees and swings
# soft elbows, from the root's gait and phase; the agent's arms type while it
# works, and its legs fold when it sits. exec build_kit.py first, in the same
# globals.
use_collection('CyberOfficeAndroids')

AM = dict(
    white=mat('RS_Android_Shell_White', '#e6e8ec', .35, .22),
    ivory=mat('RS_Android_Shell_Ivory', '#ddd4bf', .1, .45),
    black=mat('RS_Android_Shell_Black', '#1e2026', .9, .2),
    silver=mat('RS_Android_Shell_Silver', '#b9bfc9', 1.0, .16),
    core=mat('RS_Android_Core', '#15171c', .8, .35),
    coreBlue=mat('RS_Android_Core_Blue', '#24407c', .6, .4),
    cable=mat('RS_Android_Cable', '#0e0f12', .4, .5),
    teal=mat('RS_Android_Glow_Teal', emit='#2fe0c0', strength=4),
    # the agent's eyes and status ring: the page recolours them by the agent's state
    agentVisor=mat('RS_Android_Visor_Agent', emit='#56627a', strength=3),
    status=mat('RS_Android_Status', emit='#56627a', strength=3),
)
NECK, SHOULDER, HIP = 1.6, 1.45, .93


def joint(a, core, glow, x, y, z, r, axis='x'):
    """A mechanical joint: a dark ball with a glowing ring around it."""
    a.sphere(r, core, x, y, z, seg=8, rings=5)
    with a.at(x, y, z, rz=math.pi / 2 if axis == 'x' else 0, rx=math.pi / 2 if axis == 'y' else 0):
        a.torus(r * 1.02, r * .18, glow, 14, 4)


def capsule(a, m, p0, p1, r, seg=8):
    """A rod with rounded ends between two points (fingers, struts)."""
    P0, P1 = Vector(p0), Vector(p1)
    d = P1 - P0
    mid = (P0 + P1) / 2
    q = Vector((0, 1, 0)).rotation_difference(d.normalized()).to_euler('ZYX')  # Blender ZYX = three.js XYZ, what a.at takes
    with a.at(*mid, rx=q.x, ry=q.y, rz=q.z):
        a.cyl(r, r, d.length, m, 0, 0, 0, seg)
    a.sphere(r, m, *P0, seg=seg, rings=4)
    a.sphere(r, m, *P1, seg=seg, rings=4)


def torso(name, shell, core, glow, cable):
    """One smooth athletic torso, after the references: chest and pectorals,
    a ribcage tapering to a narrow waist, hips; neck, cables, a chest light."""
    a = Asset(name)
    a.cyl(.034, .03, .16, core, 0, 1.52, -.005, 8)                  # neck
    for k in range(4):                                               # neck cables
        ang = -.75 + k * .5
        x, z = .042 * math.sin(ang), .042 * math.cos(ang)
        a.cable([(x * 1.6, 1.44, z * 1.2), (x * 1.1, 1.52, z * 1.02), (x, 1.6, z)], .006, cable, seg=4)
    a.sphere(.018, glow, 0, 1.31, .118, s=(1, 1, .5), seg=8, rings=5)   # the chest light
    for y in (1.13, 1.06):                                             # abdominal seams
        a.box(.12, .005, .006, core, 0, y, .093)
    a.box(.005, .12, .006, core, 0, 1.095, .095)
    for s in (-1, 1):
        a.box(.004, .2, .004, glow, s * .12, 1.27, .055, rz=s * .12)   # side lights
    body = meta(name + '_Body', [
        ('e', (0, 1.33, 0), .16, (1.05, 1.0, .62)),                     # chest
        ('e', (.065, 1.36, .055), .075, (1.1, .75, .6)), ('e', (-.065, 1.36, .055), .075, (1.1, .75, .6)),   # pectorals
        ('e', (.09, 1.455, -.02), .06, (1.4, .6, .9)), ('e', (-.09, 1.455, -.02), .06, (1.4, .6, .9)),       # trapezius
        ('e', (0, 1.2, 0), .13, (1.0, 1.0, .62)),                      # ribs, tapering
        ('e', (0, 1.08, 0), .1, (1.05, 1.2, .75)),                     # waist
        ('e', (0, 1.13, .065), .055, (.95, 1.7, .45)),                 # abdominals
        ('e', (0, .97, 0), .13, (1.1, .7, .8)),                        # hips
        ('e', (0, .95, -.06), .09, (1.35, .8, .8))], .007, [shell], target=1500)   # glutes
    root = a.build()
    join(root, [body])
    return root


def head_fn(name, shell, core, eyes, glow):
    a = Asset(name)
    a.cyl(.036, .036, .1, core, 0, -.02, -.005, 10)
    for s in (-1, 1):
        a.sphere(.013, eyes, s * .031, .1, .088, s=(1.5, .6, .5), seg=8, rings=5)     # eyes
        with a.at(s * .086, .1, -.005, rz=math.pi / 2):                                  # ear units
            a.cyl(.032, .032, .016, core, 0, 0, 0, 16)
            a.torus(.026, .004, glow, 12, 3)
        a.box(.003, .05, .003, glow, s * .06, .2, .02, rz=s * .4)                        # temple seams
    face = meta(name + '_Face', [
        ('e', (0, .14, -.01), .095, (.84, 1.0, 1.05)),    # cranium
        ('e', (0, .13, -.05), .08, (.9, 1, 1)),            # back of the skull
        ('e', (0, .07, .03), .066, (.78, 1.1, .86)),       # face
        ('e', (0, .025, .04), .044, (1.05, .6, .88)),      # jaw
        ('e', (0, .002, .062), .02, (1.1, .8, .9)),        # chin
        ('e', (.042, .086, .058), .022, (1, .8, .8)), ('e', (-.042, .086, .058), .022, (1, .8, .8)),   # cheekbones
        ('e', (0, .086, .086), .012, (.6, 1.6, .9)),       # nose ridge
        ('e', (0, .12, .068), .038, (1.35, .22, .5))], .0045, [shell], target=800)                     # brow, flush
    o = a.build()
    join(o, [face])
    return o


ELBOW_Y, KNEE = -.3, (0, -.46, .025)  # the lower limbs' pivots, from the shoulder and hip


def arm_fn(name, fore_name, s, shell, core, glow):
    """Deltoid and biceps as the upper arm; a child forearm (fore_name, its
    pivot the dark elbow) tapering to simple fingers. The upper arm with its
    forearm attached."""
    a = Asset(name)
    limb = meta(name + '_Limb', [
        ('e', (s * .02, -.015, 0), .068, (1.0, 1.05, 1.05)),           # deltoid
        ('c', (s * .015, -.06, 0), (s * .008, -.27, 0), .042),         # upper arm
        ('e', (s * .004, -.15, .018), .044, (1, 1.6, 1))], .006, [shell], target=320)   # biceps
    upper = a.build()
    join(upper, [limb])
    # The forearm in its own frame: the elbow at its origin.
    f = Asset(fore_name)
    d = Vector((0, -math.cos(.2), math.sin(.2)))  # forearm, a little forward
    f.sphere(.034, core, 0, 0, 0, seg=8, rings=5)
    with f.at(0, 0, 0, rz=math.pi / 2):
        f.torus(.034, .005, glow, 14, 4)
    hand = d * .28
    for k in range(4):                                                 # fingers: simple segments
        z = hand.z - .022 + k * .015
        L = .07 - abs(k - 1.5) * .008
        f.box(.013, L, .012, core, hand.x, hand.y - .03 - L / 2, z + .005)
    f.box(.015, .05, .015, core, hand.x - s * .026, hand.y - .035, hand.z + .03, rx=.5)   # thumb
    fl = meta(fore_name + '_Limb', [
        ('c', tuple(d * .04), tuple(d * .14), .038),                  # forearm
        ('c', tuple(d * .14), tuple(d * .24), .03),                   #   tapering to the wrist
        ('e', tuple(hand), .036, (.45, 1.2, .9))], .006, [shell], target=330)       # back of the hand
    fore = f.build()
    join(fore, [fl])
    attach(fore, upper, (s * .008, ELBOW_Y, 0))
    return upper


def leg_fn(name, shin_name, shell, core, glow):
    """A full thigh tapering to the knee; a child shin (shin_name, its pivot
    the dark knee): calf, a slim ankle, a sleek foot. The thigh with its shin
    attached."""
    a = Asset(name)
    limb = meta(name + '_Limb', [
        ('c', (0, -.03, 0), (0, -.22, .01), .078),                     # upper thigh
        ('c', (0, -.22, .01), (0, -.41, .01), .06)], .007, [shell], target=380)   # lower thigh
    thigh = a.build()
    join(thigh, [limb])
    # The shin in its own frame: the knee at its origin.
    kx, ky, kz = KNEE
    b = Asset(shin_name)
    b.sphere(.04, core, 0, 0, 0, seg=8, rings=5)
    with b.at(0, 0, 0, rz=math.pi / 2):
        b.torus(.04, .005, glow, 14, 4)
    sl = meta(shin_name + '_Limb', [
        ('c', (0, -.5 - ky, -.005 - kz), (0, -.66 - ky, -.012 - kz), .052),   # calf
        ('c', (0, -.66 - ky, -.008 - kz), (0, -.85 - ky, 0 - kz), .036),      # shin to the ankle
        ('e', (0, -.9 - ky, .055 - kz), .05, (.75, .5, 2.1))], .007, [shell], target=370)   # foot
    shin = b.build()
    join(shin, [sl])
    attach(shin, thigh, KNEE)
    return thigh


def android(name, prefix, shell, core, eyes, glow, ring=None):
    root = torso(name, shell, core, glow, AM['cable'] if core is AM['core'] else AM['coreBlue'])
    if ring:
        a = Asset(name + '_Ring')
        with a.at(0, .02, 0, rx=math.pi / 2):
            a.torus(.42, .02, ring, 40, 6)
        join(root, [a.build()])
    attach(head_fn(f'{prefix}_Head', shell, core, eyes, glow), root, (0, NECK, 0))
    for s, side in ((1, 'L'), (-1, 'R')):  # _L at +x
        attach(arm_fn(f'{prefix}_Arm_{side}', f'{prefix}_Forearm_{side}', s, shell, core, glow), root, (s * .19, SHOULDER, 0))
        attach(leg_fn(f'{prefix}_Leg_{side}', f'{prefix}_Shin_{side}', shell, core, glow), root, (s * .095, HIP, 0))
    return root


androids = {
    'Char_Android_Agent': android('Char_Android_Agent', 'Agent', AM['white'], AM['core'], AM['agentVisor'], M['warm'], AM['status']),
    'Char_Android_A': android('Char_Android_A', 'AndroidA', AM['ivory'], AM['coreBlue'], M['warm'], M['led']),
    'Char_Android_B': android('Char_Android_B', 'AndroidB', AM['black'], AM['core'], M['led'], M['led']),
    'Char_Android_C': android('Char_Android_C', 'AndroidC', AM['silver'], AM['core'], AM['teal'], AM['teal']),
}
export_all(androids)
layout(androids, y0=-40)
print('built', len(androids), 'androids ->', OUT)
for o in androids.values():
    tris = sum(len(p.vertices) - 2 for c in [o, *o.children_recursive] for p in c.data.polygons)
    print(f'{o.name}: {tris} tris, nodes {[c.name for c in o.children_recursive]}')
