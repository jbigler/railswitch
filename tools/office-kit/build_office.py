# Office set: exec build_kit.py first, in the same globals.
use_collection('CyberOfficeKit')

H = 4.0  # floor-to-ceiling height for first-person scale
# name, builder, collider ('aabb', a box list, or None)
ASSETS = [
    ('Furn_Workstation_3Mon', lambda a: workstation(a, 3), 'aabb'),
    ('Furn_Workstation_2Mon', lambda a: workstation(a, 2, 1.8, seed=4), 'aabb'),
    ('Furn_Desk', desk, 'aabb'), ('Furn_Chair', chair, 'aabb'),
    ('Tech_Monitor', monitor, None), ('Tech_WallDisplay', wall_display, None), ('Tech_Keyboard', keyboard, None),
    ('Tech_PCTower', pc_tower, 'aabb'), ('Deco_Mug', mug, None),
    ('Tech_ServerRack', server_rack, [(.6, 2.0, 1.0, 0, 1.0, 0)]), ('Tech_ServerRack_B', lambda a: server_rack(a, seed=7), [(.6, 2.0, 1.0, 0, 1.0, 0)]),  # the cabinet: not its cables
    ('Furn_Sofa', sofa, 'aabb'), ('Furn_Sofa_Short', lambda a: sofa(a, 1.4), 'aabb'), ('Furn_CoffeeTable', coffee_table, 'aabb'),
    ('Deco_Plant_Floor', plant, 'aabb'), ('Deco_Plant_Floor_B', lambda a: plant(a, True, 5, M['terracotta']), 'aabb'),
    ('Deco_Plant_Small', lambda a: plant(a, False, 3), None),
    ('Deco_HangingPlanter', hanging_planter, None), ('Furn_Bookshelf', bookshelf, 'aabb'), ('Deco_NeonSign', neon_sign, None),
    ('Arch_Door', door, 'aabb'), ('Light_CeilingPanel', ceiling_panel, None),
    ('Ext_CityBackdrop', city_backdrop, None),
]
objs = {}
for name, fn, col in ASSETS:
    a = Asset(name)
    fn(a)
    objs[name] = a.build()
    add_colliders(objs[name], col)

ft = Asset('Arch_Floor_4m'); floor_tile(ft); f = ft.build(); f.data.transform(Matrix.Rotation(-math.pi / 2, 4, 'X'))
objs['Arch_Floor_4m'] = f
add_colliders(f, [(4, .1, 4, 0, -.05, 0)])

objs['Arch_Wall_Plain_4m'] = make_wall('Arch_Wall_Plain_4m', height=H)
objs['Arch_Wall_Window_4m'] = make_wall('Arch_Wall_Window_4m', height=H, windows=[(0, .9, 3.2, 2.2, .25)])
objs['Arch_Wall_Plain_2m'] = make_wall('Arch_Wall_Plain_2m', width=2.0, height=H)  # beside the elevator, on the 4 m grid
objs['Arch_Wall_Door_4m'] = make_wall('Arch_Wall_Door_4m', height=H, doors=[(0, 1.1, 2.4)])
for n in ('Arch_Wall_Plain_4m', 'Arch_Wall_Window_4m'):
    add_colliders(objs[n], [(4, H, .15, 0, H / 2, -.075)])
side = 2 - .55
add_colliders(objs['Arch_Wall_Plain_2m'], [(2, H, .15, 0, H / 2, -.075)])
add_colliders(objs['Arch_Wall_Door_4m'], [
    (side, H, .15, -(.55 + side / 2), H / 2, -.075), (side, H, .15, .55 + side / 2, H / 2, -.075),
    (1.1, H - 2.4, .15, 0, 2.4 + (H - 2.4) / 2, -.075)])

quad_uvs(objs)
export_all(objs)
layout(objs)
print('built', len(objs), 'office assets ->', OUT)
