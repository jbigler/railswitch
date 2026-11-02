// The tower's rooms: the lobby (the project) and a floor per worktree, built
// from the office kit (D145). A room is {scene, view, walk, update(state),
// ticks, name}: update redraws what the state changes (rack lights, screens,
// the agent) without building the room again. The starship's rooms (D144)
// with the bridge as the lobby, labs as floors and the transporter as the
// elevator.
//
// A floor's plan (24 × 16 m, 4 m high; x right, z towards the front): a
// corridor from the elevator (the left wall's middle) to the windows at the
// far end, glass rooms on both sides.
//   back:  the server room (the lobby's operations) · the café nook ·
//          the lounge
//   front: work (the lobby's reception) · the meeting room · the game room
// The pet lives by the café; androids wander the corridor and the nook.
import { THREE, kit, screen, label, rng, hash, setElevatorDoors, createRoamer, createFlyer } from './world.js';
import { agentAndroid, wanderer } from './actors.js';
import { film, neonSign } from './film.js';
import { issuesFor, prsFor, whoLabel } from './work.js';

const PI = Math.PI;

// Service and stack states, coloured.
export const stateColors = {
  running: 0x63e07c, up: 0x63e07c, starting: 0xffc857, partial: 0xffc857, activating: 0xffc857,
  failed: 0xff6b6b, error: 0xff6b6b, stopped: 0x3b4558, 'on demand': 0x4a7bd1,
};
const colorOf = (s) => stateColors[s] ?? 0x3b4558;
const css = (n) => '#' + n.toString(16).padStart(6, '0');
const checkColor = { pass: '#63e07c', fail: '#ff6b6b', pending: '#ffc857' };
const reviewColor = { approved: '#63e07c', changes_requested: '#ff6b6b', pending: '#ffc857', review_required: '#ffc857' };

// svcState is a service's systemd state ("active/running", "inactive/dead",
// "on-demand"…) in a word.
export function svcState(s) {
  if (s === 'on-demand') return 'on demand';
  const [active] = s.split('/');
  if (active === 'active' || active === 'reloading') return 'running';
  if (active === 'activating' || active === 'deactivating') return 'starting';
  if (active === 'failed') return 'failed';
  return 'stopped';
}

function pickable(o, tip, act) {
  o.userData.pick = { tip, act };
  return o;
}

// The pieces a room may use, loaded once.
export const KIT_NAMES = ['Arch_Floor_4m', 'Arch_Wall_Plain_4m', 'Arch_Wall_Plain_2m', 'Arch_Wall_Window_4m', 'Arch_Elevator_Floor', 'Arch_Elevator_Cab',
  'Ext_CityBackdrop', 'Arch_CeilingDuct_4m', 'Arch_Ceiling_PipeRun_4m', 'Arch_Ceiling_CableTray_4m',
  'Arch_Ceiling_Sprinkler_4m', 'Arch_Ceiling_Fan', 'Arch_Ceiling_Vent', 'Arch_WallPipes_4m',
  'Light_CeilingPanel', 'Light_PendantBox',
  'Tech_ServerRack', 'Tech_ServerRack_B', 'Tech_ToolCart', 'Tech_TerminalKiosk', 'Tech_MonitorBank',
  'Tech_MonitorStack', 'Tech_Laptop', 'Tech_MonitorWall_A',
  'Furn_Workstation_Lived', 'Furn_Workstation_Lived_B', 'Furn_Desk', 'Furn_CoffeeTable',
  'Deco_Boxes', 'Deco_Crate', 'Deco_MiniFridge', 'Deco_CorkBoard', 'Deco_NeonSign', 'Deco_Extinguisher', 'Deco_DeskLamp', 'Deco_Mug',
  'Pet_Cat_Walk', 'Pet_Cat_Sitting', 'Pet_Dog_Walk', 'Pet_Dog_Lying', 'Furn_DogBed', 'Deco_PetBowls', 'Pet_Parrot_Fly',
  'Furn_ParrotPerch', 'Pet_Cat_Curled', 'Pet_Aquarium', 'Pet_JellyfishTank', 'Pet_Gecko_Terrarium',
  'Char_Android_Agent', 'Char_Android_A', 'Char_Android_B', 'Char_Android_C', // the agent's is spawned in actors.js
  'Furn_Sofa_Tufted_L', 'Furn_Sofa', 'Deco_Plant_Floor', 'Furn_Armchair', 'Furn_MeetingTable', 'Furn_Chair',
  'Arch_GlassWall_4m', 'Arch_GlassWall_2m', 'Arch_GlassWall_Door_4m',
  'Deco_Plant_Snake', 'Deco_Plant_Monstera', 'Deco_Plant_Palm', 'Deco_Plant_Bush', 'Deco_Plant_Trailing',
  'Furn_SideTable', 'Deco_FloorLamp', 'Deco_Rug_Geo', 'Deco_CoatRack', 'Deco_Whiteboard', 'Deco_Backpack', 'Furn_Stool',
  'Deco_BookStack', 'Deco_PizzaBoxes'];

// decor is a floor's look, from its name: "randomly consistent", as the
// starship's decks (D144): the name's hash seeds the floor's hue, its second
// neon (a third of the wheel or more away) and its pet.
function decor(name) {
  const seed = hash(name);
  const r = rng(seed);
  const hue = r();
  const neon = (h) => new THREE.Color().setHSL(h % 1, 0.9, 0.55).getHex();
  return {
    seed, r,
    accent: neon(hue),
    secondary: neon(hue + 0.33 + r() * 0.34),
    pet: Object.keys(PETS)[Math.floor(r() * Object.keys(PETS).length)],
    pick: (list) => list[Math.floor(r() * list.length)],
  };
}

// glowFor is the neon strength that makes a colour look as bright as the
// others: green and cyan are far brighter to the eye than blue or magenta.
function glowFor(hex) {
  const c = new THREE.Color(hex);
  const lum = 0.2126 * c.r + 0.7152 * c.g + 0.0722 * c.b;
  return Math.min(1.6, Math.max(0.5, 0.3 / lum));
}

