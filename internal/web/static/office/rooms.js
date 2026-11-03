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
import { THREE, kit, screen, label, rng, hash, setElevatorDoors, createRoamer, createFlyer, textCanvas, free } from './world.js';
import { agentAndroid, wanderer } from './actors.js';
import { film, neonSign } from './film.js';
import { city } from './city.js';
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
  'Arch_CeilingDuct_4m', 'Arch_Ceiling_PipeRun_4m', 'Arch_Ceiling_CableTray_4m',
  'Arch_Ceiling_Sprinkler_4m', 'Arch_Ceiling_Fan', 'Arch_Ceiling_Vent', 'Arch_WallPipes_4m',
  'Light_CeilingPanel', 'Light_PendantBox',
  'Tech_ServerRack', 'Tech_ServerRack_B', 'Tech_ToolCart', 'Tech_TerminalKiosk', 'Tech_MonitorBank',
  'Tech_MonitorStack', 'Tech_Laptop', 'Tech_MonitorWall_A',
  'Furn_Workstation_Lived', 'Furn_Workstation_Lived_B', 'Furn_Desk', 'Furn_CoffeeTable',
  'Deco_Boxes', 'Deco_Crate', 'Deco_MiniFridge', 'Deco_CorkBoard', 'Deco_NeonSign', 'Deco_Extinguisher', 'Deco_DeskLamp', 'Deco_Mug',
  'Pet_Cat_Walk', 'Pet_Cat_Sitting', 'Pet_Dog_Walk', 'Pet_Dog_Lying', 'Furn_DogBed', 'Deco_PetBowls', 'Pet_Parrot_Fly',
  'Furn_ParrotPerch', 'Pet_Cat_Curled', 'Pet_Aquarium', 'Pet_JellyfishTank', 'Pet_Gecko_Terrarium',
  'Char_Android_Agent', 'Char_Android_A', 'Char_Android_B', 'Char_Android_C', // the agent's is spawned in actors.js
  'Furn_Sofa_L', 'Furn_Sofa', 'Deco_Plant_Floor', 'Furn_Armchair', 'Furn_HoloTable', 'Furn_Chair',
  'Arch_GlassWall_4m', 'Arch_GlassWall_2m', 'Arch_GlassWall_Door_4m',
  'Deco_Plant_Snake', 'Deco_Plant_Monstera', 'Deco_Plant_Palm', 'Deco_Plant_Bush', 'Deco_Plant_Trailing',
  'Furn_SideTable', 'Deco_FloorLamp', 'Deco_Rug_Geo', 'Deco_CoatRack', 'Deco_Whiteboard', 'Deco_Backpack', 'Furn_Stool',
  'Deco_BookStack', 'Deco_PizzaBoxes',
  'Tech_Vending_Ramen', 'Tech_Vending_Drinks', 'Furn_CoffeeCounter', 'Furn_Arcade_Stand', 'Tech_Arcade_Bartop', 'Deco_WallClock'];

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
  const cityTick = city(scene, deco.seed);
  scene.userData.city = scene.children.at(-1);
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
  const doorBoxes = doorCols.map(() => new THREE.Box3()); // refilled each step, not made anew
  scene.userData.movingBoxes = () => { doorCols.forEach((c, i) => doorBoxes[i].setFromObject(c, true)); return doorBoxes; };
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
  let open = 0;
  const doors = (dt) => {
    const v = ui.viewer();
    const want = v && Math.hypot(v.x - LIFT.x, v.z - LIFT.z) < 2.8 ? 1 : 0;
    open += Math.max(-dt, Math.min(dt, want - open));
    setElevatorDoors(lift, open);
    setElevatorDoors(cab, open);
  };
  return { ticks: [doors, glassTick, cityTick, ...ticks], pad: { x: LIFT.x + 0.75, z: LIFT.z, r: 0.55, act: ui.transport } };
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
      const bar = k.point('Furn_ParrotPerch', 'bar')[1];
      const perches = [{ x: -3.2, y: bar, z: -3.6, ry: PI / 2 }, { x: 10.6, y: bar, z: 0.4, ry: -PI / 2 }];
      for (const p of perches) put(k, scene, 'Furn_ParrotPerch', p.x, 0, p.z, p.ry);
      const bird = mine(k.spawn('Pet_Parrot_Fly'));
      scene.add(bird);
      const f = createFlyer({ bird, perches, rng: rng(deco.seed) });
      ticks.push((dt) => f.update(dt));
      break;
    }
    case 'sleeper':
      put(k, scene, 'Deco_Crate', -3.3, 0, -3.6, PI / 2);
      mine(put(k, scene, 'Pet_Cat_Curled', -3.3, k.point('Deco_Crate', 'top')[1], -3.6, 0.4));
      break;
    case 'aquarium':
      mine(put(k, scene, 'Pet_Aquarium', -3.3, 0, -4.4, PI / 2));
      break;
    case 'jellyfish':
      mine(put(k, scene, 'Pet_JellyfishTank', -3.3, 0, -4.0));
      break;
    default: // the gecko's terrarium stands on the café counter
      mine(put(k, scene, 'Pet_Gecko_Terrarium', 1.15, k.point('Furn_CoffeeCounter', 'top')[1], -7.6));
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

