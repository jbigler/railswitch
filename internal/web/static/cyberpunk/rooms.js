// The tower's rooms: the lobby (the project) and a floor per worktree, built
// from the office kit (D145). A room is {scene, view, walk, update(state),
// ticks, name}: update redraws what the state changes (rack lights, screens,
// the agent) without building the room again. The starship's rooms (D144)
// with the bridge as the lobby, labs as floors and the transporter as the
// elevator.
import { THREE, kit, screen, label, rng, hash, setElevatorDoors, createRoamer, createFlyer } from './world.js';
import { robot } from './actors.js';

const PI = Math.PI;

// Service and stack states, coloured.
export const stateColors = {
  running: 0x63e07c, up: 0x63e07c, starting: 0xffc857, partial: 0xffc857, activating: 0xffc857,
  failed: 0xff6b6b, error: 0xff6b6b, stopped: 0x3b4558, 'on demand': 0x4a7bd1,
};
const colorOf = (s) => stateColors[s] ?? 0x3b4558;
const css = (n) => '#' + n.toString(16).padStart(6, '0');

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
const PET_PIECES = ['Pet_Cat_Walk', 'Pet_Cat_Sitting', 'Pet_Dog_Walk', 'Pet_Dog_Lying', 'Furn_DogBed', 'Deco_PetBowls',
  'Pet_Parrot_Fly', 'Furn_ParrotPerch', 'Pet_Cat_Curled', 'Deco_Crate', 'Pet_Aquarium', 'Pet_JellyfishTank', 'Pet_Gecko_Terrarium', 'Furn_Desk'];
export const KIT_NAMES = ['Arch_Floor_4m', 'Arch_Wall_Plain_4m', 'Arch_Wall_Window_4m', 'Arch_Elevator_Floor', 'Arch_Elevator_Cab',
  'Ext_CityBackdrop', 'Arch_Ceiling_LightRun_4m', 'Arch_CeilingDuct_4m', 'Arch_Ceiling_PipeRun_4m', 'Arch_Ceiling_CableTray_4m',
  'Arch_Ceiling_Sprinkler_4m', 'Arch_Ceiling_HangingCables', 'Arch_Ceiling_Fan', 'Arch_Ceiling_Vent', 'Arch_WallPipes_4m',
  'Tech_ServerRack', 'Tech_ServerRack_B', 'Furn_Workstation_Lived', 'Furn_Workstation_Lived_B', 'Furn_Workstation_3Mon',
  'Furn_Workstation_2Mon', 'Tech_TerminalKiosk', 'Tech_MonitorBank', 'Tech_MonitorWall_A', 'Tech_MonitorWall_B', 'Tech_ToolCart',
  'Tech_NetworkShelf', 'Furn_Sofa', 'Furn_Sofa_Short', 'Furn_CoffeeTable', 'Deco_Boxes', 'Deco_MiniFridge', 'Deco_CorkBoard',
  'Deco_Poster', 'Deco_NeonSign', 'Deco_Plant_Floor', 'Deco_Plant_Floor_B', 'Deco_HangingPlanter', 'Deco_Extinguisher',
  'Deco_CableRun', 'Deco_TrashBin', ...PET_PIECES];

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

// The floor plan: 16 × 12 m, 4 m high; the elevator in the back wall at
// x = -2 with its shaft behind; the server room along the left wall.
const FLOOR = { minX: -7.85, maxX: 7.85, minZ: -5.85, maxZ: 5.85, y: 0 };
const AREAS = [[FLOOR.minX, FLOOR.maxX, FLOOR.minZ, FLOOR.maxZ], [-2.95, -1.05, -8.2, FLOOR.minZ]];
const LIFT = { x: -2, z: -6 };

