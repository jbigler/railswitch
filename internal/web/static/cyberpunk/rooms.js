// The tower's rooms: the lobby (the project) and a floor per worktree, built
// from the office kit (D145). A room is {scene, view, walk, update(state),
// ticks, name}: update redraws what the state changes (rack lights, screens,
// the agent) without building the room again. The starship's rooms (D144)
// with the bridge as the lobby, labs as floors and the transporter as the
// elevator.
//
// A floor's plan (16 × 12 m, 4 m high; x right, z towards the front):
//   back-left   server area: two rows of racks across an aisle, hazard-taped
//   back-right  work area: your desk and the agent's on a rug; the PR station
//   front-left  the big screen on the left wall, a lounge facing it
//   front-right the floor's pet
// The elevator is in the back wall at x = -2; androids wander everywhere.
import { THREE, kit, screen, label, rng, hash, setElevatorDoors, createRoamer, createFlyer } from './world.js';
import { agentAndroid, wanderer } from './actors.js';

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
export const KIT_NAMES = ['Arch_Floor_4m', 'Arch_Wall_Plain_4m', 'Arch_Wall_Window_4m', 'Arch_Elevator_Floor', 'Arch_Elevator_Cab',
  'Ext_CityBackdrop', 'Arch_CeilingDuct_4m', 'Arch_Ceiling_PipeRun_4m', 'Arch_Ceiling_CableTray_4m',
  'Arch_Ceiling_Sprinkler_4m', 'Arch_Ceiling_HangingCables', 'Arch_Ceiling_Fan', 'Arch_Ceiling_Vent', 'Arch_WallPipes_4m',
  'Light_CeilingPanel', 'Light_PendantBox', 'Light_HexCluster',
  'Tech_ServerRack', 'Tech_ServerRack_B', 'Tech_NetworkShelf', 'Tech_ToolCart', 'Tech_TerminalKiosk', 'Tech_MonitorBank',
  'Tech_MonitorStack', 'Tech_Laptop', 'Tech_MonitorWall_A',
  'Furn_Workstation_Lived', 'Furn_Workstation_Lived_B', 'Furn_Desk', 'Furn_CoffeeTable',
  'Deco_Boxes', 'Deco_Crate', 'Deco_MiniFridge', 'Deco_CorkBoard', 'Deco_Poster', 'Deco_NeonSign',
  'Deco_Plant_Floor', 'Deco_Plant_Floor_B', 'Deco_HangingPlanter', 'Deco_Extinguisher', 'Deco_CableRun', 'Deco_DeskLamp', 'Deco_Mug',
  'Pet_Cat_Walk', 'Pet_Cat_Sitting', 'Pet_Dog_Walk', 'Pet_Dog_Lying', 'Furn_DogBed', 'Deco_PetBowls', 'Pet_Parrot_Fly',
  'Furn_ParrotPerch', 'Pet_Cat_Curled', 'Pet_Aquarium', 'Pet_JellyfishTank', 'Pet_Gecko_Terrarium',
  'Char_Android_Agent', 'Char_Android_A', 'Char_Android_B', 'Char_Android_C', // the agent's is spawned in actors.js
  'Furn_Sofa_Tufted_L', 'Furn_Armchair', 'Furn_Divider_Glass', 'Furn_Divider_Shelf', 'Furn_Divider_Planter',
  'Deco_Plant_Snake', 'Deco_Plant_Monstera', 'Deco_Plant_Palm', 'Deco_Plant_Bush', 'Deco_Plant_Trailing',
  'Furn_SideTable', 'Deco_FloorLamp', 'Deco_Rug_Geo', 'Deco_CoatRack', 'Deco_Whiteboard', 'Deco_Backpack', 'Furn_Stool',
  'Deco_BookStack', 'Deco_PizzaBoxes'];

// NEON is the highlight colours a floor takes: cyberpunk's, not any hue.
const NEON = [0x22e6ff, 0xff2bd6, 0x8b5cff, 0xff7a1a, 0x7dff3a, 0xff2b4a, 0x3a7bff, 0x1affb2];