// café (the nook at the back, open to the corridor): the vending machines
// by the elevator's side, the coffee counter and the fridge against the
// back wall, the clock above, a bistro table. Returns its ticks.
function cafe(k, scene, deco, gecko) {
  zoneSign(scene, 'Café', 0, -1.3);
  const ticks = [];
  for (const [name, x, title, color, kind] of [['Tech_Vending_Ramen', -2.95, 'Ramen', 0xff3fae, 'ramen'], ['Tech_Vending_Drinks', -1.8, 'Drinks', 0x29e7ff, 'drinks']]) {
    const v = put(k, scene, name, x, 0, -7.55);
    vendScreen(v, kind, color);
    const head = neonSign(title, color, null);
    head.position.set(x, 2.12, -7.25);
    head.scale.setScalar(0.85);
    scene.add(head);
  }
  const kanji = neonSign('麺', 0xff3fae, null);
  kanji.position.set(-3.7, 2.6, -7.9);
  scene.add(kanji);
  put(k, scene, 'Furn_CoffeeCounter', 2.0, 0, -7.65);
  put(k, scene, 'Deco_MiniFridge', 3.55, 0, -7.55);
  const counter = k.point('Furn_CoffeeCounter', 'top')[1];
  put(k, scene, 'Deco_Mug', 2.9, counter, -7.5);
  put(k, scene, 'Deco_Mug', 3.0, counter, -7.72);
  if (!gecko) put(k, scene, 'Deco_PizzaBoxes', 1.2, counter, -7.6, 0.2);
  put(k, scene, 'Furn_Stool', 1.4, 0, -6.8);
  put(k, scene, 'Furn_Stool', 2.6, 0, -6.8);
  put(k, scene, 'Furn_SideTable', 0.4, 0, -4.4);
  put(k, scene, 'Furn_Stool', -0.3, 0, -4.4);
  put(k, scene, 'Furn_Stool', 1.1, 0, -4.4);
  put(k, scene, 'Deco_Plant_Snake', 3.5, 0, -2.6, deco.r() * 6);
  put(k, scene.userData.ceiling, 'Deco_Plant_Trailing', 2.0, 4, -7.3, deco.r() * 6);
  ticks.push(wallClock(k, scene, -0.4, 2.55, -7.97));
  zoneSign(scene, 'Elevator', -7.5, 0, PI / 2); // both faces read: an arrow would point wrong on one
  return ticks;
}

// wallClock hangs the clock and turns its hands to the viewer's local time.
function wallClock(k, scene, x, y, z, ry = 0) {
  const c = put(k, scene, 'Deco_WallClock', x, y, z, ry);
  const hands = ['Clock_Hour', 'Clock_Minute', 'Clock_Second'].map((n) => c.getObjectByName(n));
  return () => {
    const d = new Date();
    const sec = d.getSeconds() + d.getMilliseconds() / 1000;
    const min = d.getMinutes() + sec / 60;
    const hr = (d.getHours() % 12) + min / 60;
    // clockwise, seen from the front (+z): negative turns about z
    if (hands[0]) hands[0].rotation.z = -(hr / 12) * PI * 2;
    if (hands[1]) hands[1].rotation.z = -(min / 60) * PI * 2;
    if (hands[2]) hands[2].rotation.z = -(Math.floor(sec) / 60) * PI * 2;
  };
}

