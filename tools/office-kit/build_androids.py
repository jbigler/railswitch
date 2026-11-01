# Androids: the agent's partner and the wanderers. Hard-surface bipeds with
# Head, Arm_L/R and Leg_L/R nodes (pivots at the neck, shoulders and hips) for
# kit.js: the legs and arms walk from the root's gait and phase, the agent's
# arms type while it works. exec build_kit.py first, in the same globals.
use_collection('CyberOfficeAndroids')

AM = dict(
    white=mat('RS_Android_Shell_White', '#e3e7ee', .35, .35),
    black=mat('RS_Android_Shell_Black', '#1d2028', .55, .3),
    grey=mat('RS_Android_Shell_Grey', '#8d94a1', .55, .35),
    joint=mat('RS_Android_Joint', '#15171c', .6, .45),
    orange=mat('RS_Android_Trim_Orange', '#e0661c', .3, .4),
    gold=mat('RS_Android_Trim_Gold', '#c9a227', .8, .3),
    teal=mat('RS_Android_Trim_Teal', '#1fb5a8', .3, .4),
    # the agent's visor and status ring: the page recolours them by the agent's state
    agentVisor=mat('RS_Android_Visor_Agent', emit='#56627a', strength=3),
    status=mat('RS_Android_Status', emit='#56627a', strength=3),
)
HIP, SHOULDER, NECK = .92, 1.42, 1.53


def body(a, shell, trim):
    """Pelvis, torso, yoke and neck; origin at the floor."""
    a.box(.3, .16, .2, AM['joint'], 0, HIP + .03, 0)
    a.cyl(.19, .15, .5, shell, 0, 1.2, 0, 16)
    a.box(.26, .18, .03, trim, 0, 1.3, .16)
    a.box(.2, .012, .01, M['neon'], 0, 1.215, .17)  # chest stripe: the floor's colour
    a.box(.18, .22, .07, shell, 0, 1.25, -.17)      # back pack
    a.box(.06, .012, .01, M['led'], .05, 1.33, -.21)
    a.cyl(.2, .2, .04, AM['joint'], 0, 1.45, 0, 16)
    a.cyl(.05, .05, .1, AM['joint'], 0, NECK - .02, 0, 10)
    for s in (-1, 1):
        a.sphere(.065, AM['joint'], s * .24, SHOULDER, 0, seg=10, rings=6)
        a.sphere(.07, AM['joint'], s * .1, HIP, 0, seg=10, rings=6)


def head(a, shell, visor):
    a.sphere(.12, shell, 0, .1, 0, s=(1, 1.12, 1.05), seg=16, rings=10)
    a.sphere(.112, visor, 0, .105, .022, s=(1.0, .36, 1.0), seg=16, rings=6)  # visor band across the face
    for s in (-1, 1):
        a.cyl(.035, .035, .03, AM['joint'], s * .12, .1, 0, 12, rz=math.pi / 2)
    a.cyl(.006, .006, .12, AM['joint'], .07, .25, -.02, 6)
    a.sphere(.015, visor, .07, .31, -.02, seg=8, rings=6)


def arm(a, s, shell, trim):
    a.cyl(.045, .04, .28, shell, s * .02, -.16, 0, 12)
    a.sphere(.045, AM['joint'], s * .02, -.31, 0, seg=10, rings=6)
    with a.at(s * .02, -.31, 0, rx=-.25):  # forearm a little forward
        a.cyl(.04, .035, .26, shell, 0, -.14, 0, 12)
        a.cyl(.043, .043, .03, trim, 0, -.05, 0, 12)
        a.sphere(.045, AM['joint'], 0, -.3, 0, s=(.8, 1.1, .6), seg=10, rings=6)


def leg(a, shell, trim):
    a.cyl(.07, .055, .42, shell, 0, -.22, 0, 12)
    a.sphere(.055, AM['joint'], 0, -.45, 0, seg=10, rings=6)
    a.cyl(.058, .058, .04, trim, 0, -.55, 0, 12)
    a.cyl(.055, .045, .4, shell, 0, -.66, 0, 12)
    a.box(.1, .06, .2, AM['joint'], 0, -.89, .04)  # foot: its sole on the floor


def android(name, prefix, shell, trim, visor, ring=None):
    a = Asset(name)
    body(a, shell, trim)
    if ring:
        with a.at(0, .02, 0, rx=math.pi / 2):
            a.torus(.42, .02, ring, 40, 6)
    root = a.build()
    part(root, f'{prefix}_Head', lambda h: head(h, shell, visor), (0, NECK, 0))
    for s, side in ((1, 'L'), (-1, 'R')):  # _L at +x
        part(root, f'{prefix}_Arm_{side}', lambda h, s=s: arm(h, s, shell, trim), (s * .24, SHOULDER, 0))
        part(root, f'{prefix}_Leg_{side}', lambda h: leg(h, shell, trim), (s * .1, HIP, 0))
    return root


androids = {
    'Char_Android_Agent': android('Char_Android_Agent', 'Agent', AM['white'], M['neon2'], AM['agentVisor'], AM['status']),
    'Char_Android_A': android('Char_Android_A', 'AndroidA', AM['white'], AM['orange'], M['led']),
    'Char_Android_B': android('Char_Android_B', 'AndroidB', AM['black'], AM['gold'], M['led']),
    'Char_Android_C': android('Char_Android_C', 'AndroidC', AM['grey'], AM['teal'], M['led']),
}
export_all(androids)
layout(androids, y0=-40)
print('built', len(androids), 'androids ->', OUT)
for o in androids.values():
    tris = sum(len(p.vertices) - 2 for c in [o, *o.children_recursive] for p in c.data.polygons)
    print(f'{o.name}: {tris} tris, nodes {[c.name for c in o.children_recursive]}')