function put(k, parent, name, x, y, z, ry = 0) {
  const o = k.spawn(name);
  o.userData.kit = name;
  o.position.set(x, y, z);
  o.rotation.y = ry;
  parent.add(o);
  return o;
}

function box(parent, w, h, d, mat, x, y, z) {
  const m = new THREE.Mesh(new THREE.BoxGeometry(w, h, d), mat);
  m.position.set(x, y, z);
  parent.add(m);
  return m;
}

const FLOOR = { minX: -11.85, maxX: 11.85, minZ: -7.85, maxZ: 7.85, y: 0 };
const AREAS = [[FLOOR.minX, FLOOR.maxX, FLOOR.minZ, FLOOR.maxZ], [-14.2, FLOOR.minX, -0.95, 0.95]];
const LIFT = { x: -12, z: 0 }; // in the left wall, opening onto the corridor (+x)

// The floor plan (after office space-planning practice: zones by activity and
// noise; social spaces by the entrance and the main route; quiet work deep in
// the plan by the windows, screens at right angles to them; meeting near the
// windows just off the corridor; back of house windowless, by the core):
//   corridor   z -2…2, from the elevator to the right wall's windows
//   back-left  server room (or the lobby's operations), glass (x -12…-4, z -8…-2)
//   back-mid   café nook, open to the corridor: vending, counter, the pet
//   back-right lounge, glass: the big screen on the back wall
//   front-left work (or the lobby's reception), glass: the desks, the PR station
//   front-mid  meeting, glass, by the front windows
//   front-right game room, glass, by the windows
// The server room's panel comes first: its tube flickers (film.js).
const LIGHTS = [[-8, -5], [8, -5], [0, -5], [-8, 5], [0, 5], [8, 5], [-6, 0], [0, 0], [6, 0], [10, 0]];

// GLASS are the glass rooms' walls: [piece, x, z, ry]. A door piece's
// opening is its local x 0…2 (ry PI turns it to -2…0 in x): each opens onto
// the corridor where the plan has it.
const GLASS = [
  ['Arch_GlassWall_4m', -10, -2, 0], ['Arch_GlassWall_Door_4m', -6, -2, PI], ['Arch_GlassWall_4m', -4, -6, PI / 2], ['Arch_GlassWall_2m', -4, -3, PI / 2],
  ['Arch_GlassWall_Door_4m', 6, -2, PI], ['Arch_GlassWall_4m', 10, -2, 0], ['Arch_GlassWall_4m', 4, -6, PI / 2], ['Arch_GlassWall_2m', 4, -3, PI / 2],
  ['Arch_GlassWall_4m', -10, 2, 0], ['Arch_GlassWall_Door_4m', -6, 2, PI], ['Arch_GlassWall_2m', -4, 3, PI / 2], ['Arch_GlassWall_4m', -4, 6, PI / 2],
  ['Arch_GlassWall_Door_4m', -2, 2, PI], ['Arch_GlassWall_4m', 2, 2, 0], ['Arch_GlassWall_2m', 4, 3, PI / 2], ['Arch_GlassWall_4m', 4, 6, PI / 2],
  ['Arch_GlassWall_Door_4m', 6, 2, PI], ['Arch_GlassWall_4m', 10, 2, 0],
];
const DOOR_SLIDE = 1.9; // how far a glass door's leaf slides open (local -x)