// vendScreen draws a vending machine's menu on its blank screen: eight
// items in two columns, prices, a credit line and a BUY button.
function vendScreen(machine, kind, color) {
  const cv = document.createElement('canvas');
  cv.width = 312;
  cv.height = 380;
  const g = cv.getContext('2d');
  const tint = '#' + new THREE.Color(color).getHexString();
  g.fillStyle = '#0c0816';
  g.fillRect(0, 0, cv.width, cv.height);
  const items = kind === 'ramen'
    ? [['Chicken', '#ffd27a', 90], ['Veggie', '#d08cff', 100], ['Miso', '#ffb35c', 95], ['Spicy', '#ff4a4a', 110], ['Tomato', '#ff7a6a', 90], ['Curry', '#ffc93c', 99], ['Seafood', '#7ad7ff', 120], ['Extra hot', '#ff2d55', 115]]
    : [['Cola', '#ff3b3b', 60], ['Neon', '#7dff6a', 70], ['Lemon', '#fff36a', 60], ['Grape', '#b36aff', 70], ['Cold brew', '#c08a5a', 90], ['Water', '#8ad8ff', 40], ['Energy', '#ff8a2a', 95], ['Matcha', '#9ad66a', 85]];
  items.forEach(([name, c, price], n) => {
    const x = 14 + (n % 2) * 148, y = 12 + Math.floor(n / 2) * 78;
    g.strokeStyle = tint;
    g.globalAlpha = 0.6;
    g.lineWidth = 2;
    g.strokeRect(x, y, 136, 70);
    g.globalAlpha = 1;
    g.fillStyle = c;
    if (kind === 'ramen') { g.beginPath(); g.moveTo(x + 12, y + 22); g.lineTo(x + 44, y + 22); g.lineTo(x + 40, y + 60); g.lineTo(x + 16, y + 60); g.closePath(); g.fill(); }
    else g.fillRect(x + 16, y + 16, 24, 46);
    g.fillStyle = '#e8e2ff';
    g.font = '600 15px ui-sans-serif, system-ui, sans-serif';
    g.fillText(name, x + 52, y + 30);
    g.fillStyle = tint;
    g.font = '700 16px ui-monospace, monospace';
    g.fillText(`${price} ¢`, x + 52, y + 54);
  });
  g.fillStyle = tint;
  g.font = '700 22px ui-monospace, monospace';
  g.fillText('100 credits', 70, 340);
  g.strokeStyle = tint;
  g.lineWidth = 3;
  g.strokeRect(210, 318, 86, 38);
  g.fillText('BUY', 230, 345);
  const tex = new THREE.CanvasTexture(cv);
  tex.colorSpace = THREE.SRGBColorSpace;
  tex.flipY = false; // the model's UVs are glTF's, top-left origin
  machine.traverse((o) => { if (o.isMesh && o.material?.name === 'RS_VendScreen') o.material = new THREE.MeshBasicMaterial({ map: tex, toneMapped: false }); });
}

// boardFace returns a setter for the board's face (D151): a picture
// (ImageBitmap) of its drawing, fitted on the glass, or null for the
// diagrams the model comes with.
function boardFace(board) {
  let face = null;
  board.traverse((o) => { if (o.isMesh && o.material?.name === 'RS_Whiteboard') face = o; });
  if (!face) return () => {};
  const kept = face.material;
  const cv = document.createElement('canvas');
  cv.width = 2048;
  cv.height = 1024; // the board is 2.4 × 1.2 m
  const tex = new THREE.CanvasTexture(cv);
  tex.colorSpace = THREE.SRGBColorSpace;
  tex.flipY = false; // the model's UVs are glTF's, top-left origin
  tex.anisotropy = 4;
  const drawn = new THREE.MeshBasicMaterial({ map: tex, toneMapped: false });
  return (img) => {
    if (!img) {
      face.material = kept;
      return;
    }
    const g = cv.getContext('2d');
    g.fillStyle = '#121212'; // Excalidraw's dark background
    g.fillRect(0, 0, cv.width, cv.height);
    const s = Math.min((cv.width - 64) / img.width, (cv.height - 64) / img.height, 2);
    const w = img.width * s, h = img.height * s;
    g.drawImage(img, (cv.width - w) / 2, (cv.height - h) / 2, w, h);
    tex.needsUpdate = true;
    face.material = drawn;
  };
}

// arcadeScreen draws the bartop's attract screen: a sunburst over a sunset
// and PRESS START, blinking. Returns its tick.
function arcadeScreen(cab, seed) {
  const cv = document.createElement('canvas');
  cv.width = 256;
  cv.height = 205;
  const g = cv.getContext('2d');
  const tex = new THREE.CanvasTexture(cv);
  tex.colorSpace = THREE.SRGBColorSpace;
  tex.flipY = false;
  cab.traverse((o) => { if (o.isMesh && o.material?.name === 'RS_ArcadeScreen') o.material = new THREE.MeshBasicMaterial({ map: tex, toneMapped: false }); });
  const green = seed % 2 ? '#5dff8a' : '#ff6ad5';
  let age = 1, on = true;
  const draw = (t) => {
    g.fillStyle = '#04100a';
    g.fillRect(0, 0, cv.width, cv.height);
    g.strokeStyle = green;
    g.lineWidth = 2;
    const cx = 128, cy = 92;
    for (let k = 0; k < 24; k++) {
      const a = (k / 24) * PI * 2 + t * 0.3;
      g.beginPath();
      g.moveTo(cx + Math.cos(a) * 22, cy + Math.sin(a) * 22);
      g.lineTo(cx + Math.cos(a) * 62, cy + Math.sin(a) * 62);
      g.stroke();
    }
    g.fillStyle = green;
    g.beginPath();
    g.arc(cx, cy, 18, PI, 0);
    g.fill();
    for (let k = 0; k < 4; k++) g.fillRect(cx - 18, cy + 4 + k * 6, 36, 2);
    if (on) {
      g.font = '700 22px ui-monospace, monospace';
      g.fillText('PRESS START', 52, 186);
    }
    tex.needsUpdate = true;
  };
  draw(0);
  return (dt, t) => {
    if ((age += dt) < 0.5) return;
    age = 0;
    on = !on;
    draw(t);
  };
}