// shell builds a floor's floor, walls, elevator, windows onto the city,
// ceiling (first person only) and lights.
function shell(k, scene, deco, ui) {
  scene.background = new THREE.Color(0x04050a);
  for (const x of [-6, -2, 2, 6]) for (const z of [-4, 0, 4]) put(k, scene, 'Arch_Floor_4m', x, 0, z);
  const walls = [];
  const wall = (name, x, z, ry) => walls.push(put(k, scene, name, x, 0, z, ry));
  wall('Arch_Wall_Plain_4m', -6, -6, 0);
  wall('Arch_Elevator_Floor', LIFT.x, LIFT.z, 0);
  const lift = walls[walls.length - 1];
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

  const ceiling = new THREE.Group();
  const top = new THREE.Mesh(new THREE.PlaneGeometry(16, 12), new THREE.MeshStandardMaterial({ color: 0x0d0e12, roughness: 0.9 }));
  top.rotation.x = PI / 2;
  top.position.y = 4;
  ceiling.add(top);
  for (const x of [-4, 0, 4]) put(k, ceiling, 'Arch_Ceiling_LightRun_4m', x, 4, 0.3);
  put(k, ceiling, deco.pick(['Arch_CeilingDuct_4m', 'Arch_Ceiling_PipeRun_4m', 'Arch_Ceiling_CableTray_4m']), 2, 4, -3);
  put(k, ceiling, deco.pick(['Arch_Ceiling_CableTray_4m', 'Arch_Ceiling_Sprinkler_4m', 'Arch_Ceiling_HangingCables']), -2, 4, 3);
  put(k, ceiling, 'Arch_Ceiling_Fan', 4.5, 4, 3.4);
  put(k, ceiling, 'Arch_Ceiling_Vent', -5, 4, -1.5);
  ceiling.visible = false;
  ceiling.userData.solid = true;
  scene.add(ceiling);
  scene.userData.ceiling = ceiling;
  put(k, scene, 'Arch_WallPipes_4m', -2, 0, 5.99, PI);

  const hemi = new THREE.HemisphereLight(0x8090b0, 0x101018, 2);
  scene.add(hemi);
  scene.userData.hemi = hemi;
  const moon = new THREE.DirectionalLight(0xb8c8ff, 0.6); // the city's glow, so the overview reads
  moon.position.set(8, 14, 6);
  scene.add(moon);
  // Thin neon far away blows out to white at the kit's strength: this view
  // sets it by the colour's brightness, bloom carrying the glow.
  scene.traverse((o) => {
    const n = o.material?.name ?? '';
    if (n === 'RS_Neon_Accent') o.material.emissiveIntensity = glowFor(deco.accent);
    if (n === 'RS_Neon_Secondary') o.material.emissiveIntensity = glowFor(deco.secondary);
  });
  for (const [x, z] of [[-4, 0.3], [0, 0.3], [4, 0.3], [-4, -4], [3, -4], [3, 4], [-4, 4]]) {
    const l = new THREE.PointLight(0xdfe8ff, 5, 9);
    l.position.set(x, 3.2, z);
    scene.add(l);
  }
  for (const [x, z] of [[-6.5, -4.5], [6.5, 4.5]]) {
    const l = new THREE.PointLight(deco.accent, 3, 7);
    l.position.set(x, 1.2, z);
    scene.add(l);
  }

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

// The pets: one per floor, picked by the floor's name.
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

// pet places the floor's pet in the right-hand part of the floor (x 2–7.5,
// z 0.5–5.8, kept free of other furniture); finish() starts a roamer or a
// flyer once every collider is in place.
function pet(k, scene, deco, ui) {
  const which = Object.keys(PETS)[deco.seed % 7];
  const name = PET_NAMES[(deco.seed >>> 8) % PET_NAMES.length];
  const { kind, sound } = PETS[which];
  const tip = `${name} the ${kind}`;
  const mine = (o) => pickable(o, tip, () => ui.say(`${name}: ${sound}`, o));
  const ticks = [];
  let finish = () => {};
  const roam = (walkName, restName, opts) => {
    const walk = mine(k.spawn(walkName));
    const rest = mine(k.spawn(restName));
    scene.add(walk, rest);
    finish = () => {
      const r = createRoamer({ walk, rest, colliders: k.colliders(scene), bounds: FLOOR, rng: rng(deco.seed), ...opts });
      ticks.push((dt) => r.update(dt, ui.viewer()));
    };
  };
  switch (which) {
    case 'cat':
      put(k, scene, 'Deco_PetBowls', 3.4, 0, 3.6);
      roam('Pet_Cat_Walk', 'Pet_Cat_Sitting', { start: { x: 3, z: 2, ry: 0 }, speed: 0.5, stride: 0.3 });
      break;
    case 'dog':
      put(k, scene, 'Furn_DogBed', 3.4, 0, 2.6, -0.6);
      put(k, scene, 'Deco_PetBowls', 4.9, 0, 4);
      roam('Pet_Dog_Walk', 'Pet_Dog_Lying', { start: { x: 3.4, z: 2.6, ry: -0.6 }, homes: [{ x: 3.4, z: 2.6, ry: -0.6 }], speed: 0.7, stride: 0.45, height: 0.6 });
      break;
    case 'parrot': {
      const perches = [{ x: 6.9, y: 1.284, z: -1, ry: -PI / 2 }, { x: 3, y: 1.284, z: 4.6, ry: PI }];
      for (const p of perches) put(k, scene, 'Furn_ParrotPerch', p.x, 0, p.z, p.ry);
      const bird = mine(k.spawn('Pet_Parrot_Fly'));
      scene.add(bird);
      const f = createFlyer({ bird, perches, rng: rng(deco.seed) });
      ticks.push((dt) => f.update(dt));
      break;
    }
    case 'sleeper':
      put(k, scene, 'Deco_Crate', 6.6, 0, 2.2, -0.3);
      mine(put(k, scene, 'Pet_Cat_Curled', 6.6, 0.46, 2.2, 0.4));
      break;
    case 'aquarium':
      mine(put(k, scene, 'Pet_Aquarium', 1.2, 0, 5.65, PI));
      break;
    case 'jellyfish':
      mine(put(k, scene, 'Pet_JellyfishTank', 3.8, 0, 2.6));
      break;
    default:
      put(k, scene, 'Furn_Desk', 5.6, 0, 5.4, PI);
      mine(put(k, scene, 'Pet_Gecko_Terrarium', 5.6, 0.75, 5.4, PI));
  }
  return { ticks, finish: () => finish() };
}

// rack is a server rack for one service: its LEDs and edge lights show the
// service's state.
function rack(k, svc, i) {
  const g = k.spawn(i % 2 ? 'Tech_ServerRack_B' : 'Tech_ServerRack');
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
    base.setHex(colorOf(state)).multiplyScalar(2);
    lights.color.copy(base);
  };
  g.tick = (dt, t) => {
    if (state === 'running') lights.color.copy(base).multiplyScalar(0.75 + Math.sin(t * 3 + i) * 0.25);
  };
  return g;
}

