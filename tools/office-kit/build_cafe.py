# Café and game room set: vending machines, the coffee counter, a bartop
# arcade on its stand, the wall clock. exec build_kit.py first, in the same
# globals. Whatever shows text or moves (menus, the arcade's screen, the
# clock's hands) is drawn or turned by the page: the models carry blank,
# named surfaces (RS_VendScreen_*, RS_ArcadeScreen) and named hand nodes.
use_collection('CyberOfficeCafe')

M['vend'] = mat('RS_VendScreen', '#000000', 0, .3, emit='#1a1030', strength=1)   # blank: the page draws the menu
M['arcadeScreen'] = mat('RS_ArcadeScreen', '#000000', 0, .2, emit='#06120a', strength=1)
M['steel'] = mat('RS_Steel', '#8a9099', .9, .3)
M['cup'] = mat('RS_Cup', '#d8d4cc', 0, .5)
M['clockFace'] = mat('RS_ClockFace', '#0b0d12', .2, .5)
M['hand'] = mat('RS_ClockHand', '#d9e4f0', .3, .4)
M['orange'] = mat('RS_Glow_Orange', emit='#ff7a1a', strength=4)


def vending(a, seed=1):
    """A vending machine, 1.0 × 1.9 × 0.8 m, in worn violet paint: a big menu
    screen (RS_VendScreen, 0.78 × 0.95 m, its centre 1.32 m up), a card
    reader, a dispenser slot, a cup holder, side vents, a dark lower
    cabinet with scuffed panels, a mount for the page's header sign on top."""
    w, h, d = 1.0, 1.9, .8
    zf = d / 2
    a.box(w, h, d, M['violet'], 0, h / 2, 0)
    a.box(w + .02, .08, d + .02, M['dark'], 0, .04, 0)                    # foot
    a.box(w - .08, .8, .02, M['dark'], 0, .5, zf)                         # lower cabinet face
    for k in range(5):
        a.box(.5, .016, .01, M['trim'], -.12, .32 + k * .04, zf + .012)   # its vent
    a.box(.88, 1.06, .03, M['dark'], 0, 1.33, zf)                         # screen bezel
    a.plane(.78, .95, M['vend'], 0, 1.32, zf + .017)
    a.box(.88, .006, .006, M['neon2'], 0, 1.87, zf + .01)                 # edge light
    a.box(.14, .2, .04, M['dark'], .3, .95, zf + .02)                     # card reader
    a.box(.08, .05, .005, M['led'], .3, 1.0, zf + .042)
    a.box(.4, .14, .05, M['trim'], -.15, .9, zf + .02)                    # dispenser slot
    a.box(.36, .1, .004, M['dark'], -.15, .9, zf + .046)
    a.cyl(.04, .03, .08, M['red'], -.42, 1.0, zf + .04, 12)               # a cup in the holder
    for s_ in (-1, 1):                                                    # side vents
        for k in range(6):
            a.box(.006, .015, .28, M['dark'], s_ * (w / 2 + .003), 1.55 + k * .035, .05)
    a.box(.7, .05, .12, M['dark'], 0, h + .025, zf - .1)                  # header-sign mount


def coffee_counter(a):
    """Café counter, 2.0 × 0.95 × 0.6 m: worn dark paint, a steel top, an
    espresso machine with two group heads and a lit display, a grinder,
    stacked cups."""
    a.box(2.0, .9, .6, M['dark'], 0, .45, 0)
    a.box(2.04, .04, .64, M['steel'], 0, .92, 0)
    a.box(1.9, .006, .006, M['neon2'], 0, .1, .302)
    ex, ey = -.35, .94                                                    # espresso machine
    a.box(.62, .42, .42, M['body'], ex, ey + .21, -.04)
    a.box(.62, .03, .42, M['steel'], ex, ey + .435, -.04)
    for gx in (-.15, .15):
        a.cyl(.04, .04, .06, M['steel'], ex + gx, ey + .26, .2, 12)
        a.box(.03, .02, .16, M['dark'], ex + gx, ey + .22, .27)           # portafilter handles
    a.box(.2, .07, .005, M['led'], ex, ey + .36, .175)                    # display
    a.box(.5, .02, .2, M['steel'], ex, ey + .02, .12)                     # drip tray
    a.box(.16, .3, .16, M['body'], .3, ey + .15, -.1)                     # grinder
    a.cyl(.07, .04, .16, M['glass'], .3, ey + .38, -.1, 12)               # its hopper
    for k in range(4):                                                    # stacked cups
        a.cyl(.04, .03, .07, M['cup'], .65, ey + .035 + k * .06, .05, 12)


