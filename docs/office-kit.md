# Virtual office asset kit

The 3D pieces the dashboard's virtual office is built from: a worn, lived-in
cyberpunk office walked in first person, several floors joined by an elevator,
one pet per floor, androids. 109 glTF files (`.gltf` + `.bin`, textures as PNGs beside
them) in `internal/web/static/office/models/`, a
loader in `internal/web/static/office/kit.js`, and the Blender scripts that
build them in `tools/office-kit/`.

## Conventions

- Meters. Each piece's origin is on the floor at its footprint's center, front
  facing +Z. Wall-hung pieces (posters, displays, signs) have their origin at
  their center with the back on the wall; ceiling pieces (`Arch_Ceiling_*`,
  `Arch_CeilingDuct_4m`, `Light_*`, `Deco_HangingPlanter`) at the ceiling,
  hanging down.
- A floor is 4 m tall; walls, floor tiles and ceiling runs are 4 m long, so the
  building sits on a 4 m grid.
- Names are `Category_Name_Variant`: `Arch` (walls, floors, doors, elevator,
  ceiling gear), `Furn`, `Tech` (screens, racks, carts), `Deco`, `Light`, `Pet`,
  `Ext` (outside the windows). A node's mesh has the node's name.
- Floors and walls are textured: `RS_Floor` (`RS_Trim_Floor.png`: 1 m
  steel plates, a perforated grate, a tread plate, seams, rivets, scratches,
  grime) and `RS_Wall` (`RS_Trim_Wall.png`: 1 m × 0.5 m panels, a vent, a
  hatch, grime streaks), each 2 m per repeat; the export projects their UVs
  along each face's main axis in the piece's own coordinates, so pieces on
  the 4 m grid continue each other's pattern.
- Triangle budgets: walls and floors under 500, small props under 1k, furniture
  under 3k, hero pieces (workstations, monitor walls, pets) under 5k.

## Materials

Every material name starts with `RS_`. The highlight color is one material, so
it changes everywhere at once:

| Material | Role | `kit.js` |
|---|---|---|
| `RS_Neon_Accent` | neon strips, outlines, fan rings | `setAccent` |
| `RS_LED_Accent` | status LEDs, the black cat's eyes | `setAccent` |
| `RS_Screen`, `RS_Screen_UI`, `RS_Screen_Term` | screens: a grayscale emissive texture tinted by the emissive color | `setAccent` |
| `RS_Neon_Secondary` | violet trim, the sign, jellyfish tentacles | `setSecondary` |
| `RS_FloorDisplay` | the elevator's floor number: blank, for a canvas texture | – |
| `RS_RackDisplay`, `RS_VendScreen`, `RS_ArcadeScreen` | a rack's status display, a vending machine's menu, an arcade's screen: blank, the page draws on them | – |
| `RS_Whiteboard` | the glass board's surface: one quad mapped to the full image, for a drawing later | – |
| `RS_HoloBed` | the holo-table's dark bed: the page draws the hologram above it | – |
| `RS_Body`, `RS_BodyDark`, `RS_PaintViolet` | worn paint (`draw_paint`): panel seams, screws, scratches and chips to bare metal, grime; 1 m per repeat, projected per face like the walls | – |
| `RS_ScreenOff`, `RS_Glow_Warm`, `RS_LED_Green`, `RS_LED_Amber`, … | fixed colors | – |

Neon is emissive at strength 4 (LEDs 8): it reads best with a bloom pass at a
threshold near 1.

## Colliders