// wallScreen is a canvas screen on the back wall, in a dark frame.
function wallScreen(scene, x, y, w, h) {
  const frame = new THREE.Mesh(new THREE.BoxGeometry(w + 0.1, h + 0.1, 0.06), new THREE.MeshStandardMaterial({ color: 0x0b0c10, roughness: 0.5 }));
  frame.position.set(x, y, -5.95);
  scene.add(frame);
  const sc = screen(w, h);
  sc.position.set(x, y, -5.915);
  scene.add(sc);
  return sc;
}

// The decor slots of a floor, filled by its name; the middle and the way
// from the elevator stay free.
function furnish(k, scene, deco) {
  switch (deco.pick(['lounge', 'boxes', 'wall', 'cart'])) {
    case 'lounge':
      put(k, scene, 'Furn_Sofa', -4.6, 0, 5.2, PI);
      put(k, scene, 'Furn_CoffeeTable', -4.6, 0, 3.9);
      break;
    case 'boxes':
      put(k, scene, 'Deco_Boxes', -5.6, 0, 5.1, PI + 0.2);
      put(k, scene, 'Deco_MiniFridge', -3.8, 0, 5.45, PI);
      break;
    case 'wall':
      put(k, scene, 'Tech_MonitorWall_A', -4.6, 0, 5.2, PI);
      break;
    default:
      put(k, scene, 'Tech_ToolCart', -5.2, 0, 4.9, PI + 0.3);
      put(k, scene, 'Deco_TrashBin', -3.9, 0, 5.3);
  }
  switch (deco.pick(['bank', 'kiosk', 'none', 'shelf'])) {
    case 'bank': put(k, scene, 'Tech_MonitorWall_B', -5.2, 0, -0.5, PI / 2); break;
    case 'kiosk': put(k, scene, 'Tech_TerminalKiosk', -5.6, 0, 0.6, PI / 2); break;
    case 'shelf': put(k, scene, 'Tech_NetworkShelf', -2, 0, 6, PI); break;
    default:
  }
  put(k, scene, deco.pick(['Deco_Plant_Floor', 'Deco_Plant_Floor_B']), 7.3, 0, -5.3);
  put(k, scene, 'Deco_Extinguisher', -3.2, 0, -5.92);
  put(k, scene, deco.r() < 0.5 ? 'Deco_CorkBoard' : 'Deco_Poster', -6, 1.7, 5.98, PI);
  put(k, scene, 'Deco_HangingPlanter', 7.2, 4, 1.8);
  put(k, scene, 'Deco_CableRun', 0.8, 0, -2.4, 0.3);
}