// lounge (back-right, behind glass): the sofa facing the big screen on the
// back wall from a comfortable 4.5 m, on a rug, an armchair and a side table,
// an arc lamp.
function lounge(k, scene, deco, ui, accent) {
  zoneSign(scene, 'Lounge', 5, -1.3);
  const big = bigScreen(scene, 8, 2.2, -7.88, 0, 4.2, 2.3, accent);
  pickable(big.screen, 'The big screen', () => ui.say('This screen is yours: what goes up here comes later.', big));
  put(k, scene, 'Deco_Rug_Geo', 8, 0, -4.6);
  put(k, scene, 'Furn_Sofa_L', 8.6, 0, -3.3, PI);
  put(k, scene, 'Furn_CoffeeTable', 8.2, 0, -4.7);
  put(k, scene, 'Deco_BookStack', 8.0, k.point('Furn_CoffeeTable', 'top')[1], -4.7);
  put(k, scene, 'Furn_Armchair', 5.3, 0, -5.0, PI / 2);
  put(k, scene, 'Furn_SideTable', 5.2, 0, -3.0);
  put(k, scene, 'Deco_FloorLamp', 11.4, 0, -2.6, PI);
  put(k, scene, 'Deco_PizzaBoxes', 5.2, 0, -7.4, 0.3);
  put(k, scene, 'Deco_Backpack', 6.4, 0, -2.7, -1.0);
  put(k, scene, 'Deco_Plant_Monstera', 11.4, 0, -7.4, deco.r() * 6);
  put(k, scene.userData.ceiling, 'Deco_Plant_Trailing', 10.4, 4, -6.6, deco.r() * 6);
  return big;
}

// meeting (front-middle, behind glass, by the windows): the holo-table,
// six chairs around it, a screen wall. Returns the table's hologram.
function meeting(k, scene, deco) {
  zoneSign(scene, 'Meeting', -3, 1.3);
  put(k, scene, 'Furn_HoloTable', 0.3, 0, 5.0);
  for (const x of [-0.7, 0.3, 1.3]) {
    put(k, scene, 'Furn_Chair', x, 0, 3.5);
    put(k, scene, 'Furn_Chair', x, 0, 6.5, PI);
  }
  put(k, scene, 'Tech_MonitorWall_A', -3.35, 0, 5.0, PI / 2);
  put(k, scene, 'Deco_Plant_Palm', 3.4, 0, 7.4, deco.r() * 6);
  put(k, scene, 'Deco_Plant_Snake', 3.4, 0, 2.6, deco.r() * 6);
  put(k, scene, 'Deco_Plant_Bush', -3.4, 0, 7.4, deco.r() * 6);
  return holoTable(scene, 0.3, 5.0, deco.accent); // the floor's main colour; the table's trim keeps the secondary
}