// decor is a floor's look, from its name: "randomly consistent".
function decor(name) {
  const seed = hash(name);
  const r = rng(seed);
  const i = Math.floor(r() * NEON.length);
  return {
    seed, r,
    accent: NEON[i],
    secondary: NEON[(i + 2 + Math.floor(r() * (NEON.length - 3))) % NEON.length],
    pick: (list) => list[Math.floor(r() * list.length)],
  };
}

// glowFor is the neon strength that makes a colour look as bright as the
// others: green and cyan are far brighter to the eye than blue or magenta.
function glowFor(hex) {
  const c = new THREE.Color(hex);
  const lum = 0.2126 * c.r + 0.7152 * c.g + 0.0722 * c.b;
  return Math.min(1.6, Math.max(0.8, 0.35 / lum));
}

function put(k, parent, name, x, y, z, ry = 0) {
  const o = k.spawn(name);
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

const FLOOR = { minX: -7.85, maxX: 7.85, minZ: -5.85, maxZ: 5.85, y: 0 };
const AREAS = [[FLOOR.minX, FLOOR.maxX, FLOOR.minZ, FLOOR.maxZ], [-2.95, -1.05, -8.2, FLOOR.minZ]];
const LIFT = { x: -2, z: -6 };

// shell builds a floor's floor, walls, elevator, windows onto the city,
// ceiling and its lights (the ceiling shows in first person only).
function shell(k, scene, deco, ui) {
  scene.background = new THREE.Color(0x04050a);
  for (const x of [-6, -2, 2, 6]) for (const z of [-4, 0, 4]) put(k, scene, 'Arch_Floor_4m', x, 0, z);
  const walls = [];
  const wall = (name, x, z, ry) => { const w = put(k, scene, name, x, 0, z, ry); walls.push(w); return w; };
  wall('Arch_Wall_Plain_4m', -6, -6, 0);
  const lift = wall('Arch_Elevator_Floor', LIFT.x, LIFT.z, 0);
  const cab = put(k, scene, 'Arch_Elevator_Cab', LIFT.x, 0, LIFT.z - 0.2);
  wall('Arch_Wall_Plain_4m', 2, -6, 0);
  wall('Arch_Wall_Plain_4m', 6, -6, 0);
  for (const z of [-4, 0, 4]) wall('Arch_Wall_Window_4m', 8, z, -PI / 2);
  for (const z of [-4, 0, 4]) wall('Arch_Wall_Plain_4m', -8, z, PI / 2);
  wall('Arch_Wall_Plain_4m', -6, 6, PI);
  wall('Arch_Wall_Plain_4m', -2, 6, PI);
  wall('Arch_Wall_Window_4m', 2, 6, PI);
  wall('Arch_Wall_Window_4m', 6, 6, PI);
  scene.userData.walls = walls;
  put(k, scene, 'Ext_CityBackdrop', 26, -4, 0, -PI / 2);
  put(k, scene, 'Ext_CityBackdrop', 4, -4, 24, PI);
  put(k, scene, 'Arch_WallPipes_4m', -2, 0, 5.99, PI);

  const ceiling = new THREE.Group();
  const top = new THREE.Mesh(new THREE.PlaneGeometry(16, 12), new THREE.MeshStandardMaterial({ color: 0x1a1c22, roughness: 0.9 }));
  top.rotation.x = PI / 2;
  top.position.y = 4;
  ceiling.add(top);
  for (const [x, z] of [[-5.6, -3.6], [-1.4, 1.2], [3.6, 1.2], [-5.4, 3.4], [2, -1.8]]) put(k, ceiling, 'Light_CeilingPanel', x, 4, z, PI / 2);
  for (const x of [1.7, 5.4]) put(k, ceiling, 'Light_PendantBox', x, 4, -4.4);
  put(k, ceiling, 'Arch_Ceiling_CableTray_4m', -5.6, 4, -3.6, PI / 2);
  put(k, ceiling, deco.pick(['Arch_CeilingDuct_4m', 'Arch_Ceiling_PipeRun_4m']), 2.5, 4, -0.2);
  put(k, ceiling, deco.pick(['Arch_Ceiling_Sprinkler_4m', 'Arch_Ceiling_HangingCables', 'Arch_Ceiling_PipeRun_4m']), 0, 4, 4.6);
  put(k, ceiling, 'Arch_Ceiling_Fan', 5, 4, 3.4);
  put(k, ceiling, 'Arch_Ceiling_Vent', -2, 4, -3);
  ceiling.visible = false;
  ceiling.userData.solid = true;
  scene.add(ceiling);
  scene.userData.ceiling = ceiling;

  const hemi = new THREE.HemisphereLight(0x9aa8c8, 0x202230, 2.4);
  scene.add(hemi);
  scene.userData.hemi = hemi;
  const moon = new THREE.DirectionalLight(0xb8c8ff, 0.8); // the city's glow
  moon.position.set(8, 14, 6);
  scene.add(moon);
  for (const [x, z] of [[-5.6, -3.6], [-1.4, 1.2], [3.6, 1.2], [-5.4, 3.4], [2, -1.8], [1.7, -4.4], [5.4, -4.4], [5.5, 4]]) {
    const l = new THREE.PointLight(0xe4ecff, 7.5, 11);
    l.position.set(x, 2.8, z); // low enough to light the room, not a hotspot on the ceiling
    scene.add(l);
  }
  for (const [x, z] of [[-7.5, -5.5], [7.5, 5.5]]) {
    const l = new THREE.PointLight(deco.accent, 3, 7);
    l.position.set(x, 1.2, z);
    scene.add(l);
  }
  // Thin neon far away blows out to white at the kit's strength: this view
  // sets it by the colour's brightness, bloom carrying the glow.
  scene.traverse((o) => {
    const n = o.material?.name ?? '';
    if (n === 'RS_Neon_Accent') o.material.emissiveIntensity = glowFor(deco.accent);
    if (n === 'RS_Neon_Secondary') o.material.emissiveIntensity = glowFor(deco.secondary);
    if (n === 'RS_LightPanel') o.material.emissiveIntensity = 0.5; // the lamps light the room; the panels only read as lit
  });

  // The elevator: its doors open as the walker comes near; the pad in front
  // of them opens the floor list.
  const doorCols = [];
  for (const o of [lift, cab]) o.traverse((c) => { if (/Door_[LR]_COL\d*$/.test(c.name)) doorCols.push(c); });
  scene.userData.movingBoxes = () => doorCols.map((c) => new THREE.Box3().setFromObject(c, true));
  pickable(lift, 'Elevator: go to the lobby or another floor', ui.transport);
  const sign = label('Elevator', { size: 0.3 });
  sign.position.set(LIFT.x, 3.0, LIFT.z + 0.3);
  scene.add(sign);
  let open = 0;
  const doors = (dt) => {
    const v = ui.viewer();
    const want = v && Math.hypot(v.x - LIFT.x, v.z - LIFT.z) < 2.8 ? 1 : 0;
    open += Math.max(-dt, Math.min(dt, want - open));
    setElevatorDoors(lift, open);
    setElevatorDoors(cab, open);
  };
  return { ticks: [doors], pad: { x: LIFT.x, z: LIFT.z + 0.75, r: 0.55, act: ui.transport } };
}

// hazard tapes a rectangle's open sides on the floor, yellow and black, with
// a gap (entrance) along the front side from x g0 to g1.
function hazard(scene, x0, x1, z0, z1, g0, g1) {
  const yellow = new THREE.MeshStandardMaterial({ color: 0xd9b310, roughness: 0.6 });
  const black = new THREE.MeshStandardMaterial({ color: 0x111111, roughness: 0.6 });
  const dash = (x, z, alongX, i) => box(scene, alongX ? 0.25 : 0.08, 0.006, alongX ? 0.08 : 0.25, i % 2 ? black : yellow, x, 0.004, z);
  let i = 0;
  for (let x = x0 + 0.125; x < x1; x += 0.25) if (x < g0 || x > g1) dash(x, z1, true, i++);
  for (let z = z0 + 0.125; z < z1; z += 0.25) dash(x1, z, false, i++);
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

// wallScreen is a canvas screen on the back wall above a desk.
function wallScreen(scene, x, y, w, h) {
  return bigScreen(scene, x, y, -5.92, 0, w, h, 0x333344).screen;
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

// The pets: one per floor, picked by the floor's name, in the floor's
// front-right (x 2–7.5, z 0.5–5.8).
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
  const which = Object.keys(PETS)[deco.seed % 7];
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
  switch (which) {
    case 'cat':
      put(k, scene, 'Deco_PetBowls', 3.4, 0, 3.6);
      pair('Pet_Cat_Walk', 'Pet_Cat_Sitting', { start: { x: 3, z: 2, ry: 0 }, speed: 0.5, stride: 0.3 });
      break;
    case 'dog':
      put(k, scene, 'Furn_DogBed', 3.4, 0, 2.8, -0.6);
      put(k, scene, 'Deco_PetBowls', 4.9, 0, 4.2);
      pair('Pet_Dog_Walk', 'Pet_Dog_Lying', { start: { x: 3.4, z: 2.8, ry: -0.6 }, homes: [{ x: 3.4, z: 2.8, ry: -0.6 }], speed: 0.7, stride: 0.45, height: 0.6 });
      break;
    case 'parrot': {
      const perches = [{ x: 7, y: 1.284, z: 1.2, ry: -PI / 2 }, { x: 3, y: 1.284, z: 4.8, ry: PI }];
      for (const p of perches) put(k, scene, 'Furn_ParrotPerch', p.x, 0, p.z, p.ry);
      const bird = mine(k.spawn('Pet_Parrot_Fly'));
      scene.add(bird);
      const f = createFlyer({ bird, perches, rng: rng(deco.seed) });
      ticks.push((dt) => f.update(dt));
      break;
    }
    case 'sleeper':
      put(k, scene, 'Deco_Crate', 6.6, 0, 2.4, -0.3);
      mine(put(k, scene, 'Pet_Cat_Curled', 6.6, 0.46, 2.4, 0.4));
      break;
    case 'aquarium':
      mine(put(k, scene, 'Pet_Aquarium', 3.4, 0, 5.65, PI));
      break;
    case 'jellyfish':
      mine(put(k, scene, 'Pet_JellyfishTank', 4, 0, 3));
      break;
    default:
      put(k, scene, 'Furn_Desk', 5.6, 0, 5.4, PI);
      mine(put(k, scene, 'Pet_Gecko_Terrarium', 5.6, 0.75, 5.4, PI));
  }
  // the androids: each a roamer that walks wherever the floor is free
  const models = ['Char_Android_A', 'Char_Android_B', 'Char_Android_C'];
  const starts = [{ x: 0, z: 0.5 }, { x: -1, z: 4 }, { x: 4.5, z: -1 }];
  for (let i = 0; i < androids; i++) {
    const a = wanderer(k, models[(deco.seed + i) % 3], rng(deco.seed + 31 * i), ui.say);
    scene.add(a);
    roam(a, null, { start: { ...starts[i], ry: deco.r() * PI * 2 }, speed: 0.8, stride: 1.1, height: 1.9, radius: 0.3, idle: [3, 10], shy: 1.4 });
  }
  return { ticks, start: () => later.forEach((f) => f()) };
}

// Decor every floor gets, some of it picked by the floor's name: plants in
// every corner and hanging over the seats, and the odds and ends of a room
// people work in.
function furnish(k, scene, deco) {
  const plants = ['Deco_Plant_Palm', 'Deco_Plant_Monstera', 'Deco_Plant_Snake', 'Deco_Plant_Bush', 'Deco_Plant_Floor', 'Deco_Plant_Floor_B'];
  put(k, scene, deco.pick(plants), 7.3, 0, -5.3, deco.r() * 6);
  put(k, scene, deco.pick(plants), -7.3, 0, 5.3, deco.r() * 6);
  put(k, scene, deco.pick(plants), 7.3, 0, -1.2, deco.r() * 6);
  put(k, scene, 'Deco_Plant_Snake', -0.7, 0, -5.5, deco.r() * 6);
  put(k, scene, 'Deco_Plant_Snake', -0.3, 0, 5.45, deco.r() * 6);
  for (const [x, z] of [[-4.6, 4.6], [3.6, -5.2], [7.3, -3]]) put(k, scene.userData.ceiling, 'Deco_Plant_Trailing', x, 4, z, deco.r() * 6);
  put(k, scene, 'Deco_HangingPlanter', 7.2, 4, 0.2);
  put(k, scene, 'Deco_MiniFridge', -1.2, 0, 5.45, PI);
  put(k, scene, 'Furn_Stool', -2.1, 0, 4.7);
  put(k, scene, deco.pick(['Deco_Boxes', 'Deco_Crate']), -2.7, 0, 5.3, PI + 0.3);
  put(k, scene, 'Deco_CorkBoard', -6, 1.7, 5.98, PI);
  put(k, scene, 'Deco_Poster', -0.3, 1.8, -5.97);
  put(k, scene, 'Deco_Poster', 7.98, 1.8, -1.9, -PI / 2);
  put(k, scene, 'Deco_Extinguisher', -0.6, 0, 5.94, PI);
  put(k, scene, 'Deco_CableRun', 0.6, 0, -1.2, 0.3);
  put(k, scene, 'Deco_CoatRack', 0.35, 0, -5.5);
}

// lounge furnishes the front-left: the tufted sectional facing the big
// screen on the left wall, on a rug, an armchair, a side table, an arc lamp,
// and what people leave behind.
function lounge(k, scene, deco) {
  put(k, scene, 'Deco_Rug_Geo', -5.6, 0, 2.7, PI / 2);
  put(k, scene, 'Furn_Sofa_Tufted_L', -4.0, 0, 2.5, -PI / 2);
  put(k, scene, 'Furn_CoffeeTable', -5.9, 0, 2.0, PI / 2);
  put(k, scene, 'Deco_BookStack', -5.8, 0.42, 1.8);
  put(k, scene, 'Furn_Armchair', -5.6, 0, 0.55, 0.25);
  put(k, scene, 'Furn_SideTable', -3.6, 0, 0.75);
  put(k, scene, 'Deco_FloorLamp', -3.3, 0, 4.4, -PI / 2);
  put(k, scene, 'Deco_PizzaBoxes', -3.0, 0, 1.4, deco.r());
  put(k, scene, 'Deco_Backpack', -3.25, 0, 3.6, -1.2);
  put(k, scene, 'Furn_Divider_Shelf', -1.6, 0, 3.0, PI / 2);
}

// nook is a meeting corner in the middle of a floor: two armchairs across a
// low table on a rug, a laptop left open; and a screen wall by the front
// windows.
function nook(k, scene, deco) {
  put(k, scene, 'Deco_Rug_Geo', 0.9, 0, 2.4, PI / 2);
  put(k, scene, 'Furn_CoffeeTable', 0.9, 0, 2.4, PI / 2);
  put(k, scene, 'Furn_Armchair', 0.9, 0, 1.15, deco.r() * 0.4 - 0.2);
  put(k, scene, 'Furn_Armchair', 0.9, 0, 3.65, PI + deco.r() * 0.4 - 0.2);
  put(k, scene, 'Tech_Laptop', 0.8, 0.42, 2.3, 0.4);
  put(k, scene, 'Deco_Mug', 1.1, 0.42, 2.7);
  put(k, scene, 'Deco_Mug', 0.7, 0.42, 2.0);
  put(k, scene, 'Tech_MonitorWall_A', 7.25, 0, 4.4, -PI / 2);
  put(k, scene, 'Deco_Plant_Bush', 2.0, 0, 5.35, deco.r() * 6);
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
  sign.position.set(3.5, 3.6, -5.6);
  scene.add(sign);

  // Server area (back-left): two rows of racks across an aisle.
  const racks = new Map();
  const rackSpot = (i) => {
    const j = Math.floor(i / 2);
    return i % 2 === 0 ? { x: -7.45, z: -5.25 + j * 0.68, ry: PI / 2 } : { x: -4.35, z: -5.25 + j * 0.68, ry: -PI / 2 };
  };
  hazard(scene, -7.95, -3.6, -5.95, -1.2, -6.6, -5.2);
  const keepOut = new THREE.Mesh(new THREE.BoxGeometry(4.4, 2, 4.8), new THREE.MeshBasicMaterial());
  keepOut.name = 'PetKeepOut_COL'; // pets and androids stay out of the server area; you don't
  keepOut.visible = false;
  keepOut.position.set(-5.75, 1, -3.55);
  scene.add(keepOut);
  const roomSign = label('Server area', { size: 0.26 });
  roomSign.position.set(-5.75, 3.3, -1.4);
  scene.add(roomSign);
  put(k, scene, 'Tech_NetworkShelf', -5.9, 0, -5.99);
  put(k, scene, 'Deco_CableRun', -5.9, 0, -3.3, PI / 2);
  put(k, scene, 'Tech_ToolCart', -3.0, 0, -0.5, 0.4);
  put(k, scene, 'Furn_Divider_Glass', -3.35, 0, -4.7, PI / 2);  // the server area's side, by the elevator
  put(k, scene, 'Furn_Divider_Shelf', -3.35, 0, -2.6, PI / 2);

  // Work area (back-right): your desk and the agent's, on a rug, between a
  // glass screen and a planter.
  rug(scene, 0.1, 7.9, -5.95, -2.7, deco.accent);
  put(k, scene, 'Furn_Divider_Glass', 0.05, 0, -3.95, PI / 2);
  put(k, scene, 'Furn_Divider_Planter', 6.4, 0, -2.25, PI);
  put(k, scene, 'Deco_Whiteboard', 7.35, 0, -3.6, -PI / 2);
  put(k, scene, 'Deco_Backpack', 3.0, 0, -3.7, 0.7);
  put(k, scene, 'Deco_BookStack', 2.75, 0.75, -4.9, 0.3);
  const desk = put(k, scene, deco.pick(['Furn_Workstation_Lived', 'Furn_Workstation_Lived_B']), 1.7, 0, -4.7);
  pickable(desk, 'Your desk: terminal, Neovim, links', () => ui.desk(slug));
  const mine = pickable(wallScreen(scene, 1.7, 2.65, 2.2, 1.2), 'Your desk: terminal, Neovim, links', () => ui.desk(slug));
  const agentDesk = put(k, scene, 'Furn_Desk', 5.4, 0, -4.95);
  put(k, scene, 'Tech_MonitorStack', 5.4, 0.75, -4.75);
  put(k, scene, 'Deco_DeskLamp', 6.4, 0.75, -5.05, 2.6);
  put(k, scene, 'Deco_Mug', 4.6, 0.75, -4.7);
  pickable(agentDesk, 'Agent console: open it in a terminal', () => ui.agentDesk(slug));
  const theirs = pickable(wallScreen(scene, 5.4, 2.7, 1.6, 0.9), 'Agent console: open it in a terminal', () => ui.agentDesk(slug));
  const partner = agentAndroid(k);
  partner.position.set(5.4, 0, -3.95);
  partner.rotation.y = PI; // faces its monitors
  partner.userData.block = true;
  pickable(partner, 'Your agent partner: status, screen, type to it', () => ui.agent(slug));
  scene.add(partner);

  // The PR station: this floor's pull request, readable from the room.
  const pr = station(k, scene, 3.6, -1.9, 0, 'Pull request', 'Pull request: open it, or all of them', () => ui.prs(), 2.0, 1.1);
  put(k, scene.userData.ceiling, 'Light_HexCluster', 3.6, 4, -1.2);

  // The big screen (left wall, front half) and the lounge facing it.
  const big = bigScreen(scene, -7.88, 2.2, 2.7, PI / 2, 4.6, 2.4, deco.accent);
  pickable(big.screen, 'The big screen', () => ui.say('This screen is yours: what goes up here comes later.', big));
  lounge(k, scene, deco);
  nook(k, scene, deco);

  furnish(k, scene, deco);
  const res = residents(k, scene, deco, ui, 1 + (deco.seed % 2));
  res.start();

  const ticks = [...sh.ticks, partner.tick, ...res.ticks];
  const update = (st) => {
    const w = st.worktrees.find((x) => x.slug === slug);
    if (!w) return;
    w.services.forEach((svc, i) => {
      let rk = racks.get(svc.service);
      if (!rk) {
        rk = rack(k, svc, i);
        const s = rackSpot(i);
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
    const services = w.services.length;
    const up = w.services.filter((s) => svcState(s.state) === 'running').length;
    big.screen.update([
      { text: `stack ${w.stack} · ${up}/${services} services running`, color: css(colorOf(w.stack)) },
      w.pr ? `PR #${w.pr.number} · checks ${w.pr.checks || 'none'} · review ${(w.pr.review || 'none').replace('_', ' ')}` : 'no pull request',
      w.agent ? `agent ${w.agent.name}: ${w.agent.state}` : 'no agent running',
      '',
      { text: 'Yours to fill: pin what matters here.', color: '#8b8fb8' },
    ], slug);
    const a = w.agent;
    partner.set(a ? a.state : 'off');
    theirs.update(a ? [{ text: a.state, color: '#ffe08a' }, ...(a.message ? wrap(a.message, 30, 4) : [])] : ['no agent running'], a ? a.name : 'agent');
  };
  const walk = { areas: AREAS, start: { x: LIFT.x, z: LIFT.z + 1.7, yaw: PI }, pads: [sh.pad] };
  return { scene, view: { target: new THREE.Vector3(0, 0.5, 0), yaw: 0.2, pitch: 0.85, dist: 17 }, walk, update, ticks, name: `Floor ${n} · ${slug}` };
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

// lobby builds the project's floor. ui: {transport, toFloor(slug), prs(),
// issues(), notes(), config(), say(text, obj), viewer()}.
export async function lobby(prefix, ui) {
  const k = await kit(KIT_NAMES);
  const deco = decor('lobby:' + prefix);
  k.setAccent(css(deco.accent));
  k.setSecondary(css(deco.secondary));
  const scene = new THREE.Scene();
  const sh = shell(k, scene, deco, ui);

  // Back wall: the floors' board over a bank of monitors, the project's sign.
  put(k, scene, 'Tech_MonitorBank', 3.6, 0, -5.97);
  const view = screen(5.6, 1.4, { size: 30, fg: '#c9f6ff' });
  view.position.set(3.6, 3.15, -5.9);
  scene.add(view);
  put(k, scene, 'Deco_NeonSign', -5.4, 2.3, -5.97);
  const name = label(prefix, { size: 0.55, color: css(deco.accent) });
  name.position.set(-5.4, 3.25, -5.6);
  scene.add(name);

  // The directory: a pedestal with a beacon per floor (click one to go there).
  const pedestal = new THREE.Group();
  const stand = new THREE.Mesh(new THREE.CylinderGeometry(0.45, 0.55, 0.9, 32), new THREE.MeshStandardMaterial({ color: 0x16181e, metalness: 0.6, roughness: 0.4 }));
  stand.position.y = 0.45;
  pedestal.add(stand);
  const rim = new THREE.Mesh(new THREE.TorusGeometry(0.47, 0.015, 6, 48), new THREE.MeshBasicMaterial({ color: new THREE.Color(deco.accent).multiplyScalar(1.4), toneMapped: false }));
  rim.rotation.x = PI / 2;
  rim.position.y = 0.9;
  pedestal.add(rim);
  pedestal.position.set(0, 0, 0.6);
  pedestal.userData.block = true;
  scene.add(pedestal);
  const beacons = new THREE.Group();
  beacons.position.set(0, 1.0, 0.6);
  scene.add(beacons);
  const dirSign = label('Directory', { size: 0.3 });
  dirSign.position.set(0, 2.1, 0.6);
  scene.add(dirSign);

  // The PR board on the left wall, its kiosk below it; the other stations.
  const board = bigScreen(scene, -7.88, 2.25, -2.6, PI / 2, 3.8, 1.9, deco.accent);
  pickable(board.screen, 'Pull requests', ui.prs);
  pickable(put(k, scene, 'Tech_TerminalKiosk', -6.9, 0, -2.6, PI / 2), 'Pull requests', ui.prs);
  const boardSign = label('Pull requests', { size: 0.34 });
  boardSign.position.set(-7.6, 3.45, -2.6);
  scene.add(boardSign);
  const issues = station(k, scene, -5.4, 1.4, 1.2, 'Issues', 'GitHub issues', ui.issues);
  const notes = station(k, scene, 5.6, -2.6, -0.7, 'Notes', "The agents' shared notes", ui.notes);
  const eng = station(k, scene, 5.6, 0.1, -1.3, 'Engineering', 'Project settings, images, library', ui.config);
  issues.update(['GitHub issues', 'click to scan'], 'Issues');
  notes.update(['shared agent notes', 'read · edit · add'], 'Notes');
  eng.update(['settings', 'images', 'library'], 'Engineering');

  // The big screen (front wall) and a lounge facing it; the concierge.
  const big = bigScreen(scene, -3.8, 2.2, 5.88, PI, 4.6, 2.4, deco.accent);
  pickable(big.screen, 'The big screen', () => ui.say('This screen is yours: what goes up here comes later.', big));
  put(k, scene, 'Deco_Rug_Geo', -3.8, 0, 3.6);
  put(k, scene, 'Furn_Sofa_Tufted_L', -4.2, 0, 2.6);
  put(k, scene, 'Furn_CoffeeTable', -4.4, 0, 4.3);
  put(k, scene, 'Furn_Armchair', -6.6, 0, 3.4, 0.5);
  put(k, scene, 'Furn_SideTable', -6.6, 0, 2.2);
  put(k, scene, 'Deco_FloorLamp', -2.0, 0, 1.9, -0.6);
  put(k, scene, 'Furn_Divider_Planter', -3.4, 0, 1.55, PI);
  put(k, scene, 'Deco_Plant_Palm', 1.4, 0, 5.3);
  put(k, scene, 'Deco_Plant_Monstera', 6.9, 0, -4.8);
  put(k, scene, 'Furn_Desk', 1.4, 0, -2.4);
  put(k, scene, 'Tech_Laptop', 1.2, 0.75, -2.3);
  const concierge = wanderer(k, 'Char_Android_B', rng(deco.seed + 7), ui.say);
  concierge.position.set(1.4, 0, -1.6);
  concierge.rotation.y = PI; // behind the desk, facing whoever steps out of the elevator
  concierge.userData.block = true;
  scene.add(concierge);
  const deskSign = label('Concierge', { size: 0.28 });
  deskSign.position.set(1.4, 2.2, -2.4);
  scene.add(deskSign);
  furnish(k, scene, deco);
  const res = residents(k, scene, deco, ui, 2);
  res.start();

  const ticks = [...sh.ticks, ...res.ticks, (dt) => { beacons.rotation.y += dt * 0.25; }];
  const update = (st) => {
    const lines = st.worktrees.map((w, i) => ({
      text: `${String(i + 1).padStart(2)} ${w.base ? '★' : '·'} ${w.slug.padEnd(18).slice(0, 18)} ${w.stack.padEnd(8)} ${w.agent ? '◆ ' + w.agent.state : ''}`,
      color: css(colorOf(w.stack)),
    }));
    view.update(lines.length ? lines : ['no worktrees'], `${prefix} · ${st.worktrees.length} floors`);
    const open = st.worktrees.filter((w) => w.pr);
    board.screen.update(open.length ? open.slice(0, 8).map((w) => ({
      text: `#${String(w.pr.number).padEnd(5)} ${(w.pr.checks || '·').padEnd(8)} ${(w.pr.review || '').replace('_', ' ').padEnd(18).slice(0, 18)} ${w.slug}`,
      color: checkColor[w.pr.checks] ?? '#c9f6ff',
    })) : [st.pr_error ? 'PRs unavailable' : 'no open pull requests'], 'Pull requests');
    beacons.clear();
    st.worktrees.forEach((w, i) => {
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
      `${open.length} open pull requests`,
      `${st.worktrees.filter((w) => w.agent).length} agents running`,
      '',
      { text: 'Yours to fill: pin what matters here.', color: '#8b8fb8' },
    ], prefix);
  };
  const walk = { areas: AREAS, start: { x: LIFT.x, z: LIFT.z + 1.7, yaw: PI }, pads: [sh.pad] };
  return { scene, view: { target: new THREE.Vector3(0, 0.5, 0), yaw: 0.15, pitch: 0.8, dist: 18, max: 30 }, walk, update, ticks, name: `Lobby · ${prefix}` };
}