// shell builds a floor's floor, walls, elevator, windows onto the city,
// ceiling and its lights (the ceiling shows in first person only).
function shell(k, scene, deco, ui) {
  for (const x of [-10, -6, -2, 2, 6, 10]) for (const z of [-6, -2, 2, 6]) put(k, scene, 'Arch_Floor_4m', x, 0, z);
  const walls = [];
  const wall = (name, x, z, ry) => { const w = put(k, scene, name, x, 0, z, ry); walls.push(w); return w; };
  // the back wall solid (racks, vending, the lounge's screen); windows along
  // the front rooms and at the corridor's far end; the elevator in the left
  for (const x of [-10, -6, -2, 2, 6, 10]) wall('Arch_Wall_Plain_4m', x, -8, 0);
  for (const z of [-6, -2, 2, 6]) wall('Arch_Wall_Window_4m', 12, z, -PI / 2);
  for (const x of [-10, -6, -2, 2, 6, 10]) wall('Arch_Wall_Window_4m', x, 8, PI);
  for (const z of [-6, 6]) wall('Arch_Wall_Plain_4m', -12, z, PI / 2);
  for (const z of [-3, 3]) wall('Arch_Wall_Plain_2m', -12, z, PI / 2);
  const lift = wall('Arch_Elevator_Floor', LIFT.x, LIFT.z, PI / 2);
  const cab = put(k, scene, 'Arch_Elevator_Cab', LIFT.x - 0.2, 0, LIFT.z, PI / 2);
  scene.userData.walls = walls;
  put(k, scene, 'Ext_CityBackdrop', 30, -4, 0, -PI / 2);
  put(k, scene, 'Ext_CityBackdrop', 4, -4, 28, PI);
  put(k, scene, 'Arch_WallPipes_4m', 2, 0, -7.99);

  // The glass rooms; each door slides open as the walker comes near.
  const glassDoors = [];
  for (const [name, x, z, ry] of GLASS) {
    const g = put(k, scene, name, x, 0, z, ry);
    if (name === 'Arch_GlassWall_Door_4m') glassDoors.push(g);
  }

  const ceiling = new THREE.Group();
  const top = new THREE.Mesh(new THREE.PlaneGeometry(24, 16), new THREE.MeshStandardMaterial({ color: 0x1a1c22, roughness: 0.9 }));
  top.rotation.x = PI / 2;
  top.position.y = 4;
  ceiling.add(top);
  // a light panel over each room and along the corridor, pendants over the
  // desks; services run along the corridor: a duct, a sprinkler line, a
  // cable tray over the server aisle
  const panels = LIGHTS.map(([x, z]) => put(k, ceiling, 'Light_CeilingPanel', x, 4, z, PI / 2));
  for (const z of [3.9, 6.6]) put(k, ceiling, 'Light_PendantBox', -10.7, 4, z, PI / 2);
  put(k, ceiling, 'Arch_Ceiling_CableTray_4m', -9.6, 4, -6.45);
  put(k, ceiling, deco.pick(['Arch_CeilingDuct_4m', 'Arch_Ceiling_PipeRun_4m']), 2, 4, 0);
  put(k, ceiling, 'Arch_Ceiling_Sprinkler_4m', -6, 4, 0);
  put(k, ceiling, 'Arch_Ceiling_Fan', 8.4, 4, -4.4);
  put(k, ceiling, 'Arch_Ceiling_Vent', -10.4, 4, 0);
  ceiling.visible = false;
  ceiling.userData.solid = true;
  scene.add(ceiling);
  scene.userData.ceiling = ceiling;

  scene.userData.signColor = deco.accent;
  // Thin neon far away blows out to white at the kit's strength: this view
  // sets it by the colour's brightness, bloom carrying the glow.
  scene.traverse((o) => {
    const n = o.material?.name ?? '';
    if (n === 'RS_Neon_Accent') o.material.emissiveIntensity = glowFor(deco.accent);
    if (n === 'RS_Neon_Secondary') o.material.emissiveIntensity = glowFor(deco.secondary);
  });
  const ticks = film(scene, { walls, panels, ceiling, accent: deco.accent, seed: deco.seed });

  // The elevator: its doors open as the walker comes near; the pad in front
  // of them opens the floor list.
  const doorCols = [];
  for (const o of [lift, cab, ...glassDoors]) o.traverse((c) => { if (/(Door_[LR]|GlassDoor)_COL\d*$/.test(c.name)) doorCols.push(c); });
  scene.userData.movingBoxes = () => doorCols.map((c) => new THREE.Box3().setFromObject(c, true));
  scene.updateMatrixWorld(true);
  const slides = glassDoors.map((d) => {
    const leaf = d.getObjectByName('GlassDoor');
    return { leaf, x0: leaf?.position.x ?? 0, at: d.localToWorld(new THREE.Vector3(1, 0, 0)), open: 0 };
  }).filter((sl) => sl.leaf);
  const glassTick = (dt) => {
    const v = ui.viewer();
    for (const sl of slides) {
      const want = v && Math.hypot(v.x - sl.at.x, v.z - sl.at.z) < 2.2 ? 1 : 0;
      sl.open += Math.max(-dt * 1.5, Math.min(dt * 1.5, want - sl.open));
      sl.leaf.position.x = sl.x0 - DOOR_SLIDE * sl.open;
    }
  };
  pickable(lift, 'Elevator: go to the lobby or another floor', ui.transport);
  const sign = label('Elevator', { size: 0.3 });
  sign.position.set(LIFT.x + 0.3, 3.0, LIFT.z);
  scene.add(sign);
  let open = 0;
  const doors = (dt) => {
    const v = ui.viewer();
    const want = v && Math.hypot(v.x - LIFT.x, v.z - LIFT.z) < 2.8 ? 1 : 0;
    open += Math.max(-dt, Math.min(dt, want - open));
    setElevatorDoors(lift, open);
    setElevatorDoors(cab, open);
  };
  return { ticks: [doors, glassTick, ...ticks], pad: { x: LIFT.x + 0.75, z: LIFT.z, r: 0.55, act: ui.transport } };
}

// rug lays a dark rug with a neon edge.
function rug(scene, x0, x1, z0, z1, accent) {
  box(scene, x1 - x0, 0.01, z1 - z0, new THREE.MeshStandardMaterial({ color: 0x15131c, roughness: 0.95 }), (x0 + x1) / 2, 0.005, (z0 + z1) / 2);
  const edge = new THREE.MeshBasicMaterial({ color: new THREE.Color(accent).multiplyScalar(1.2), toneMapped: false });
  box(scene, x1 - x0, 0.012, 0.02, edge, (x0 + x1) / 2, 0.006, z1);
  box(scene, 0.02, 0.012, z1 - z0, edge, x0, 0.006, (z0 + z1) / 2);
}

// bigScreen is a large framed screen; ry 0 faces +z.
function bigScreen(scene, x, y, z, ry, w, h, accent) {
  const g = new THREE.Group();
  box(g, w + 0.16, h + 0.16, 0.08, new THREE.MeshStandardMaterial({ color: 0x0b0c10, roughness: 0.5 }), 0, 0, -0.04);
  const edge = new THREE.MeshBasicMaterial({ color: new THREE.Color(accent).multiplyScalar(1.4), toneMapped: false });
  box(g, w + 0.2, 0.025, 0.025, edge, 0, h / 2 + 0.09, 0);
  box(g, w + 0.2, 0.025, 0.025, edge, 0, -h / 2 - 0.09, 0);
  const sc = screen(w, h, { size: 26 });
  sc.position.z = 0.005;
  g.add(sc);
  g.position.set(x, y, z);
  g.rotation.y = ry;
  scene.add(g);
  scene.userData.walls?.push(g); // the overview cuts it away with its wall
  g.screen = sc;
  return g;
}

// wallScreen is a canvas screen on a wall above a desk; ry faces it out.
function wallScreen(scene, x, y, z, ry, w, h) {
  return bigScreen(scene, x, y, z, ry, w, h, 0x333344).screen;
}

// station is a kiosk with a screen above it and a sign: a place to read
// something and click for more.
function station(k, scene, x, z, ry, title, tip, act, w = 1.5, h = 0.85) {
  pickable(put(k, scene, 'Tech_TerminalKiosk', x, 0, z, ry), tip, act);
  const sc = screen(w, h);
  sc.position.set(x, 2.15, z);
  sc.rotation.y = ry;
  sc.translateZ(0.12);
  sc.rotateX(-0.12);
  pickable(sc, tip, act);
  scene.add(sc);
  const l = label(title, { size: 0.3 });
  l.position.set(x, 2.25 + h / 2 + 0.2, z);
  scene.add(l);
  return sc;
}