// holoTable is the hologram over the holo-table at (x, z), in color: a
// wireframe terrain rising from the bed, a pin per item standing out of it
// (coloured by state, labelled, clickable), a halo ring above with a HUD
// panel facing the door, crosshairs drifting in the air. set(items, title,
// lines): items [{label, color, tip, act}]; tick(dt, t) moves it.
function holoTable(scene, x, z, color) {
  const g = new THREE.Group();
  g.position.set(x, 0, z);
  scene.add(g);
  const tint = new THREE.Color(color).multiplyScalar(1.3);
  const glow = (opacity, extra = {}) => new THREE.MeshBasicMaterial({ color: tint, transparent: true, opacity, blending: THREE.AdditiveBlending, depthWrite: false, toneMapped: false, side: THREE.DoubleSide, ...extra });
  const BED = { w: 2.3, d: 1.3, y: 0.89 };
  // the bed's grid
  const lines = [];
  for (let i = 0; i <= 23; i++) lines.push(-BED.w / 2 + (i * BED.w) / 23, BED.y, -BED.d / 2, -BED.w / 2 + (i * BED.w) / 23, BED.y, BED.d / 2);
  for (let i = 0; i <= 13; i++) lines.push(-BED.w / 2, BED.y, -BED.d / 2 + (i * BED.d) / 13, BED.w / 2, BED.y, -BED.d / 2 + (i * BED.d) / 13);
  const gridGeo = new THREE.BufferGeometry();
  gridGeo.setAttribute('position', new THREE.Float32BufferAttribute(lines, 3));
  g.add(new THREE.LineSegments(gridGeo, new THREE.LineBasicMaterial({ color: tint, transparent: true, opacity: 0.1, blending: THREE.AdditiveBlending, depthWrite: false, toneMapped: false })));
  // the terrain: heights from a few slow waves, flattened toward the rim
  const geo = new THREE.PlaneGeometry(BED.w, BED.d, 30, 17); // sparse: dense lines add up to a solid glow
  geo.rotateX(-PI / 2);
  const pos = geo.attributes.position;
  const height = (px, pz, t) => {
    const fade = Math.max(0, 1 - (Math.abs(px) / (BED.w / 2)) ** 4) * Math.max(0, 1 - (Math.abs(pz) / (BED.d / 2)) ** 4);
    const v = 0.1 + 0.08 * Math.sin(px * 2.1 + t * 0.25) * Math.cos(pz * 2.7 - t * 0.18) + 0.05 * Math.sin(px * 5.3 + pz * 3.1 + t * 0.4);
    return BED.y + 0.01 + Math.max(0, v) * fade;
  };
  const terrain = new THREE.Mesh(geo, glow(0.16, { wireframe: true }));
  terrain.raycast = () => {};
  g.add(terrain);
  // the halo and its HUD panel
  const halo = new THREE.Group();
  halo.position.y = 2.7;
  for (const [w, d, hx, hz] of [[2.8, 0.025, 0, -0.8], [2.8, 0.025, 0, 0.8], [0.025, 1.6, -1.4, 0], [0.025, 1.6, 1.4, 0]]) {
    const bar = new THREE.Mesh(new THREE.BoxGeometry(w, 0.025, d), glow(0.9));
    bar.position.set(hx, 0, hz);
    halo.add(bar);
  }
  const hudCv = document.createElement('canvas');
  hudCv.width = 512;
  hudCv.height = 160;
  const hudTex = new THREE.CanvasTexture(hudCv);
  hudTex.colorSpace = THREE.SRGBColorSpace;
  const hud = new THREE.Mesh(new THREE.PlaneGeometry(1.6, 0.5), glow(1, { map: hudTex, color: 0xffffff }));
  hud.position.set(0, -0.32, -0.8);
  hud.rotation.y = PI; // faces the door (-z)
  hud.raycast = () => {};
  halo.add(hud);
  g.add(halo);
  // crosshairs in the air
  const plus = document.createElement('canvas');
  plus.width = plus.height = 32;
  const pg = plus.getContext('2d');
  pg.strokeStyle = '#fff';
  pg.lineWidth = 3;
  pg.beginPath();
  pg.moveTo(16, 4); pg.lineTo(16, 28); pg.moveTo(4, 16); pg.lineTo(28, 16);
  pg.stroke();
  const r = rng(hash(String(color)));
  const cross = new Float32Array(40 * 3);
  for (let i = 0; i < 40; i++) cross.set([(r() - 0.5) * 3.2, 1.2 + r() * 1.4, (r() - 0.5) * 2.0], i * 3);
  const crossGeo = new THREE.BufferGeometry();
  crossGeo.setAttribute('position', new THREE.BufferAttribute(cross, 3));
  const crosses = new THREE.Points(crossGeo, new THREE.PointsMaterial({ map: new THREE.CanvasTexture(plus), color: tint, size: 0.07, transparent: true, opacity: 0.7, blending: THREE.AdditiveBlending, depthWrite: false, toneMapped: false }));
  crosses.raycast = () => {};
  g.add(crosses);

  const pins = new THREE.Group();
  g.add(pins);
  let key = '';
  const spots = (n) => {
    const out = [];
    const cols = Math.max(1, Math.ceil(Math.sqrt(n * 1.8)));
    const rows = Math.ceil(n / cols);
    for (let i = 0; i < n; i++) {
      const cx = i % cols, cz = Math.floor(i / cols);
      out.push([(-0.5 + (cx + 0.5) / cols) * (BED.w - 0.4), (-0.5 + (cz + 0.5) / rows) * (BED.d - 0.3)]);
    }
    return out;
  };
  const drawHud = (title, rows) => {
    const c = hudCv.getContext('2d');
    c.clearRect(0, 0, hudCv.width, hudCv.height);
    c.strokeStyle = '#' + tint.getHexString();
    c.lineWidth = 3;
    c.strokeRect(4, 4, hudCv.width - 8, hudCv.height - 8);
    c.fillStyle = '#' + tint.getHexString();
    c.font = '700 34px ui-monospace, monospace';
    c.textBaseline = 'top';
    c.fillText(title.slice(0, 24), 18, 14);
    c.font = '24px ui-monospace, monospace';
    rows.slice(0, 3).forEach((l, i) => c.fillText(String(l).slice(0, 34), 18, 62 + i * 30));
    hudTex.needsUpdate = true;
  };
  return {
    set(items, title, rows) {
      drawHud(title, rows);
      const k2 = items.map((it) => `${it.label}:${it.color}`).join(' ');
      if (k2 === key) return;
      key = k2;
      for (const p of [...pins.children]) {
        p.traverse((o) => { o.geometry?.dispose(); o.material?.map?.dispose(); o.material?.dispose(); });
        pins.remove(p);
      }
      spots(items.length).forEach(([px, pz], i) => {
        const it = items[i];
        const pin = new THREE.Group();
        const base = height(px, pz, 0);
        const top = base + 0.55 + (i % 3) * 0.12;
        const c = new THREE.Color(it.color).multiplyScalar(1.5);
        const mat = new THREE.MeshBasicMaterial({ color: c, transparent: true, opacity: 0.9, blending: THREE.AdditiveBlending, depthWrite: false, toneMapped: false });
        const pole = new THREE.Mesh(new THREE.CylinderGeometry(0.01, 0.01, top - base, 6), mat);
        pole.position.set(px, (top + base) / 2, pz);
        const ring = new THREE.Mesh(new THREE.TorusGeometry(0.08, 0.01, 6, 24), mat);
        ring.rotation.x = PI / 2;
        ring.position.set(px, top, pz);
        const tag = label(it.label, { size: 0.2, color: '#' + c.getHexString() });
        tag.position.set(px, top + 0.16, pz);
        const hit = new THREE.Mesh(new THREE.CylinderGeometry(0.08, 0.08, top - base + 0.2, 8), new THREE.MeshBasicMaterial({ transparent: true, opacity: 0, depthWrite: false }));
        hit.position.copy(pole.position);
        pin.add(pole, ring, tag, hit);
        pickable(pin, it.tip, it.act);
        pins.add(pin);
      });
    },
    tick(dt, t) {
      for (let i = 0; i < pos.count; i++) pos.setY(i, height(pos.getX(i), pos.getZ(i), t));
      pos.needsUpdate = true;
      halo.position.y = 2.7 + Math.sin(t * 0.8) * 0.02;
      crosses.rotation.y = t * 0.03;
    },
  };
}

