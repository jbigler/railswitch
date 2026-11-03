# A Blender scene to model a new Virtual Office set in (D153,
# docs/office-kit.md): per piece of kit/pieces.json, a wireframe box from its
# min to its max and an empty <piece>_PT_<point> per point, each piece in its
# category's collection (Arch, Furn, Tech, ...), its origin at the world's.
#   blender --background --python tools/office-kit/stub_set.py -- <out.blend>
import json
import os
import sys

import bpy

here = os.path.dirname(os.path.abspath(__file__))
src = os.path.join(here, '..', '..', 'internal', 'web', 'static', 'office', 'kit', 'pieces.json')
out = sys.argv[sys.argv.index('--') + 1] if '--' in sys.argv else sys.exit('usage: blender --background --python stub_set.py -- <out.blend>')
pieces = json.load(open(src))['pieces']


def to_bl(x, y, z):  # three.js (Y up, front +Z) -> Blender (Z up, front -Y)
    return (x, -z, y)


bpy.ops.wm.read_factory_settings(use_empty=True)
cols = {}
for name, p in sorted(pieces.items()):
    cat = name.split('_', 1)[0]
    if cat not in cols:
        cols[cat] = bpy.data.collections.new(cat)
        bpy.context.scene.collection.children.link(cols[cat])
    (x0, y0, z0), (x1, y1, z1) = p['min'], p['max']
    corners = [to_bl(x, y, z) for x in (x0, x1) for y in (y0, y1) for z in (z0, z1)]
    edges = [(a, b) for a in range(8) for b in range(a + 1, 8) if bin(a ^ b).count('1') == 1]
    me = bpy.data.meshes.new(name)
    me.from_pydata(corners, edges, [])
    box = bpy.data.objects.new(name + '_Box', me)
    box.display_type = 'WIRE'
    cols[cat].objects.link(box)
    for pt, xyz in p.get('points', {}).items():
        e = bpy.data.objects.new(f'{name}_PT_{pt}', None)
        e.empty_display_type = 'PLAIN_AXES'
        e.empty_display_size = 0.1
        e.location = to_bl(*xyz)
        e.parent = box
        cols[cat].objects.link(e)
    box.hide_set(True)  # show one piece at a time from the outliner
bpy.ops.wm.save_as_mainfile(filepath=os.path.abspath(out))
print('stub_set:', len(pieces), 'pieces ->', out)