// The pets: one per floor, picked by the floor's name; their corner is the
// café nook's left side (x -4…-2, z -5…-2).
const PET_NAMES = ['Nova', 'Pixel', 'Quark', 'Biscuit', 'Comet', 'Miso', 'Neon', 'Glitch', 'Mochi', 'Byte', 'Ramen', 'Chrome'];
const PETS = {
  cat: { kind: 'cat', sound: 'purrrr…' },
  dog: { kind: 'dog', sound: 'woof!' },
  parrot: { kind: 'macaw', sound: 'squawk! git push --force!' },
  sleeper: { kind: 'cat', sound: 'zzz… purr…' },
  aquarium: { kind: 'fish', sound: 'blub blub' },
  jellyfish: { kind: 'jellyfish', sound: '~ ~ ~' },
  gecko: { kind: 'gecko', sound: 'chirp!' },
};

// residents places the floor's pet and its wandering androids; start() sets
// them moving once every collider is in place.
function residents(k, scene, deco, ui, androids) {
  const which = deco.pet;
  const name = PET_NAMES[(deco.seed >>> 8) % PET_NAMES.length];
  const { kind, sound } = PETS[which];
  const mine = (o) => pickable(o, `${name} the ${kind}`, () => ui.say(`${name}: ${sound}`, o));
  const ticks = [];
  const later = [];
  const roam = (walk, rest, opts) => later.push(() => {
    const r = createRoamer({ walk, rest, colliders: k.colliders(scene), bounds: FLOOR, rng: rng(deco.seed + later.length), ...opts });
    ticks.push((dt) => r.update(dt, ui.viewer()));
  });
  const pair = (walkName, restName, opts) => {
    const walk = mine(k.spawn(walkName));
    const rest = mine(k.spawn(restName));
    scene.add(walk, rest);
    roam(walk, rest, opts);
  };
  // the pet's corner is the café nook's left side (x -4…-2, z -5…-2); the
  // walkers roam the corridor and the nook (the glass rooms' doors stay shut
  // to them)
  switch (which) {
    case 'cat':
      put(k, scene, 'Deco_PetBowls', -3.4, 0, -2.6);
      pair('Pet_Cat_Walk', 'Pet_Cat_Sitting', { start: { x: -3.2, z: -3.6, ry: PI / 2 }, speed: 0.5, stride: 0.3 });
      break;
    case 'dog':
      put(k, scene, 'Furn_DogBed', -3.2, 0, -3.8);
      put(k, scene, 'Deco_PetBowls', -2.4, 0, -2.6);
      pair('Pet_Dog_Walk', 'Pet_Dog_Lying', { start: { x: -3.2, z: -3.8, ry: PI / 2 }, homes: [{ x: -3.2, z: -3.8, ry: PI / 2 }], speed: 0.7, stride: 0.45, height: 0.6 });
      break;
    case 'parrot': {
      // a clear line between the perches: down the corridor
      const perches = [{ x: -3.2, y: 1.284, z: -3.6, ry: PI / 2 }, { x: 10.6, y: 1.284, z: 0.4, ry: -PI / 2 }];
      for (const p of perches) put(k, scene, 'Furn_ParrotPerch', p.x, 0, p.z, p.ry);
      const bird = mine(k.spawn('Pet_Parrot_Fly'));
      scene.add(bird);
      const f = createFlyer({ bird, perches, rng: rng(deco.seed) });
      ticks.push((dt) => f.update(dt));
      break;
    }
    case 'sleeper':
      put(k, scene, 'Deco_Crate', -3.3, 0, -3.6, PI / 2);
      mine(put(k, scene, 'Pet_Cat_Curled', -3.3, 0.46, -3.6, 0.4));
      break;
    case 'aquarium':
      mine(put(k, scene, 'Pet_Aquarium', -3.3, 0, -4.4, PI / 2));
      break;
    case 'jellyfish':
      mine(put(k, scene, 'Pet_JellyfishTank', -3.3, 0, -4.0));
      break;
    default: // the gecko's terrarium stands on the café counter
      mine(put(k, scene, 'Pet_Gecko_Terrarium', 2.9, 0.75, -7.55));
  }
  // the androids: each a roamer that walks wherever the floor is free
  const models = ['Char_Android_A', 'Char_Android_B', 'Char_Android_C'];
  const starts = [{ x: -6, z: 0 }, { x: 0, z: 0.5 }, { x: 6, z: -0.3 }]; // in the corridor
  for (let i = 0; i < androids; i++) {
    const a = wanderer(k, models[(deco.seed + i) % 3], rng(deco.seed + 31 * i), ui.say);
    scene.add(a);
    roam(a, null, { start: { ...starts[i], ry: deco.r() * PI * 2 }, speed: 0.8, stride: 1.1, height: 1.9, radius: 0.3, idle: [3, 10], shy: 1.4 });
  }
  return { ticks, start: () => later.forEach((f) => f()) };
}

// zoneSign hangs a neon sign naming a zone at its entrance, facing +z
// turned by ry (both faces read).
function zoneSign(scene, text, x, z, ry = 0) {
  const sign = neonSign(text, scene.userData.signColor, scene.userData.ceiling);
  sign.position.set(x, 2.75, z);
  sign.rotation.y = ry;
  scene.add(sign);
  sign.rods(x, 2.75, z, ry);
}

// café (the nook at the back, open to the corridor): the counter and the
// fridge against the back wall, the vending machines' place beside them,
// a bistro table.
function cafe(k, scene, deco, gecko) {
  zoneSign(scene, 'Café', 0, -1.3);
  put(k, scene, 'Furn_Desk', 2.0, 0, -7.55);
  put(k, scene, 'Deco_MiniFridge', 3.4, 0, -7.55);
  put(k, scene, 'Deco_Mug', 1.6, 0.75, -7.45);
  put(k, scene, 'Deco_Mug', 1.85, 0.75, -7.6);
  if (!gecko) put(k, scene, 'Deco_PizzaBoxes', 2.5, 0.75, -7.55, 0.2);
  put(k, scene, 'Furn_Stool', 1.4, 0, -6.6);
  put(k, scene, 'Furn_Stool', 2.6, 0, -6.6);
  put(k, scene, 'Furn_SideTable', 0.4, 0, -4.4);
  put(k, scene, 'Furn_Stool', -0.3, 0, -4.4);
  put(k, scene, 'Furn_Stool', 1.1, 0, -4.4);
  put(k, scene, 'Deco_Plant_Snake', 3.5, 0, -2.6, deco.r() * 6);
  put(k, scene.userData.ceiling, 'Deco_Plant_Trailing', 2.0, 4, -7.3, deco.r() * 6);
}