// gameRoom (front-right, behind glass, by the windows): two bartop arcades
// on their stands along the windows, a sofa facing them, a coffee table.
// Returns its ticks.
function gameRoom(k, scene, deco) {
  zoneSign(scene, 'Game room', 5, 1.3);
  rug(scene, 5.5, 11.5, 3.0, 7.6, deco.accent);
  const ticks = [];
  [3.8, 5.2].forEach((z, n) => {
    put(k, scene, 'Furn_Arcade_Stand', 11.3, 0, z, -PI / 2);
    ticks.push(arcadeScreen(put(k, scene, 'Tech_Arcade_Bartop', 11.3, k.point('Furn_Arcade_Stand', 'top')[1], z, -PI / 2), n));
  });
  put(k, scene, 'Furn_Sofa', 8.0, 0, 4.5, PI / 2);
  put(k, scene, 'Furn_CoffeeTable', 9.4, 0, 4.5, PI / 2);
  put(k, scene, 'Deco_Plant_Floor', 11.4, 0, 7.4, deco.r() * 6);
  put(k, scene, 'Deco_PizzaBoxes', 9.4, k.point('Furn_CoffeeTable', 'top')[1], 4.4, 0.4);
  return ticks;
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
  // 1 m apart: their cable loops stand out from their sides
  const rackSpot = (i) => (i < 7 ? { x: -11.2 + i * 1.0, z: -7.45, ry: 0 } : i < 14 ? { x: -11.2 + (i - 7) * 1.0, z: -5.5, ry: PI } : null);

  // Work (front-left, behind glass): your desk and the agent's against the
  // left wall, screens above them; the PR station by the windows.
  zoneSign(scene, 'Work', -7, 1.3);
  rug(scene, -11.9, -4.1, 2.1, 7.9, deco.accent);
  const desk = put(k, scene, deco.pick(['Furn_Workstation_Lived', 'Furn_Workstation_Lived_B']), -10.7, 0, 3.9, PI / 2);
  pickable(desk, 'Your desk: terminal, Neovim, links', () => ui.desk(slug));
  const mine = pickable(wallScreen(scene, -11.92, 2.65, 3.9, PI / 2, 2.2, 1.2), 'Your desk: terminal, Neovim, links', () => ui.desk(slug));
  const agentDesk = put(k, scene, 'Furn_Desk', -10.95, 0, 6.6, PI / 2);
  const deskTop = k.point('Furn_Desk', 'top')[1]; // the agent's desk
  put(k, scene, 'Tech_MonitorStack', -10.75, deskTop, 6.6, PI / 2);
  put(k, scene, 'Deco_DeskLamp', -11.05, deskTop, 5.6, 2.6 + PI / 2);
  put(k, scene, 'Deco_Mug', -10.7, deskTop, 7.4);
  pickable(agentDesk, 'Agent console: open it in a terminal', () => ui.agentDesk(slug));
  const theirs = pickable(wallScreen(scene, -11.92, 2.7, 6.6, PI / 2, 1.6, 0.9), 'Agent console: open it in a terminal', () => ui.agentDesk(slug));
  const partner = agentAndroid(k);
  partner.position.set(-9.95, 0, 6.6);
  partner.rotation.y = -PI / 2; // faces its monitors
  partner.userData.block = true;
  pickable(partner, 'Your agent partner: status, screen, type to it', () => ui.agent(slug));
  scene.add(partner);
  const board = pickable(put(k, scene, 'Deco_Whiteboard', -4.6, 0, 5.0, -PI / 2), 'Whiteboard: draw on it', () => ui.whiteboard(slug));
  const setBoard = boardFace(board);
  put(k, scene, 'Deco_CoatRack', -4.6, 0, 7.5);
  put(k, scene, 'Deco_Backpack', -9.4, 0, 5.0, 0.7);
  put(k, scene, 'Deco_BookStack', -11.0, k.point(desk.userData.kit, 'top')[1], 2.95, 0.3); // on your workstation
  put(k, scene, 'Deco_CorkBoard', -11.98, 1.7, 5.25, PI / 2);
  put(k, scene, 'Deco_Plant_Monstera', -4.6, 0, 2.6, deco.r() * 6);
  put(k, scene.userData.ceiling, 'Deco_Plant_Trailing', -10.0, 4, 3.0, deco.r() * 6);
  const pr = station(k, scene, -7.4, 7.2, PI, 'Pull request', 'Pull request: its page (checks, reviews, merge), or all of them', () => ui.prPage(slug), 2.0, 1.1);

  const which = deco.pet;
  const cafeTicks = cafe(k, scene, deco, which === 'gecko');
  const big = lounge(k, scene, deco, ui, deco.accent);
  const holo = meeting(k, scene, deco);
  const gameTicks = gameRoom(k, scene, deco);
  support(k, scene, deco);
  const res = residents(k, scene, deco, ui, 1 + (deco.seed % 2));
  res.start();

  const ticks = [...sh.ticks, partner.tick, ...res.ticks, holo.tick, ...cafeTicks, ...gameTicks];
  const update = (st) => {
    const w = st.worktrees.find((x) => x.slug === slug);
    if (!w) return;
    w.services.forEach((svc, i) => {
      // a rack's spot is its service's place in the list now: a service
      // removed before it moves it up, never onto another rack
      const s = rackSpot(i);
      let rk = racks.get(svc.service);
      if (!rk) {
        if (!s) return;
        rk = rack(k, svc, i, deco.secondary);
        pickable(rk, '', () => ui.service(slug, svc.service));
        racks.set(svc.service, rk);
        scene.add(rk);
        ticks.push(rk.tick);
      }
      rk.visible = !!s; // past the 14th spot: hidden until one frees up
      if (s) {
        rk.position.set(s.x, 0, s.z);
        rk.rotation.y = s.ry;
      }
      rk.set(svc);
      rk.userData.pick.tip = `${svc.service}: ${svcState(svc.state)}`;
    });
    for (const [name, rk] of racks) {
      if (!w.services.some((s) => s.service === name)) {
        scene.remove(rk);
        free(rk);
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
    holo.set(w.services.map((svc) => ({
      label: svc.service, color: colorOf(svcState(svc.state)),
      tip: `${svc.service}: ${svcState(svc.state)}`, act: () => ui.service(slug, svc.service),
    })), slug, [`stack ${w.stack} · ${up}/${w.services.length} running`, w.pr ? `PR #${w.pr.number} · ${w.pr.checks || 'no checks'}` : 'no PR', w.agent ? `agent ${w.agent.state}` : 'no agent']);
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
  return { scene, view: { target: new THREE.Vector3(0, 0.5, 0), yaw: 0.2, pitch: 0.85, dist: 24, max: 40 }, walk, update, ticks, name: `Floor ${n} · ${slug}`, setBoard };
}

// rack is a server rack for one service: its LEDs and accent bar show the
// service's state, its display names it, and a hologram floats above it in
// the floor's secondary colour (name, state, bars that pulse while it runs).
function rack(k, svc, i, secondary) {
  const g = k.spawn(i % 4 < 2 ? 'Tech_ServerRack' : 'Tech_ServerRack_B');
  const lights = new THREE.MeshBasicMaterial({ color: 0x3b4558, toneMapped: false });
  let display = null;
  g.traverse((o) => {
    const n = o.isMesh ? o.material?.name ?? '' : '';
    if (n.startsWith('RS_LED_') || n === 'RS_Neon_Accent') o.material = lights;
    if (n === 'RS_RackDisplay') display = o;
  });
  // the display: a plane of the blank quad's size, so the text can't come
  // out mirrored whatever the export did to its UVs
  const shot = new THREE.CanvasTexture(textCanvas([], { w: 256, h: 96 }));
  shot.colorSpace = THREE.SRGBColorSpace;
  if (display) {
    const b = new THREE.Box3().setFromObject(display);
    const panel = new THREE.Mesh(new THREE.PlaneGeometry(b.max.x - b.min.x, b.max.y - b.min.y), new THREE.MeshBasicMaterial({ map: shot, toneMapped: false }));
    panel.position.set((b.min.x + b.max.x) / 2, (b.min.y + b.max.y) / 2, b.max.z + 0.002);
    g.add(panel);
  }
  const holoCv = document.createElement('canvas');
  holoCv.width = 320;
  holoCv.height = 160;
  const holoTex = new THREE.CanvasTexture(holoCv);
  holoTex.colorSpace = THREE.SRGBColorSpace;
  const holo = new THREE.Mesh(new THREE.PlaneGeometry(0.9, 0.45), new THREE.MeshBasicMaterial({
    map: holoTex, transparent: true, blending: THREE.AdditiveBlending, depthWrite: false, side: THREE.DoubleSide, toneMapped: false,
  }));
  holo.position.set(0, 2.5, 0.35);
  holo.rotation.x = -0.12;
  holo.raycast = () => {}; // clicks reach the rack
  g.add(holo);
  const tint = css(secondary);
  let state = '';
  let age = 1;
  const drawHolo = (t) => {
    const c = holoCv.getContext('2d');
    const w = holoCv.width, h = holoCv.height;
    c.clearRect(0, 0, w, h);
    c.strokeStyle = tint;
    c.globalAlpha = 0.55;
    c.lineWidth = 3;
    for (const [x, y, dx, dy] of [[4, 4, 1, 1], [w - 4, 4, -1, 1], [4, h - 4, 1, -1], [w - 4, h - 4, -1, -1]]) {
      c.beginPath();
      c.moveTo(x + dx * 26, y);
      c.lineTo(x, y);
      c.lineTo(x, y + dy * 26);
      c.stroke();
    }
    c.globalAlpha = 1;
    c.fillStyle = tint;
    c.font = '700 30px ui-monospace, monospace';
    c.textBaseline = 'top';
    c.fillText(svc.service.slice(0, 16), 16, 14);
    c.fillStyle = css(colorOf(state));
    c.font = '24px ui-monospace, monospace';
    c.fillText(state || '…', 16, 52);
    c.fillStyle = tint;
    for (let b = 0; b < 14; b++) {
      const v = state === 'running' ? 0.25 + 0.75 * Math.abs(Math.sin(t * 1.6 + b * 0.7 + i)) : 0.12;
      const bh = v * 50;
      c.globalAlpha = 0.5 + v * 0.5;
      c.fillRect(16 + b * 21, h - 14 - bh, 14, bh);
    }
    c.globalAlpha = 1;
    holoTex.needsUpdate = true;
  };
  const base = new THREE.Color();
  g.set = (s) => {
    if (svcState(s.state) === state) return; // unchanged: keep the drawn face
    state = svcState(s.state);
    base.setHex(colorOf(state)).multiplyScalar(1.6);
    lights.color.copy(base);
    shot.image = textCanvas([{ text: state, color: css(colorOf(state)) }], { w: 256, h: 96, size: 24, title: svc.service.slice(0, 14) });
    shot.needsUpdate = true;
    drawHolo(0);
  };
  g.tick = (dt, t) => {
    if (state === 'running') lights.color.copy(base).multiplyScalar(0.75 + Math.sin(t * 3 + i) * 0.25);
    if ((age += dt) > 0.15 && state === 'running') {
      age = 0;
      drawHolo(t);
    }
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
    if (line && (line + ' ' + w).length > n) {
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
  put(k, scene, 'Deco_NeonSign', 2.0, 2.6, -7.97);
  const name = label(prefix, { size: 0.5, color: css(deco.accent) });
  name.position.set(2.0, 3.45, -7.6);
  scene.add(name);
  put(k, scene, 'Furn_Desk', -7.0, 0, 4.0);
  put(k, scene, 'Tech_Laptop', -7.0, k.point('Furn_Desk', 'top')[1], 4.0, PI);
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
  const cafeTicks = cafe(k, scene, deco, which === 'gecko');
  const big = lounge(k, scene, deco, ui, deco.accent);
  const holo = meeting(k, scene, deco);
  const gameTicks = gameRoom(k, scene, deco);
  support(k, scene, deco);
  const res = residents(k, scene, deco, ui, 2);
  res.start();

  const ticks = [...sh.ticks, ...res.ticks, holo.tick, ...cafeTicks, ...gameTicks, workTick, (dt) => { beacons.rotation.y += dt * 0.25; }];
  let beaconKey = '';
  const update = (st) => {
    const lines = st.worktrees.map((w, i) => ({
      text: `${String(i + 1).padStart(2)} ${w.base ? '★' : '·'} ${w.slug.padEnd(18).slice(0, 18)} ${w.stack.padEnd(8)} ${w.agent ? '◆ ' + w.agent.state : ''}`,
      color: css(colorOf(w.stack)),
    }));
    view.update(lines.length ? lines : ['no worktrees'], `${prefix} · ${st.worktrees.length} floors`);
    const open = st.worktrees.filter((w) => w.pr);
    holo.set(st.worktrees.map((w) => ({
      label: w.slug, color: colorOf(w.stack), tip: `${w.slug}: ${w.stack}`, act: () => ui.toFloor(w.slug),
    })), prefix, [`${st.worktrees.length} floors · ${st.worktrees.filter((w) => w.stack === 'up').length} up`, `${open.length} with an open PR`, `${st.worktrees.filter((w) => w.agent).length} agents`]);
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
