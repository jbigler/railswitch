# Rebuilds a built-in office set and exports its glTFs (docs/office-kit.md).
#   [OFFICE_KIT_SET=modern] blender --background --python tools/office-kit/build_all.py [-- --only <piece>...]
# or open it in Blender's Text Editor and Run Script. Blender 4.2 or later.
import os
import bpy

try:  # Text Editor: the open file's path
    src = bpy.context.space_data.text.filepath
except AttributeError:  # --python
    src = __file__
here = os.path.dirname(os.path.abspath(bpy.path.abspath(src)))
g = {'__file__': os.path.join(here, 'build_kit.py'), '__name__': 'office_kit'}
kit = os.environ.get('OFFICE_KIT_SET') or 'cyberpunk'
theme = () if kit == 'cyberpunk' else (f'theme_{kit}.py',)  # after the kit, before what it restyles
for f in ('build_kit.py', *theme, 'build_office.py', 'build_decor.py', 'build_arch.py', 'build_pets.py', 'build_androids.py', 'build_lounge.py', 'build_glass.py', 'build_cafe.py'):
    path = os.path.join(here, f)
    exec(compile(open(path).read(), path, 'exec'), g)
unknown = (g['ONLY'] or set()) - g['BUILT']
if unknown:
    raise SystemExit('build_all.py: no such piece: ' + ', '.join(sorted(unknown)) +
                     '\nPieces: ' + ' '.join(sorted(g['BUILT'])))
# The piece list from what was just exported (not for a build to OFFICE_KIT_OUT:
# pieces.json describes the checked-in models). cyberpunk's models write it;
# another set, built from the same kit, is stamped as built for it.
if not os.environ.get('OFFICE_KIT_OUT'):
    import runpy
    pieces = runpy.run_path(os.path.join(here, 'pieces.py'))
    pieces['main' if kit == 'cyberpunk' else 'stamp'](g['OUT'])