// lounge (back-right, behind glass): the sofa facing the big screen on the
// back wall from a comfortable 4.5 m, on a rug, an armchair and a side table,
// an arc lamp.
function lounge(k, scene, deco, ui, accent) {
  zoneSign(scene, 'Lounge', 5, -1.3);
  const big = bigScreen(scene, 8, 2.2, -7.88, 0, 4.2, 2.3, accent);
  pickable(big.screen, 'The big screen', () => ui.say('This screen is yours: what goes up here comes later.', big));
  put(k, scene, 'Deco_Rug_Geo', 8, 0, -4.6);
  put(k, scene, 'Furn_Sofa_Tufted_L', 8.6, 0, -3.3, PI);
  put(k, scene, 'Furn_CoffeeTable', 8.2, 0, -4.7);
  put(k, scene, 'Deco_BookStack', 8.0, 0.42, -4.7);
  put(k, scene, 'Furn_Armchair', 5.3, 0, -5.0, PI / 2);
  put(k, scene, 'Furn_SideTable', 5.2, 0, -3.0);
  put(k, scene, 'Deco_FloorLamp', 11.4, 0, -2.6, PI);
  put(k, scene, 'Deco_PizzaBoxes', 5.2, 0, -7.4, 0.3);
  put(k, scene, 'Deco_Backpack', 6.4, 0, -2.7, -1.0);
  put(k, scene, 'Deco_Plant_Monstera', 11.4, 0, -7.4, deco.r() * 6);
  put(k, scene.userData.ceiling, 'Deco_Plant_Trailing', 10.4, 4, -6.6, deco.r() * 6);
  return big;
}

// meeting (front-middle, behind glass, by the windows): a table for six
// facing a screen wall.
function meeting(k, scene, deco) {
  zoneSign(scene, 'Meeting', -3, 1.3);
  put(k, scene, 'Furn_MeetingTable', 0.3, 0, 5.0);
  for (const x of [-0.5, 0.3, 1.1]) {
    put(k, scene, 'Furn_Chair', x, 0, 4.25);
    put(k, scene, 'Furn_Chair', x, 0, 5.75, PI);
  }
  put(k, scene, 'Tech_MonitorWall_A', -3.35, 0, 5.0, PI / 2);
  put(k, scene, 'Tech_Laptop', -0.1, 0.76, 4.85, 0.2);
  put(k, scene, 'Deco_Mug', 0.7, 0.76, 5.2);
  put(k, scene, 'Deco_Plant_Palm', 3.4, 0, 7.4, deco.r() * 6);
  put(k, scene, 'Deco_Plant_Snake', 3.4, 0, 2.6, deco.r() * 6);
  put(k, scene, 'Deco_Plant_Bush', -3.4, 0, 7.4, deco.r() * 6);
}

// gameRoom (front-right, behind glass, by the windows): a sofa facing the
// arcade, a coffee table. shortcut: kiosks stand in for the arcade machines
// until the kit has them.
function gameRoom(k, scene, deco) {
  zoneSign(scene, 'Game room', 5, 1.3);
  rug(scene, 5.5, 11.5, 3.0, 7.6, deco.accent);
  for (const z of [3.8, 5.0]) put(k, scene, 'Tech_TerminalKiosk', 11.3, 0, z, -PI / 2);
  put(k, scene, 'Furn_Sofa', 7.0, 0, 4.6, PI / 2);
  put(k, scene, 'Furn_CoffeeTable', 8.6, 0, 4.6, PI / 2);
  put(k, scene, 'Deco_Plant_Floor', 11.4, 0, 7.4, deco.r() * 6);
  put(k, scene, 'Deco_PizzaBoxes', 8.6, 0.42, 4.5, 0.4);
}

// support: what sits at the corridor's far end and by the elevator.
function support(k, scene, deco) {
  put(k, scene, deco.pick(['Deco_Boxes', 'Deco_Crate']), 11.2, 0, 1.4, -PI / 2);
  put(k, scene, 'Deco_Extinguisher', -11.92, 0, -2.6, PI / 2);
}