def arcade_stand(a):
    """The bartop's stand: a pedestal 0.75 m high, worn paint, a neon foot."""
    a.box(.5, .75, .42, M['dark'], 0, .375, 0)
    a.box(.62, .05, .52, M['body'], 0, .025, 0)
    a.box(.58, .006, .006, M['neon2'], 0, .055, .26)
    a.box(.66, .03, .56, M['body'], 0, .765, 0)                           # its top


def arcade_bartop(a):
    """Bartop arcade, 0.62 × 0.62 × 0.55 m, after the reference: a chunky
    bolted cabinet, an angled screen (RS_ArcadeScreen, 0.4 × 0.32 m: the
    page draws it), a control deck with a joystick and six buttons, violet
    side panels with a glowing sunset disc, a cartridge on top, a grille.
    Origin at its base (it sits on the stand, 0.78 m up)."""
    a.box(.62, .16, .55, M['body'], 0, .08, 0)                            # base
    a.box(.62, .05, .3, M['dark'], 0, .18, .12, rx=-.12)                  # control deck
    a.cyl(.012, .012, .08, M['trim'], -.15, .25, .14, 8)                  # joystick
    a.sphere(.035, M['neon2'], -.15, .3, .14)
    for k, (bx, bz) in enumerate([(.04, .1), (.11, .1), (.18, .1), (.04, .18), (.11, .18), (.18, .18)]):
        a.cyl(.02, .02, .02, M['neon2'], bx, .21, bz, 12)
    with a.at(0, .4, -.05, rx=-.3):                                      # screen housing, leaning back
        a.box(.56, .42, .14, M['body'], 0, 0, 0)
        a.box(.48, .36, .02, M['dark'], 0, 0, .07)
        a.plane(.4, .32, M['arcadeScreen'], 0, 0, .082)
        for bx in (-.25, .25):
            for by in (-.18, .18):
                a.cyl(.01, .01, .01, M['trim'], bx, by, .075, 8, rx=math.pi / 2)
    for s_ in (-1, 1):                                                    # side art
        a.box(.012, .5, .5, M['violet'], s_ * .316, .3, 0)
        a.cyl(.09, .09, .004, M['orange'], s_ * .323, .32, .02, 24, rz=math.pi / 2)
        for k in range(3):
            a.box(.005, .012, .2, M['violet'], s_ * .326, .27 + k * .03, .02)
    a.box(.2, .1, .03, M['violet'], 0, .66, -.18)                         # cartridge on top
    a.box(.16, .05, .004, M['led'], 0, .67, -.164)
    for k in range(5):                                                    # grille under the screen
        a.box(.3, .01, .004, M['dark'], 0, .07 + k * .02, .277)


def clock_face(a):
    """Analog wall clock, 0.5 m across, back on the wall (origin at its
    centre): a dark face, twelve ticks, a neon ring. The hands are child
    nodes Clock_Hour, Clock_Minute and Clock_Second, turned by the page
    (about their local Z)."""
    a.cyl(.25, .25, .04, M['body'], 0, 0, .02, 48, rx=math.pi / 2)
    a.cyl(.23, .23, .005, M['clockFace'], 0, 0, .042, 48, rx=math.pi / 2)
    a.torus(.24, .008, M['neon'])
    for k in range(12):
        t = k * math.pi / 6
        long_ = k % 3 == 0
        a.box(.012 if long_ else .006, .04 if long_ else .025, .004, M['hand'], math.sin(t) * .2, math.cos(t) * .2, .046, rz=-t)


def hand(length, width, m):
    def draw(a):
        a.box(width, length, .004, m, 0, length / 2 - .02, 0)
    return draw


objs = {}
for name, fn, col in [
    ('Tech_Vending_Ramen', lambda a: vending(a, 1), [(1.0, 1.9, .8, 0, .95, 0)]),
    ('Tech_Vending_Drinks', lambda a: vending(a, 2), [(1.0, 1.9, .8, 0, .95, 0)]),
    ('Furn_CoffeeCounter', coffee_counter, [(2.0, .95, .6, 0, .475, 0)]),
    ('Furn_Arcade_Stand', arcade_stand, [(.62, .78, .52, 0, .39, 0)]),
    ('Tech_Arcade_Bartop', arcade_bartop, None),
]:
    a = Asset(name)
    fn(a)
    objs[name] = a.build()
    add_colliders(objs[name], col)

a = Asset('Deco_WallClock')
clock_face(a)
clock = a.build()
objs['Deco_WallClock'] = clock
part(clock, 'Clock_Hour', hand(.13, .014, M['hand']), pivot=(0, 0, .05))
part(clock, 'Clock_Minute', hand(.19, .009, M['hand']), pivot=(0, 0, .054))
part(clock, 'Clock_Second', hand(.21, .004, M['led']), pivot=(0, 0, .058))

quad_uvs(objs, prefixes=('RS_VendScreen', 'RS_ArcadeScreen'))
export_all(objs)
print('cafe:', ', '.join(objs))
