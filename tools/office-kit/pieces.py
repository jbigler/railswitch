# Writes internal/web/static/office/kit/pieces.json, the piece list a Virtual
# Office set is checked against (D153, docs/office-kit.md): each piece's box
# from its exported glTF, its extras from its node and material names, its
# points from POINTS below. Plain Python, no Blender:
#   python3 tools/office-kit/pieces.py [<models dir>]
# build_all.py runs it last. The version goes up by one when a piece's box or
# points change, so sets built for the old list show as stale.
import json
import math
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
WEB = os.path.join(HERE, '..', '..', 'internal', 'web', 'static', 'office')
MODELS = os.path.normpath(os.path.join(WEB, 'sets', 'cyberpunk'))
OUT = os.path.normpath(os.path.join(WEB, 'kit', 'pieces.json'))

# Where the layout (rooms.js) puts things on a piece, in its own frame.
POINTS = {
    'Furn_Desk': {'top': [0, 0.75, 0]},
    'Furn_Workstation_Lived': {'top': [0, 0.75, 0]},
    'Furn_Workstation_Lived_B': {'top': [0, 0.75, 0]},
    'Furn_CoffeeCounter': {'top': [0, 0.94, 0]},
    'Furn_CoffeeTable': {'top': [0, 0.42, 0]},
    'Furn_Arcade_Stand': {'top': [0, 0.78, 0]},
    'Furn_ParrotPerch': {'bar': [0, 1.284, 0]},
    'Deco_Crate': {'top': [0, 0.46, 0]},
}

IS_COL = re.compile(r'_COL\d*$')  # kit.js's rule


def is_accent(m):  # kit.js's isAccent and isSecondary
    return m in ('RS_Neon_Accent', 'RS_LED_Accent', 'RS_Neon_Secondary') or (m.startswith('RS_Screen') and m != 'RS_ScreenOff')


def node_matrix(n):
    if 'matrix' in n:
        m = n['matrix']  # column-major
        return [[m[0], m[4], m[8], m[12]], [m[1], m[5], m[9], m[13]], [m[2], m[6], m[10], m[14]], [0, 0, 0, 1]]
    x, y, z, w = n.get('rotation', [0, 0, 0, 1])
    r = [[1 - 2 * (y * y + z * z), 2 * (x * y - z * w), 2 * (x * z + y * w)],
         [2 * (x * y + z * w), 1 - 2 * (x * x + z * z), 2 * (y * z - x * w)],
         [2 * (x * z - y * w), 2 * (y * z + x * w), 1 - 2 * (x * x + y * y)]]
    s = n.get('scale', [1, 1, 1])
    t = n.get('translation', [0, 0, 0])
    return [[r[i][0] * s[0], r[i][1] * s[1], r[i][2] * s[2], t[i]] for i in range(3)] + [[0, 0, 0, 1]]


def mul(a, b):
    return [[sum(a[i][k] * b[k][j] for k in range(4)) for j in range(4)] for i in range(4)]


def describe(g):
    """The box of every mesh but the colliders, after node transforms, and the
    extras: collider, parts (the named nodes below the root), accent."""
    lo, hi = [math.inf] * 3, [-math.inf] * 3
    parts, col = [], False

    def walk(i, m, depth):
        nonlocal col
        n = g['nodes'][i]
        m = mul(m, node_matrix(n))
        name = n.get('name', '')
        if IS_COL.search(name):
            col = True
            return
        if depth > 0 and name:
            parts.append(name)
        if 'mesh' in n:
            for p in g['meshes'][n['mesh']]['primitives']:
                a = g['accessors'][p['attributes']['POSITION']]
                for cx in (a['min'][0], a['max'][0]):
                    for cy in (a['min'][1], a['max'][1]):
                        for cz in (a['min'][2], a['max'][2]):
                            v = [m[r][0] * cx + m[r][1] * cy + m[r][2] * cz + m[r][3] for r in range(3)]
                            for k in range(3):
                                lo[k], hi[k] = min(lo[k], v[k]), max(hi[k], v[k])
        for c in n.get('children', []):
            walk(c, m, depth + 1)

    eye = [[1, 0, 0, 0], [0, 1, 0, 0], [0, 0, 1, 0], [0, 0, 0, 1]]
    for r in g['scenes'][g.get('scene', 0)]['nodes']:
        walk(r, eye, 0)
    extras = []
    if col:
        extras.append('collider')
    if parts:
        extras.append('parts')
    if any(is_accent(m.get('name', '')) for m in g.get('materials', [])):
        extras.append('accent')
    return [round(v, 3) + 0.0 for v in lo], [round(v, 3) + 0.0 for v in hi], sorted(set(parts)), extras


def build(models):
    pieces = {}
    for f in sorted(os.listdir(models)):
        if not f.endswith('.gltf'):
            continue
        name = f[:-5]
        with open(os.path.join(models, f)) as fh:
            lo, hi, parts, extras = describe(json.load(fh))
        p = {'min': lo, 'max': hi}
        if name in POINTS:
            p['points'] = POINTS[name]
        p['extras'] = extras
        if parts:
            p['parts'] = parts
        pieces[name] = p
    missing = sorted(set(POINTS) - set(pieces))
    if missing:
        sys.exit('pieces.py: points for pieces that were not exported: ' + ', '.join(missing))
    return pieces


def changed(old, new):
    """True when a piece both lists have moved its box or its points: what
    breaks a set built for the old list. Added or removed pieces don't."""
    for name in old.keys() & new.keys():
        a, b = old[name], new[name]
        if a.get('points', {}) != b.get('points', {}):
            return True
        for k in ('min', 'max'):
            if any(abs(x - y) > 0.001 for x, y in zip(a[k], b[k])):
                return True
    return False


def self_check():
    a = {'P': {'min': [0, 0, 0], 'max': [1, 1, 1], 'points': {'top': [0, 1, 0]}}}
    assert not changed(a, a)
    assert not changed(a, {**a, 'Q': a['P']}), 'an added piece bumps nothing'
    assert not changed(a, {'P': {**a['P'], 'max': [1.0005, 1, 1]}}), 'rounding noise bumps nothing'
    assert changed(a, {'P': {**a['P'], 'max': [1.03, 1, 1]}}), 'a moved box bumps'
    assert changed(a, {'P': {**a['P'], 'points': {'top': [0, 0.9, 0]}}}), 'a moved point bumps'


def main(models=MODELS):
    self_check()
    pieces = build(models)
    version = 1
    if os.path.exists(OUT):
        with open(OUT) as fh:
            old = json.load(fh)
        version = old['version'] + 1 if changed(old['pieces'], pieces) else old['version']
    with open(OUT, 'w') as fh:
        json.dump({'version': version, 'pieces': pieces}, fh, indent=1, sort_keys=True)
        fh.write('\n')
    print('pieces.py:', len(pieces), 'pieces, version', version, '->', OUT)


if __name__ == '__main__':
    main(*sys.argv[1:2])