// lab builds a worktree's floor. ui: {transport, service(slug, svc),
// desk(slug), agentDesk(slug), agent(slug), prs(), say(text, obj), viewer()}.
export async function lab(slug, n, ui) {
  const k = await kit(KIT_NAMES);
  const deco = decor(slug);
  k.setAccent(css(deco.accent));
  k.setSecondary(css(deco.secondary));
  const scene = new THREE.Scene();
  const sh = shell(k, scene, deco, ui);
  const sign = label(`Floor ${n} · ${slug}`, { size: 0.34, color: css(deco.accent) });
  sign.position.set(-1.5, 3.4, -7.6);
  scene.add(sign);

  // Server room (back-left, windowless, behind glass): a data hall's two
  // rows of full-height racks (0.6 × 1 m), fronts facing a cold aisle along
  // the back wall; the way in is the glass door by the landing.
  zoneSign(scene, 'Server room', -7, -1.3);
  put(k, scene, 'Tech_ToolCart', -4.6, 0, -6.6, -PI / 2);
  const keepOut = new THREE.Mesh(new THREE.BoxGeometry(7.8, 2, 5.8), new THREE.MeshBasicMaterial());
  keepOut.name = 'PetKeepOut_COL'; // pets and androids stay out of the server room; you don't
  keepOut.visible = false;
  keepOut.position.set(-8, 1, -5);
  scene.add(keepOut);
  const racks = new Map();
  // shortcut: room for 14 racks; a stack with more services shows the first 14
  const rackSpot = (i) => (i < 7 ? { x: -11.3 + i * 0.62, z: -7.45, ry: 0 } : i < 14 ? { x: -11.3 + (i - 7) * 0.62, z: -5.5, ry: PI } : null);

  // Work (front-left, behind glass): your desk and the agent's against the
  // left wall, screens above them; the PR station by the windows.
  zoneSign(scene, 'Work', -7, 1.3);
  rug(scene, -11.9, -4.1, 2.1, 7.9, deco.accent);
  const desk = put(k, scene, deco.pick(['Furn_Workstation_Lived', 'Furn_Workstation_Lived_B']), -10.7, 0, 3.9, PI / 2);
  pickable(desk, 'Your desk: terminal, Neovim, links', () => ui.desk(slug));
  const mine = pickable(wallScreen(scene, -11.92, 2.65, 3.9, PI / 2, 2.2, 1.2), 'Your desk: terminal, Neovim, links', () => ui.desk(slug));
  const agentDesk = put(k, scene, 'Furn_Desk', -10.95, 0, 6.6, PI / 2);
  put(k, scene, 'Tech_MonitorStack', -10.75, 0.75, 6.6, PI / 2);
  put(k, scene, 'Deco_DeskLamp', -11.05, 0.75, 5.6, 2.6 + PI / 2);
  put(k, scene, 'Deco_Mug', -10.7, 0.75, 7.4);
  pickable(agentDesk, 'Agent console: open it in a terminal', () => ui.agentDesk(slug));
  const theirs = pickable(wallScreen(scene, -11.92, 2.7, 6.6, PI / 2, 1.6, 0.9), 'Agent console: open it in a terminal', () => ui.agentDesk(slug));
  const partner = agentAndroid(k);
  partner.position.set(-9.95, 0, 6.6);
  partner.rotation.y = -PI / 2; // faces its monitors
  partner.userData.block = true;
  pickable(partner, 'Your agent partner: status, screen, type to it', () => ui.agent(slug));
  scene.add(partner);
  put(k, scene, 'Deco_Whiteboard', -4.6, 0, 5.0, -PI / 2);
  put(k, scene, 'Deco_CoatRack', -4.6, 0, 7.5);
  put(k, scene, 'Deco_Backpack', -9.4, 0, 5.0, 0.7);
  put(k, scene, 'Deco_BookStack', -11.0, 0.75, 2.95, 0.3);
  put(k, scene, 'Deco_CorkBoard', -11.98, 1.7, 5.25, PI / 2);
  put(k, scene, 'Deco_Plant_Monstera', -4.6, 0, 2.6, deco.r() * 6);
  put(k, scene.userData.ceiling, 'Deco_Plant_Trailing', -10.0, 4, 3.0, deco.r() * 6);
  const pr = station(k, scene, -7.4, 7.2, PI, 'Pull request', 'Pull request: open it, or all of them', () => ui.prs(), 2.0, 1.1);

  const which = deco.pet;
  cafe(k, scene, deco, which === 'gecko');
  const big = lounge(k, scene, deco, ui, deco.accent);
  meeting(k, scene, deco);
  gameRoom(k, scene, deco);
  support(k, scene, deco);
  const res = residents(k, scene, deco, ui, 1 + (deco.seed % 2));
  res.start();

  const ticks = [...sh.ticks, partner.tick, ...res.ticks];
  const update = (st) => {
    const w = st.worktrees.find((x) => x.slug === slug);
    if (!w) return;
    w.services.forEach((svc, i) => {
      let rk = racks.get(svc.service);
      if (!rk) {
        const s = rackSpot(i);
        if (!s) return;
        rk = rack(k, svc, i);
        rk.position.set(s.x, 0, s.z);
        rk.rotation.y = s.ry;
        pickable(rk, '', () => ui.service(slug, svc.service));
        racks.set(svc.service, rk);
        scene.add(rk);
        ticks.push(rk.tick);
      }
      rk.set(svc);
      rk.userData.pick.tip = `${svc.service}: ${svcState(svc.state)}`;
    });
    for (const [name, rk] of racks) {
      if (!w.services.some((s) => s.service === name)) {
        scene.remove(rk);
        racks.delete(name);
        ticks.splice(ticks.indexOf(rk.tick), 1);
      }
    }
    mine.update([
      { text: `stack: ${w.stack}`, color: css(colorOf(w.stack)) },
      w.pr ? `PR #${w.pr.number} ${w.pr.state.toLowerCase()}` : 'no PR',
      ...w.links.slice(0, 4).map((l) => ({ text: '↗ ' + l.label, color: '#9fc3ff' })),
    ], `⎇ ${w.branch || slug}`);
    pr.update(w.pr ? [
      ...wrap(w.pr.title, 34, 2),
      { text: `checks: ${w.pr.checks || 'none'}`, color: checkColor[w.pr.checks] ?? '#9ff3ff' },
      { text: `review: ${(w.pr.review || 'none').replace('_', ' ')}`, color: reviewColor[w.pr.review] ?? '#9ff3ff' },
      ...(w.pr.review_app ? [{ text: '↗ review app', color: '#9fc3ff' }] : []),
    ] : [st.pr_error ? 'PRs unavailable' : 'no pull request for this branch'], w.pr ? `#${w.pr.number} · ${w.pr.state.toLowerCase()}` : 'Pull request');
    const up = w.services.filter((s) => svcState(s.state) === 'running').length;
    big.screen.update([
      { text: `stack ${w.stack} · ${up}/${w.services.length} services running`, color: css(colorOf(w.stack)) },
      w.pr ? `PR #${w.pr.number} · checks ${w.pr.checks || 'none'} · review ${(w.pr.review || 'none').replace('_', ' ')}` : 'no pull request',
      w.agent ? `agent ${w.agent.name}: ${w.agent.state}${w.agent.usage ? ` · ${tokens(w.agent.usage)} tokens` : ''}` : 'no agent running',
      '',
      { text: 'Yours to fill: pin what matters here.', color: '#8b8fb8' },
    ], slug);
    const a = w.agent;
    partner.set(a ? a.state : 'off');
    theirs.update(a ? [{ text: a.state, color: '#ffe08a' }, ...(a.usage ? [{ text: `${tokens(a.usage)} tokens · ${tokens({ ...a.usage, input: 0, cache_read: 0, cache_write: 0 })} out`, color: '#9fc3ff' }] : []), ...(a.message ? wrap(a.message, 30, a.usage ? 3 : 4) : [])] : ['no agent running'], a ? a.name : 'agent');
  };
  const walk = { areas: AREAS, start: { x: LIFT.x + 1.7, z: LIFT.z, yaw: PI / 2 }, pads: [sh.pad] };
  return { scene, view: { target: new THREE.Vector3(0, 0.5, 0), yaw: 0.2, pitch: 0.85, dist: 24, max: 40 }, walk, update, ticks, name: `Floor ${n} · ${slug}` };
}