// lab builds a worktree's floor. ui: {transport, service(slug, svc),
// desk(slug), agentDesk(slug), agent(slug), say(text, obj), viewer()}.
export async function lab(slug, n, ui) {
  const k = await kit(KIT_NAMES);
  const deco = decor(slug);
  const { r } = deco;
  k.setAccent(css(deco.accent));
  k.setSecondary(css(deco.secondary));
  const scene = new THREE.Scene();
  const sh = shell(k, scene, deco, ui);
  const sign = label(`Floor ${n} · ${slug}`, { size: 0.5, color: css(deco.accent) });
  sign.position.set(3.5, 3.55, -5.6);
  scene.add(sign);

  // Server room: a rack per service along the left wall, fronts to the room.
  const racks = new Map();
  const rackSpot = (i) => (i < 16 ? { x: -7.45, z: -5.25 + i * 0.68, ry: PI / 2 } : { x: -5.4, z: -5.25 + (i - 16) * 0.68, ry: -PI / 2 });
  const zone = new THREE.Mesh(new THREE.BoxGeometry(1.15, 2, 11.8), new THREE.MeshBasicMaterial());
  zone.name = 'ServerRoom_COL'; // keeps pets and walkers out of the racks' row
  zone.visible = false;
  zone.position.set(-7.42, 1, 0);
  scene.add(zone);
  const roomSign = label('Server room', { size: 0.34 });
  roomSign.position.set(-7.3, 3.3, -5.4);
  scene.add(roomSign);

  // Your desk, then the agent's, against the back wall.
  const desk = put(k, scene, deco.pick(['Furn_Workstation_Lived', 'Furn_Workstation_Lived_B']), 1.6, 0, -4.6);
  pickable(desk, 'Your desk: terminal, Neovim, links', () => ui.desk(slug));
  const mine = pickable(wallScreen(scene, 1.6, 2.65, 2.2, 1.2), 'Your desk: terminal, Neovim, links', () => ui.desk(slug));
  const agentDesk = put(k, scene, 'Furn_Workstation_3Mon', 5.4, 0, -4.6);
  pickable(agentDesk, 'Agent console: open it in a terminal', () => ui.agentDesk(slug));
  const theirs = pickable(wallScreen(scene, 5.4, 2.55, 1.6, 0.9), 'Agent console: open it in a terminal', () => ui.agentDesk(slug));
  const partner = robot(r, deco.accent);
  partner.position.set(6.95, 0, -3.9);
  partner.rotation.y = -0.6;
  partner.userData.block = true;
  pickable(partner, 'Your agent partner: status, screen, type to it', () => ui.agent(slug));
  scene.add(partner);

  furnish(k, scene, deco);
  const p = pet(k, scene, deco, ui);
  p.finish();

  const ticks = [...sh.ticks, partner.tick, ...p.ticks];
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
    const a = w.agent;
    partner.set(a ? a.state : 'off');
    theirs.update(a ? [{ text: a.state, color: '#ffe08a' }, ...(a.message ? wrap(a.message, 30, 4) : [])] : ['no agent running'], a ? a.name : 'agent');
  };
  const walk = { areas: AREAS, start: { x: LIFT.x, z: LIFT.z + 1.7, yaw: PI }, pads: [sh.pad] };
  return { scene, view: { target: new THREE.Vector3(0, 0.5, 0), yaw: 0.2, pitch: 0.85, dist: 17 }, walk, update, ticks, name: `Floor ${n} · ${slug}` };
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

  put(k, scene, 'Tech_MonitorBank', 3.6, 0, -5.97);
  const view = screen(5.6, 1.4, { size: 30, fg: '#c9f6ff' });
  view.position.set(3.6, 3.15, -5.9);
  scene.add(view);
  const neon = put(k, scene, 'Deco_NeonSign', -5.4, 2.3, -5.97);
  neon.userData.solid = true;
  const name = label(prefix, { size: 0.55, color: css(deco.accent) });
  name.position.set(-5.4, 3.25, -5.6);
  scene.add(name);

  // The directory: a pedestal with a beacon per floor (click one to go there).
  const pedestal = new THREE.Group();
  const stand = new THREE.Mesh(new THREE.CylinderGeometry(0.45, 0.55, 0.9, 32), new THREE.MeshStandardMaterial({ color: 0x16181e, metalness: 0.6, roughness: 0.4 }));
  stand.position.y = 0.45;
  pedestal.add(stand);
  const rim = new THREE.Mesh(new THREE.TorusGeometry(0.47, 0.015, 6, 48), new THREE.MeshBasicMaterial({ color: new THREE.Color(deco.accent).multiplyScalar(3), toneMapped: false }));
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

  const station = (x, z, ry, title, tip, act) => {
    const kiosk = put(k, scene, 'Tech_TerminalKiosk', x, 0, z, ry);
    pickable(kiosk, tip, act);
    const sc = screen(1.5, 0.85);
    sc.position.set(x, 2.05, z);
    sc.rotation.y = ry;
    sc.translateZ(0.12);
    sc.rotateX(-0.15);
    pickable(sc, tip, act);
    scene.add(sc);
    const l = label(title, { size: 0.3 });
    l.position.set(x, 2.75, z);
    scene.add(l);
    return sc;
  };
  const prs = station(-5.4, -2.6, 0.7, 'Pull requests', 'Pull requests', ui.prs);
  const issues = station(-5.4, 1.6, 1.3, 'Issues', 'GitHub issues', ui.issues);
  const notes = station(5.6, -2.6, -0.7, 'Notes', "The agents' shared notes", ui.notes);
  const eng = station(5.6, 0.1, -1.3, 'Engineering', 'Project settings, images, library', ui.config);
  issues.update(['GitHub issues', 'click to scan'], 'Issues');
  notes.update(['shared agent notes', 'read · edit · add'], 'Notes');
  eng.update(['settings', 'images', 'library'], 'Engineering');

  put(k, scene, 'Furn_Sofa', -4.6, 0, 5.2, PI);
  put(k, scene, 'Furn_CoffeeTable', -4.6, 0, 3.9);
  put(k, scene, 'Deco_Plant_Floor', 7.3, 0, -5.3);
  put(k, scene, 'Deco_Plant_Floor_B', -7.3, 0, -5.3);
  put(k, scene, 'Deco_HangingPlanter', 7.2, 4, 1.8);
  const p = pet(k, scene, deco, ui);
  p.finish();

  const ticks = [...sh.ticks, ...p.ticks, (dt) => { beacons.rotation.y += dt * 0.25; }];
  const update = (st) => {
    const lines = st.worktrees.map((w, i) => ({
      text: `${String(i + 1).padStart(2)} ${w.base ? '★' : '·'} ${w.slug.padEnd(18).slice(0, 18)} ${w.stack.padEnd(8)} ${w.agent ? '◆ ' + w.agent.state : ''}`,
      color: css(colorOf(w.stack)),
    }));
    view.update(lines.length ? lines : ['no worktrees'], `${prefix} · ${st.worktrees.length} floors`);
    const open = st.worktrees.filter((w) => w.pr);
    prs.update(open.length ? open.slice(0, 5).map((w) => `#${w.pr.number} ${w.pr.checks || ''} ${w.slug}`) : [st.pr_error ? 'unavailable' : 'no open PRs'], 'Pull requests');
    beacons.clear();
    st.worktrees.forEach((w, i) => {
      const a = (i / Math.max(1, st.worktrees.length)) * PI * 2;
      const c = new THREE.Color(colorOf(w.stack));
      const m = new THREE.Mesh(new THREE.CylinderGeometry(0.06, 0.06, 0.4 + (w.stack === 'up' ? 0.35 : 0), 8),
        new THREE.MeshBasicMaterial({ color: c.multiplyScalar(2.5), toneMapped: false }));
      m.position.set(Math.cos(a) * 0.32, 0.25, Math.sin(a) * 0.32);
      pickable(m, `Floor ${i + 1} · ${w.slug}: ${w.stack}`, () => ui.toFloor(w.slug));
      beacons.add(m);
    });
  };
  const walk = { areas: AREAS, start: { x: LIFT.x, z: LIFT.z + 1.7, yaw: PI }, pads: [sh.pad] };
  return { scene, view: { target: new THREE.Vector3(0, 0.5, 0), yaw: 0.15, pitch: 0.8, dist: 18, max: 30 }, walk, update, ticks, name: `Lobby · ${prefix}` };
}