Pieces the player bumps into carry hidden box meshes named `<piece>_COL`,
`<piece>_COL1`, … (one box per node, in the piece's local space). `kit.js`
hides them on load and `kit.colliders(root)` returns them. Wall-hung, desk-top,
ceiling and cable pieces have none. The door leaves' colliders are children of
the leaves, so they move with them.

## Moving parts

Named nodes, each with its pivot at the joint. `kit.animate(root, t)` moves
them all; `kit.setElevatorDoors(node, 0..1)` opens a landing or the cab.

| Nodes | Motion |
|---|---|
| `Door_L`, `Door_R` (in `Arch_Elevator_Floor`), `CabDoor_L`, `CabDoor_R` (in `Arch_Elevator_Cab`) | slide apart: `setElevatorDoors` |
| `Btn_Up`, `Btn_Down` | call buttons, for lighting by the caller |
| `Fan_Blades` | spin |
| `Cat_Head`, `Dog_Head`, `Parrot_Head` | look around; the parrot bobs |
| `Cat_Tail`, `Dog_Tail` | sway; the dog wags |
| `CatCurled_Body`, `Dog_Body`, `Gecko`, `Pet_Dog_Lying` | breathe |
| `Fish_1`…`Fish_6`, `Jelly_1`…`Jelly_3`, `*_Bubbles` | swim, pulse, rise inside their tanks |
| `Jelly_<n>_T1`…`T8` | each tentacle sways on its own and flares as its bell contracts |
| `CatWalk_Leg_FL`…`_BR`, `DogWalk_Leg_FL`…`_BR` | trot from the root's `userData.gait` and `phase` |
| `ParrotFly_Wing_L`, `ParrotFly_Wing_R` | spread and beat from the root's `userData.flap` |
| `<Android>_Head`, `_Arm_L/R`, `_Leg_L/R` | the androids walk (legs and arms in opposite phase) from an ancestor's `userData.gait` and `phase`; arms type while `userData.typing`; heads look around |

## Roaming

`createRoamer` (in `kit.js`) walks a pet around a floor. It is given two models:
the walking one (`Pet_Cat_Walk`, `Pet_Dog_Walk`) and the one shown while it rests
(`Pet_Cat_Sitting`, `Pet_Dog_Lying`). The pet rests a few seconds, picks a free
spot (or, with `homeChance`, one of its `homes`, such as a `Furn_DogBed`), finds
a path on a 25 cm grid around every collider lower than its `height` (A*, then
straightened), turns toward it and walks there; while the player is within
`shy` meters it stands still. `update(dt, player)` each frame; the roamer sets
both models' position, rotation and visibility, and the legs' gait and phase.
The grid is built once, so colliders that move later (elevator doors) count
where they were.

`createFlyer` moves `Pet_Parrot_Fly` between perches (`Furn_ParrotPerch`: its
bar is 1.284 m up) in an eased arc, wings spread while it flies and folded on a
perch. The arc is not checked against colliders: give it perches with a clear
line between them.

## The elevator

`Arch_Elevator_Floor` is one floor of it: the 4 m front wall with the door
surround and call panel, and a shaft section behind (inner 2.0 m wide, from
z = -0.15 to -2.3). Stack one per floor, 4 m apart. `Arch_Elevator_Cab` is the
car: origin at its floor, front opening at z = 0; it parks at (0, floor, -0.2)
relative to the floor piece.

## Pieces

- **Arch**: `Wall_Plain_4m`, `Wall_Plain_2m` (beside the elevator), `Wall_Window_4m`, `Wall_Door_4m`, `Floor_4m`,
  `Door`, `Elevator_Floor`, `Elevator_Cab`, `WallPipes_4m`, `CeilingDuct_4m`,
  `Ceiling_CableTray_4m`, `Ceiling_PipeRun_4m`, `Ceiling_LightRun_4m`,
  `Ceiling_Sprinkler_4m`, `Ceiling_HangingCables`, `Ceiling_Vent`, `Ceiling_Fan`.
  Glass partitions (`build_glass.py`): `GlassWall_4m` (two 2 m panes),
  `GlassWall_2m`, `GlassWall_Door_4m` (a fixed pane and a sliding leaf, the
  node `GlassDoor` with its collider, under a transom); floor-to-ceiling,
  frames only at the ends, the joints, floor and ceiling; centred on their
  line, seen from both sides.
- **Furn**: `Desk`, `Chair`, `Workstation_2Mon`, `Workstation_3Mon`,
  `Workstation_Lived`, `Workstation_Lived_B`, `Sofa`, `Sofa_Short`,
  `CoffeeTable`, `Bookshelf`, `CatTree`, `DogBed`, `ParrotPerch`.
- **Tech**: `Monitor`, `WallDisplay`, `MonitorStack` (desk-top), `MonitorWall_A`,
  `MonitorWall_B`, `MonitorBank`, `CRT`, `Laptop`, `DataPad`, `Keyboard`,
  `PCTower`, `ServerRack`, `ServerRack_B` (a bolted cabinet with armoured
  sides, secondary-neon edges, an accent bar, a status display, patch
  panels, LED grids and a vent; cable bundles out of its sides and up 4 m
  into the ceiling's tray; its collider is the cabinet alone),
  `NetworkShelf`, `TerminalKiosk`, `ToolCart`.
- **Café and game room** (`build_cafe.py`): `Tech_Vending_Ramen`,
  `Tech_Vending_Drinks` (worn violet cabinets with a menu screen, card
  reader, dispenser slot, side vents, a mount for a header sign),
  `Furn_CoffeeCounter` (an espresso machine, a grinder, cups),
  `Tech_Arcade_Bartop` on `Furn_Arcade_Stand` (an angled screen, a
  joystick and six buttons, sunset side art, a cartridge on top; it sits
  0.78 m up), `Deco_WallClock` (hands `Clock_Hour`, `Clock_Minute`,
  `Clock_Second`, turned about their local Z by the page).
- **Deco**: `DeskClutter`, `DeskLamp`, `Headphones`, `Mug`, `CableRun`, `Boxes`,
  `Crate`, `TrashBin`, `Poster`, `CorkBoard`, `Extinguisher`, `MiniFridge`,
  `NeonSign`, `Plant_Floor`, `Plant_Floor_B`, `Plant_Small`, `HangingPlanter`,
  `PetBowls`.
- **Light**: `CeilingPanel`, `PendantBox`, `HexCluster`.
- **Pet**: `Cat_Sitting`, `Cat_Curled`, `Dog_Bed`, `Parrot_Perch`, `Aquarium`,
  `JellyfishTank`, `Gecko_Terrarium`; for roaming `Cat_Walk`, `Dog_Walk`,
  `Dog_Lying` (no bed) and `Parrot_Fly`, with `Furn_DogBed` and
  `Furn_ParrotPerch`.
- **Ext**: `CityBackdrop` (a flat night skyline; the office draws its city
  in the page instead, `static/cyberpunk/city.js`).
- **Lounge and lived-in** (`build_lounge.py`): `Furn_Sofa_L` (an L-shaped
  sectional in charcoal leather on a painted plinth, a channel-quilted back,
  an oxblood throw and pillow, a secondary-neon strip along its base),
  `Furn_Armchair` (charcoal leather between painted panels),
  `Furn_HoloTable` (3.0 × 2.0 m: a chunky console, rim panels and corner
  blocks round a recessed `RS_HoloBed`, a control column with a glowing
  orb, grab rails, angled legs; every glow the secondary neon), plants in
  charcoal and concrete pots, each with a thin neon rim
  (`Deco_Plant_Snake`, `Monstera`, `Palm`, `Bush`, `Trailing` hanging from
  the ceiling), `Furn_SideTable`, `Deco_FloorLamp` (arc), `Deco_Rug_Geo`,
  `Deco_CoatRack` (a jacket, a cap, a scarf), `Deco_Whiteboard` (a 2.4 ×
  1.2 m dark glass board in glowing marker: a diagram, a checklist), `Deco_Backpack`, `Furn_Stool`, `Deco_BookStack`,
  `Deco_PizzaBoxes`. No plant has a white pot.
- **Char**: humanoid androids, simple but anatomical (one smooth shell for
  chest, waist and hips; a sculpted face with glowing eyes and round ear
  units; dark glowing joints; neck cables; about 6.7k triangles, over the
  hero budget). `Android_Agent` (the agent's partner: its eyes
  `RS_Android_Visor_Agent` and status ring `RS_Android_Status` are for the
  page to recolour by the agent's state), `Android_A`, `Android_B`,
  `Android_C` (wanderers in ivory, black chrome and silver).

## Rebuilding

The pieces are procedural: `tools/office-kit/build_kit.py` holds the shared
builder and materials, and one script per set builds and exports it
(`build_office.py`, `build_decor.py`, `build_arch.py`, `build_pets.py`,
`build_androids.py`, `build_lounge.py`, `build_glass.py`, `build_cafe.py`).
Geometry is written in three.js coordinates and turned to Blender's Z-up at the
end, so the export lands back exactly as written. Animals are metaballs
converted to meshes, decimated to their budget and colored per face.

```sh
blender --background --python tools/office-kit/build_all.py
```

(or open `build_all.py` in Blender's Text Editor and run it). Blender 4.2 or
later. Each set clears and rebuilds its own collection (`CyberOfficeKit`,
`CyberOfficeDecor`, `CyberOfficeArch`, `CyberOfficePets`) and overwrites its
glTF files; `OFFICE_KIT_OUT` exports elsewhere. The export is glTF with
separate textures, not GLB: the dashboard's content policy refuses the
`blob:` URLs three.js makes for images embedded in a GLB. The export takes the active scene only
(`use_active_scene`): another scene in the same file that holds pieces of
the same names would otherwise rename the build's to `<name>.001` and
export those.

`tools/office-kit/preview.html` is a sample floor for checking the kit. Like
every floor it has one pet, chosen with the Pet picker or `?pet=` (`cat`, `dog`
roam; `parrot` flies between two perches; `sleeper`, `aquarium`, `jellyfish`,
`gecko` stay put); `?seed=<n>` makes a pet's choices repeatable. Two views,
switched by their buttons or V:

- **Overview**: drag to orbit, right-drag to pan, wheel to zoom; the ceiling
  and the walls facing the camera are hidden. Double-click the floor to walk
  there.
- **First person**: click a spot (or a piece of furniture) to walk there, drag
  to look, WASD or the arrow keys move (Shift runs, left/right arrows turn). E
  opens the elevator, R goes back to the start. The page uses no pointer lock
  and no `movementX`: remote and virtual-machine sessions often hand the
  browser an absolute pointer whose relative motion jumps, so looking follows
  the pointer's position and ignores a jump.
  The walker slides along what it touches; inside a collider or a wall it can
  always walk out.

Collider boxes leave out a piece's floor-level parts (cables, decals, the
jellyfish tank's floor ring), so they block only what stands up. It loads three.js from a CDN
through an import map, so it is a development page, not served by the
dashboard: from the repository root run `python3 -m http.server` and open
`http://localhost:8000/tools/office-kit/preview.html`.

## Not done yet

- A 3D city outside the windows (`Ext_CityBackdrop` is a flat image).
- The rest of the material pass: a palette texture for props, and a CPU
  ambient-occlusion bake into a second UV channel (floors and walls are
  textured).
- Compression (`gltf-transform`: Meshopt geometry, KTX2 textures).