// rack is a server rack for one service: its LEDs and edge lights show the
// service's state.
function rack(k, svc, i) {
  const g = k.spawn(i % 4 < 2 ? 'Tech_ServerRack' : 'Tech_ServerRack_B');
  const lights = new THREE.MeshBasicMaterial({ color: 0x3b4558, toneMapped: false });
  g.traverse((o) => {
    const n = o.isMesh ? o.material?.name ?? '' : '';
    if (n.startsWith('RS_LED_') || n === 'RS_Neon_Accent') o.material = lights;
  });
  const tag = label(svc.service, { size: 0.26 });
  tag.position.set(0, 2.35, 0.2);
  g.add(tag);
  let state = '';
  const base = new THREE.Color();
  g.set = (s) => {
    state = svcState(s.state);
    base.setHex(colorOf(state)).multiplyScalar(1.6);
    lights.color.copy(base);
  };
  g.tick = (dt, t) => {
    if (state === 'running') lights.color.copy(base).multiplyScalar(0.75 + Math.sin(t * 3 + i) * 0.25);
  };
  return g;
}

// tokens is an agent session's usage in a few characters: 1.2M, 34k, 512.
export function tokens(u) {
  const n = u.input + u.output + u.cache_read + u.cache_write;
  return n >= 1e6 ? `${(n / 1e6).toFixed(1)}M` : n >= 1e3 ? `${Math.round(n / 1e3)}k` : String(n);
}

// clip cuts text to n characters, an ellipsis marking the cut.
const clip = (t, n) => (t.length > n ? t.slice(0, n - 1) + '…' : t);

function wrap(text, n, max) {
  const out = [];
  let line = '';
  for (const w of String(text).split(/\s+/)) {
    if ((line + ' ' + w).trim().length > n) {
      out.push(line);
      line = w;
    } else line = (line + ' ' + w).trim();
    if (out.length >= max) return out;
  }
  if (line) out.push(line);
  return out.slice(0, max);
}

