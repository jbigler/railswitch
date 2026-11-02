# Glass partitions: floor-to-ceiling panes in thin dark frames, for the glass
# rooms (server room, meeting room, lounge). exec build_kit.py first, in the
# same globals. Big panes, few frames: a frame only at each end, where two
# panes meet, and along the floor and ceiling. Freestanding: centred on their
# line (z = 0), seen from both sides, origin on the floor at the centre.
use_collection('CyberOfficeGlass')

GH = 4.0      # floor to ceiling, as the walls
BAR = 0.05    # frame width
DEEP = 0.07   # frame depth
PANE = 0.012  # glass thickness

M['partition'] = mat('RS_GlassPartition', '#2c3c4a', 0, .04, alpha=.08)  # a dark tint: lit, a pale one reads as a blue panel
M['frame'] = mat('RS_Frame', '#14161b', .75, .35)


def glass_run(a, length, x0=None):
    """Panes 2 m apart (or one for 2 m), framed at both ends and between."""
    x0 = -length / 2 if x0 is None else x0
    n = max(1, round(length / 2))
    w = length / n
    for i in range(n + 1):  # posts
        a.box(BAR, GH, DEEP, M['frame'], x0 + i * w, GH / 2, 0)
    a.box(length, BAR, DEEP, M['frame'], x0 + length / 2, BAR / 2, 0)        # sill
    a.box(length, BAR, DEEP, M['frame'], x0 + length / 2, GH - BAR / 2, 0)   # head
    for i in range(n):
        a.box(w - BAR, GH - 2 * BAR, PANE, M['partition'], x0 + i * w + w / 2, GH / 2, 0)
    for s in (-1, 1):  # a thin neon line along the floor, both sides
        a.box(length, .008, .006, M['neon'], x0 + length / 2, BAR + .004, s * (DEEP / 2 + .003))


def glass_wall(length):
    def build(a):
        glass_run(a, length)
    return build


DOOR_W = 2.0 - BAR  # fills the opening


def glass_door_leaf(a):
    """The sliding leaf, drawn around its own centre: a pane in a frame and a
    vertical pull bar."""
    h = GH - 2 * BAR - .6  # a transom above it
    a.box(DOOR_W, h, PANE * 1.5, M['partition'], 0, h / 2, 0)
    for x in (-DOOR_W / 2 + BAR / 2, DOOR_W / 2 - BAR / 2):
        a.box(BAR, h, DEEP * .6, M['frame'], x, h / 2, 0)
    a.box(DOOR_W, BAR, DEEP * .6, M['frame'], 0, h - BAR / 2, 0)
    a.box(.025, .9, .04, M['frame'], DOOR_W / 2 - .18, 1.05, .05)
    a.box(.025, .9, .04, M['frame'], DOOR_W / 2 - .18, 1.05, -.05)


def glass_wall_door(a):
    """4 m: a fixed 2 m pane, then the door opening (the leaf is a child
    node, GlassDoor, that slides over the fixed pane), a transom above."""
    glass_run(a, 2.0, x0=-2.0)
    x1 = 2.0
    a.box(BAR, GH, DEEP, M['frame'], x1, GH / 2, 0)                     # far post
    a.box(2.0, BAR, DEEP, M['frame'], 1.0, GH - BAR / 2, 0)              # head
    top = GH - BAR - .6
    a.box(2.0, BAR, DEEP, M['frame'], 1.0, top, 0)                       # transom bar
    a.box(2.0 - BAR, .6 - BAR, PANE, M['partition'], 1.0, top + .3, 0)   # transom glass
    for s in (-1, 1):
        a.box(2.0, .008, .006, M['neon'], 1.0, .004, s * (DEEP / 2 + .003))


objs = {}
for name, fn in (('Arch_GlassWall_4m', glass_wall(4.0)), ('Arch_GlassWall_2m', glass_wall(2.0))):
    a = Asset(name)
    fn(a)
    objs[name] = a.build()
    add_colliders(objs[name], [(float(name[-2]), GH, .1, 0, GH / 2, 0)])

a = Asset('Arch_GlassWall_Door_4m')
glass_wall_door(a)
root = a.build()
objs['Arch_GlassWall_Door_4m'] = root
add_colliders(root, [(2.0, GH, .1, -1.0, GH / 2, 0), (2.0, .6, .1, 1.0, GH - .3, 0)])
# the leaf, closed in the opening; kit.js slides it toward -x to open
leaf = part(root, 'GlassDoor', glass_door_leaf, pivot=(1.0, BAR, DEEP / 2 + .02))  # in front of the fixed pane, so it slides past it
add_colliders(leaf, [(DOOR_W, GH - 2 * BAR - .6, .1, 0, (GH - 2 * BAR - .6) / 2, 0)])

export_all(objs)
print('glass:', ', '.join(objs))
