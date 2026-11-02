# Rebuilds every office kit set and exports the GLBs (docs/office-kit.md).
#   blender --background --python tools/office-kit/build_all.py
# or open it in Blender's Text Editor and Run Script. Blender 4.2 or later.
import os
import bpy

try:  # Text Editor: the open file's path
    src = bpy.context.space_data.text.filepath
except AttributeError:  # --python
    src = __file__
here = os.path.dirname(os.path.abspath(bpy.path.abspath(src)))
g = {'__file__': os.path.join(here, 'build_kit.py'), '__name__': 'office_kit'}
for f in ('build_kit.py', 'build_office.py', 'build_decor.py', 'build_arch.py', 'build_pets.py', 'build_androids.py', 'build_lounge.py', 'build_glass.py'):
    path = os.path.join(here, f)
    exec(compile(open(path).read(), path, 'exec'), g)