// lobby builds the project's floor on the same plan: operations where a
// floor has its server room (the PR board and the stations), reception where
// a floor works (the floors board, the concierge, waiting chairs), the
// directory in the corridor's middle, and the same café, lounge, meeting
// and game rooms.
// ui: {transport, toFloor(slug), prs(), issues(), notes(), config(), say(text, obj), viewer()}.
export async function lobby(prefix, ui) {
  const k = await kit(KIT_NAMES);
  const deco = decor('lobby:' + prefix);
  k.setAccent(css(deco.accent));
  k.setSecondary(css(deco.secondary));
  const scene = new THREE.Scene();
  const sh = shell(k, scene, deco, ui);

  // Operations (back-left, behind glass, where a floor has its server room).
  zoneSign(scene, 'Operations', -7, -1.3);
  const board = bigScreen(scene, -11.88, 2.3, -5.0, PI / 2, 2.8, 1.6, deco.accent);
  pickable(board.screen, 'Pull requests', ui.prs);
  pickable(put(k, scene, 'Tech_TerminalKiosk', -11.0, 0, -5.0, PI / 2), 'Pull requests', ui.prs);
  const issues = station(k, scene, -8.6, -7.35, 0, 'Issues', 'GitHub issues', ui.issues, 2.2, 1.25);
  const notes = station(k, scene, -4.85, -6.6, -PI / 2, 'Notes', "The agents' shared notes", ui.notes);
  const eng = station(k, scene, -4.85, -4.4, -PI / 2, 'Engineering', 'Project settings, images, library', ui.config);
  // The work boards: the open issues and PRs of the people the work filter
  // shows (work.js), read when the lobby opens and every two minutes while it
  // shows (each read asks GitHub); a filter change redraws them at once.
  issues.update(['reading…'], 'Issues');
  board.screen.update(['reading…'], 'Pull requests');
  let issueDoc = null, prDoc = null, live = true;
  const drawWork = () => {
    if (issueDoc) {
      const list = issuesFor(issueDoc);
      issues.update(list.length ? list.slice(0, 9).map((i) => ({
        text: clip(`#${String(i.number).padEnd(5)} ${i.title}`, 34),
        color: i.assignees.length ? '#c9f6ff' : '#ffe08a',
      })) : ['nothing open here'], `Issues · ${whoLabel(issueDoc.me)} · ${list.length}`);
    }
    if (prDoc) {
      const list = prsFor(prDoc);
      board.screen.update(list.length ? list.slice(0, 8).map((x) => ({
        text: clip(`#${String(x.number).padEnd(5)} ${x.draft ? 'draft ' : ''}${x.review.replace('_', ' ').padEnd(9)} ${x.title}`, 44),
        color: x.reviewers.some((r) => r.toLowerCase() === prDoc.me.toLowerCase()) ? '#ffe08a' : reviewColor[x.review] ?? '#c9f6ff',
      })) : ['nothing open here'], `Pull requests · ${whoLabel(prDoc.me)} · ${list.length}`);
    }
  };
  const readWork = () => {
    ui.issueList().then((d) => { if (live) { issueDoc = d; drawWork(); } }).catch(() => live && issues.update(['issues unavailable'], 'Issues'));
    ui.prList().then((d) => { if (live) { prDoc = d; drawWork(); } }).catch(() => live && board.screen.update(['PRs unavailable'], 'Pull requests'));
  };
  readWork();
  let workAge = 0;
  const workTick = (dt) => { if ((workAge += dt) > 120) { workAge = 0; readWork(); } };
  notes.update(['shared agent notes', 'read · edit · add'], 'Notes');
  eng.update(['settings', 'images', 'library'], 'Engineering');

  // Reception (front-left, where a floor works): the floors board on the
  // left wall, the concierge at its desk facing the door, waiting chairs
  // facing the board; the project's name over the café, seen from the
  // elevator.
  zoneSign(scene, 'Reception', -7, 1.3);
  rug(scene, -11.9, -4.1, 2.1, 7.9, deco.accent);
  put(k, scene, 'Tech_MonitorBank', -11.97, 0, 5.0, PI / 2);
  const view = screen(3.6, 1.4, { size: 30, fg: '#c9f6ff' });
  view.position.set(-11.9, 3.15, 5.0);
  view.rotation.y = PI / 2;
  scene.add(view);
  put(k, scene, 'Deco_NeonSign', 0, 2.6, -7.97);
  const name = label(prefix, { size: 0.5, color: css(deco.accent) });
  name.position.set(0, 3.5, -7.6);
  scene.add(name);
  put(k, scene, 'Furn_Desk', -7.0, 0, 4.0);
  put(k, scene, 'Tech_Laptop', -7.0, 0.75, 4.0, PI);
  const concierge = wanderer(k, 'Char_Android_B', rng(deco.seed + 7), ui.say);
  concierge.position.set(-7.0, 0, 4.8);
  concierge.rotation.y = PI; // behind the desk, facing the door
  concierge.userData.block = true;
  scene.add(concierge);
  for (const z of [5.4, 6.8]) put(k, scene, 'Furn_Armchair', -9.2, 0, z, -PI / 2);
  put(k, scene, 'Furn_SideTable', -9.2, 0, 6.1);
  put(k, scene, 'Deco_Plant_Palm', -4.6, 0, 7.4, deco.r() * 6);
  put(k, scene, 'Deco_CoatRack', -4.6, 0, 2.6);

  // The directory, in the corridor's middle.
  const pedestal = new THREE.Group();
  const stand = new THREE.Mesh(new THREE.CylinderGeometry(0.45, 0.55, 0.9, 32), new THREE.MeshStandardMaterial({ color: 0x16181e, metalness: 0.6, roughness: 0.4 }));
  stand.position.y = 0.45;
  pedestal.add(stand);
  const rim = new THREE.Mesh(new THREE.TorusGeometry(0.47, 0.015, 6, 48), new THREE.MeshBasicMaterial({ color: new THREE.Color(deco.accent).multiplyScalar(1.4), toneMapped: false }));
  rim.rotation.x = PI / 2;
  rim.position.y = 0.9;
  pedestal.add(rim);
  pedestal.position.set(0, 0, 0);
  pedestal.userData.block = true;
  scene.add(pedestal);
  const beacons = new THREE.Group();
  beacons.position.set(0, 1.0, 0);
  scene.add(beacons);
  const dirSign = label('Directory', { size: 0.3 });
  dirSign.position.set(0, 2.1, 0);
  scene.add(dirSign);

  const which = deco.pet;
  cafe(k, scene, deco, which === 'gecko');
  const big = lounge(k, scene, deco, ui, deco.accent);
  meeting(k, scene, deco);
  gameRoom(k, scene, deco);
  support(k, scene, deco);
  const res = residents(k, scene, deco, ui, 2);
  res.start();

  const ticks = [...sh.ticks, ...res.ticks, workTick, (dt) => { beacons.rotation.y += dt * 0.25; }];
  let beaconKey = '';
  const update = (st) => {
    const lines = st.worktrees.map((w, i) => ({
      text: `${String(i + 1).padStart(2)} ${w.base ? '★' : '·'} ${w.slug.padEnd(18).slice(0, 18)} ${w.stack.padEnd(8)} ${w.agent ? '◆ ' + w.agent.state : ''}`,
      color: css(colorOf(w.stack)),
    }));
    view.update(lines.length ? lines : ['no worktrees'], `${prefix} · ${st.worktrees.length} floors`);
    const open = st.worktrees.filter((w) => w.pr);
    // the beacons change with the floors and their stacks only: rebuilt
    // then, the old ones' GPU buffers freed
    const key = st.worktrees.map((w) => `${w.slug}:${w.stack}`).join(' ');
    if (key !== beaconKey) {
      beaconKey = key;
      for (const m of beacons.children) {
        m.geometry.dispose();
        m.material.dispose();
      }
      beacons.clear();
    }
    if (!beacons.children.length) st.worktrees.forEach((w, i) => {
      const a = (i / Math.max(1, st.worktrees.length)) * PI * 2;
      const c = new THREE.Color(colorOf(w.stack));
      const m = new THREE.Mesh(new THREE.CylinderGeometry(0.06, 0.06, 0.4 + (w.stack === 'up' ? 0.35 : 0), 8),
        new THREE.MeshBasicMaterial({ color: c.multiplyScalar(1.6), toneMapped: false }));
      m.position.set(Math.cos(a) * 0.32, 0.25, Math.sin(a) * 0.32);
      pickable(m, `Floor ${i + 1} · ${w.slug}: ${w.stack}`, () => ui.toFloor(w.slug));
      beacons.add(m);
    });
    big.screen.update([
      `${st.worktrees.length} floors · ${st.worktrees.filter((w) => w.stack === 'up').length} up`,
      `${open.length} floors with an open pull request`,
      `${st.worktrees.filter((w) => w.agent).length} agents · ${tokens(st.worktrees.reduce((t, w) => {
        const u = w.agent?.usage;
        return u ? { input: t.input + u.input, output: t.output + u.output, cache_read: t.cache_read + u.cache_read, cache_write: t.cache_write + u.cache_write } : t;
      }, { input: 0, output: 0, cache_read: 0, cache_write: 0 }))} tokens`,
      '',
      { text: 'Yours to fill: pin what matters here.', color: '#8b8fb8' },
    ], prefix);
  };
  const walk = { areas: AREAS, start: { x: LIFT.x + 1.7, z: LIFT.z, yaw: PI / 2 }, pads: [sh.pad] };
  const leave = () => { live = false; };
  return { scene, view: { target: new THREE.Vector3(0, 0.5, 0), yaw: 0.15, pitch: 0.8, dist: 25, max: 40 }, walk, update, ticks, name: `Lobby · ${prefix}`, redrawWork: drawWork, leave };
}
