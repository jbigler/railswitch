// The ship's rooms: the bridge (the project) and a lab per worktree.
// A room is {scene, view, update(state)}: update redraws what the state
// changes (lights, screens, the crew) without building the room again.
import { THREE, S, model, tinted, screen, label, rng, hash } from './world.js';
import { cat, robot } from './actors.js';

// Service and stack states, coloured.
export const stateColors = {
  running: 0x63e07c, up: 0x63e07c, starting: 0xffc857, partial: 0xffc857, activating: 0xffc857,
  failed: 0xff6b6b, error: 0xff6b6b, stopped: 0x3b4558, 'on demand': 0x4a7bd1,
};
const colorOf = (s) => stateColors[s] ?? 0x3b4558;

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
const css = (n) => '#' + n.toString(16).padStart(6, '0');

function pickable(o, tip, act) {
  o.userData.pick = { tip, act };
  return o;
}

// stars is the space outside the windows.
function stars(r, n = 1500) {
  const pos = new Float32Array(n * 3);
  for (let i = 0; i < n; i++) {
    const v = new THREE.Vector3(r() * 2 - 1, r() * 2 - 1, r() * 2 - 1).normalize().multiplyScalar(120 + r() * 60);
    pos.set([v.x, v.y, v.z], i * 3);
  }
  const g = new THREE.BufferGeometry();
  g.setAttribute('position', new THREE.BufferAttribute(pos, 3));
  return new THREE.Points(g, new THREE.PointsMaterial({ color: 0xffffff, size: 0.6, sizeAttenuation: true }));
}

// shell builds a room's floor and its back and side walls (the front is
// open, a diorama): nx × nz tiles centred on the origin.
async function shell(scene, nx, nz, deco) {
  const W = (nx * S) / 2;
  const D = (nz * S) / 2;
  const jobs = [];
  for (let i = 0; i < nx; i++) {
    for (let j = 0; j < nz; j++) {
      const name = deco.floor(i, j);
      jobs.push(model(name).then((f) => {
        f.position.set(-W + S / 2 + i * S, -0.3 * S, -D + S / 2 + j * S);
        f.userData.solid = true;
        scene.add(f);
      }));
    }
  }
  const wall = (name, x, z, ry) => jobs.push(tinted(name, deco.wallTint).then((w) => {
    w.position.set(x, 0, z);
    w.rotation.y = ry;
    w.userData.solid = true;
    scene.add(w);
  }));
  for (let i = 0; i < nx; i++) wall(deco.backWall(i), -W + S / 2 + i * S, -D, 0);
  for (let j = 0; j < nz; j++) {
    wall(deco.sideWall(j), -W, -D + S / 2 + j * S, Math.PI / 2);
    wall(deco.sideWall(j + 7), W, -D + S / 2 + j * S, -Math.PI / 2);
  }
  await Promise.all(jobs);
  return { W, D };
}

function lights(scene, accent, r) {
  scene.add(new THREE.HemisphereLight(0xbfd4ff, 0x1a1f2b, 0.7));
  const key = new THREE.DirectionalLight(0xffffff, 1.2);
  key.position.set(6, 14, 10);
  key.castShadow = true;
  key.shadow.mapSize.set(2048, 2048);
  Object.assign(key.shadow.camera, { left: -16, right: 16, top: 16, bottom: -16 });
  scene.add(key);
  for (let i = 0; i < 3; i++) {
    const p = new THREE.PointLight(accent, 18, 14);
    p.position.set(-8 + i * 8 + r() * 2, 3.2, -2 + r() * 3);
    scene.add(p);
  }
}

// decor is a deck's look, from its name: "randomly consistent".
function decor(name) {
  const seed = hash(name);
  const r = rng(seed);
  const hue = r();
  const accent = new THREE.Color().setHSL(hue, 0.75, 0.58);
  const wallTint = new THREE.Color().setHSL(hue, 0.18, 0.86);
  const floors = [['floor', 'floor-panel'], ['floor-detail', 'floor'], ['floor-panel', 'floor-panel'], ['floor', 'floor']][Math.floor(r() * 4)];
  const windows = new Set([1 + Math.floor(r() * 2), 4 + Math.floor(r() * 2)]);
  const banners = Math.floor(r() * 8);
  return {
    seed, r, accent: accent.getHex(), wallTint: wallTint.getHex(),
    floor: (i, j) => floors[(i + j) % 2],
    backWall: (i) => (windows.has(i) ? 'wall-window' : i === banners ? 'wall-banner' : 'wall'),
    sideWall: (j) => ((j * 7 + seed) % 5 === 0 ? 'wall-pillar' : 'wall'),
  };
}

async function place(scene, name, x, z, ry = 0, y = 0) {
  const m = await model(name);
  m.position.set(x, y, z);
  m.rotation.y = ry;
  m.userData.block = true; // first person can't walk through it
  scene.add(m);
  return m;
}

// monitor is a desk monitor with a screen facing +z (towards the chair
// and the camera).
function monitor(w = 1.3, h = 0.8) {
  const g = new THREE.Group();
  const frame = new THREE.Mesh(new THREE.BoxGeometry(w + 0.08, h + 0.08, 0.06), new THREE.MeshStandardMaterial({ color: 0x1b2230, metalness: 0.5, roughness: 0.4 }));
  frame.castShadow = true;
  g.add(frame);
  const stand = new THREE.Mesh(new THREE.CylinderGeometry(0.04, 0.12, h * 0.6, 8), frame.material);
  stand.position.set(0, -h * 0.55, -0.12);
  g.add(stand);
  const sc = screen(w, h);
  sc.position.z = 0.032;
  g.add(sc);
  g.screen = sc;
  return g;
}

// rack is a server rack for one service, its lights the service's state.
function rack(svc) {
  const g = new THREE.Group();
  const body = new THREE.Mesh(new THREE.BoxGeometry(1.1, 2.3, 0.9), new THREE.MeshStandardMaterial({ color: 0x232a38, metalness: 0.6, roughness: 0.35 }));
  body.position.y = 1.15;
  body.castShadow = true;
  g.add(body);
  const leds = [];
  for (let i = 0; i < 6; i++) {
    const led = new THREE.Mesh(new THREE.BoxGeometry(0.8, 0.07, 0.02), new THREE.MeshStandardMaterial({ color: 0x111111, emissive: 0x000000, emissiveIntensity: 2 }));
    led.material.userData.own = true;
    led.position.set(0, 0.4 + i * 0.3, 0.46);
    leds.push(led);
    g.add(led);
  }
  const tag = label(svc.service, { size: 0.3 });
  tag.position.set(0, 2.6, 0);
  g.add(tag);
  let state = '';
  g.set = (s) => {
    state = svcState(s.state);
    for (const l of leds) l.material.emissive.setHex(colorOf(state));
  };
  g.tick = (dt, t) => {
    if (state !== 'running') return;
    leds.forEach((l, i) => { l.material.emissiveIntensity = 1.4 + Math.sin(t * (3 + i) + i) * 0.8; });
  };
  return g;
}

// transporter is the pad that opens the destination list.
function transporter(accent, act) {
  const g = new THREE.Group();
  const base = new THREE.Mesh(new THREE.CylinderGeometry(1.1, 1.25, 0.18, 32), new THREE.MeshStandardMaterial({ color: 0x2a3344, metalness: 0.7, roughness: 0.3 }));
  base.position.y = 0.09;
  g.add(base);
  const glowMat = new THREE.MeshBasicMaterial({ color: accent, transparent: true, opacity: 0.25, side: THREE.DoubleSide, depthWrite: false });
  glowMat.userData.own = true;
  const beam = new THREE.Mesh(new THREE.CylinderGeometry(0.95, 0.95, 3, 32, 1, true), glowMat);
  beam.position.y = 1.6;
  g.add(beam);
  const rings = [];
  for (let i = 0; i < 3; i++) {
    const ring = new THREE.Mesh(new THREE.TorusGeometry(0.95, 0.025, 6, 40), new THREE.MeshBasicMaterial({ color: accent }));
    ring.rotation.x = Math.PI / 2;
    rings.push(ring);
    g.add(ring);
  }
  const tag = label('Transporter', { size: 0.3 });
  tag.position.y = 3.4;
  g.add(tag);
  pickable(g, 'Transporter: go to the bridge or another lab', act);
  g.tick = (dt, t) => {
    rings.forEach((ring, i) => { ring.position.y = 0.2 + ((t * 0.8 + i / 3) % 1) * 2.9; });
    glowMat.opacity = 0.18 + Math.sin(t * 2) * 0.07;
  };
  return g;
}

function sky(scene, r) {
  scene.background = new THREE.Color(0x03050b);
  scene.add(stars(r));
}

// lab builds a worktree's lab. ui: {transport, service(slug, svc), desk(slug),
// agentDesk(slug), agent(slug), say(text, obj)}.
export async function lab(slug, ui) {
  const deco = decor(slug);
  const { r } = deco;
  const scene = new THREE.Scene();
  sky(scene, r);
  lights(scene, deco.accent, r);
  const { W, D } = await shell(scene, 8, 6, deco);
  const sign = label(slug, { size: 0.6, color: css(deco.accent) });
  sign.position.set(0, 2.9, -D + 0.3);
  scene.add(sign);

  // Server room: racks along the left wall, behind a rail.
  const racks = new Map();
  const rackSpot = (i) => ({ x: -W + 1.1 + (i % 2) * 1.6, z: -D + 1.0 + Math.floor(i / 2) * 1.6 });
  const roomSign = label('Server room', { size: 0.34 });
  roomSign.position.set(-W + 1.9, 3.3, -D + 0.4);
  scene.add(roomSign);
  for (let j = 0; j < 3; j++) await place(scene, 'rail', -W + 4.2, -D + 1 + j * 2, Math.PI / 2);

  // The desks: the user's, then the agent's.
  await place(scene, 'table-large', 0, -1.6);
  await place(scene, 'chair-armrest-headrest', 0, 0.2, Math.PI);
  await place(scene, 'computer-system', -1.9, -2.2);
  const mine = monitor(1.5, 0.85);
  mine.position.set(0, 1.55, -1.75);
  pickable(mine, 'Your desk: terminal, Neovim, links', () => ui.desk(slug));
  scene.add(mine);

  await place(scene, 'table', 4.2, -1.6);
  const partner = robot(r, deco.accent);
  partner.position.set(4.6, -0.2, -0.4);
  partner.rotation.y = Math.PI + 0.5;
  pickable(partner, 'Your agent partner: status, screen, type to it', () => ui.agent(slug));
  partner.userData.block = true;
  scene.add(partner);
  const theirs = monitor(1.1, 0.7);
  theirs.position.set(4.0, 1.45, -1.7);
  theirs.rotation.y = 0.35;
  pickable(theirs, "Agent console: open it in a terminal", () => ui.agentDesk(slug));
  scene.add(theirs);

  // Decor, from the name.
  for (let i = 0, n = 2 + Math.floor(r() * 4); i < n; i++) {
    const kind = ['container', 'container-tall', 'container-wide', 'pipe-ring-colored', 'computer'][Math.floor(r() * 5)];
    await place(scene, kind, W - 1 - r() * 2.5, -D + 0.9 + r() * 2.5, r() * Math.PI * 2);
  }
  await place(scene, 'display-wall-wide', 2.2, -D + 0.15, 0, 1.0);

  const pad = transporter(deco.accent, ui.transport);
  pad.position.set(W - 2.2, 0, D - 2.2);
  scene.add(pad);

  const pet = cat(r, { x0: -W + 5, x1: W - 3.5, z0: 0.6, z1: D - 1 }, ui.say);
  scene.add(pet);

  const ticks = [partner.tick, pad.tick, pet.tick];
  const update = (st) => {
    const w = st.worktrees.find((x) => x.slug === slug);
    if (!w) return;
    const shown = w.services;
    shown.forEach((svc, i) => {
      let rk = racks.get(svc.service);
      if (!rk) {
        rk = rack(svc);
        const p = rackSpot(i);
        rk.position.set(p.x, 0, p.z);
        pickable(rk, `${svc.service}: ${svcState(svc.state)}`, () => ui.service(slug, svc.service));
        rk.userData.block = true;
        racks.set(svc.service, rk);
        scene.add(rk);
        ticks.push(rk.tick);
      }
      rk.set(svc);
      rk.userData.pick.tip = `${svc.service}: ${svcState(svc.state)}`;
    });
    for (const [name, rk] of racks) {
      if (!shown.some((s) => s.service === name)) {
        scene.remove(rk);
        racks.delete(name);
      }
    }
    const linkLines = w.links.slice(0, 4).map((l) => ({ text: '↗ ' + l.label, color: '#9fc3ff' }));
    mine.screen.update([
      { text: `stack: ${w.stack}`, color: css(colorOf(w.stack)) },
      w.pr ? `PR #${w.pr.number} ${w.pr.state.toLowerCase()}` : 'no PR',
      ...linkLines,
    ], `⎇ ${w.branch || slug}`);
    const a = w.agent;
    partner.set(a ? a.state : 'off');
    theirs.screen.update(a ? [{ text: a.state, color: '#ffe08a' }, ...(a.message ? wrap(a.message, 30, 4) : [])] : ['no agent running'], a ? a.name : 'agent');
  };
  const walk = {
    bounds: { x0: -W + 0.5, x1: W - 0.5, z0: -D + 0.5, z1: D - 0.3 },
    start: { x: 0, z: D - 1, yaw: 0 },
    pads: [{ x: pad.position.x, z: pad.position.z, r: 0.8, act: ui.transport }],
  };
  return { scene, view: { target: new THREE.Vector3(0, 1, -0.5), yaw: 0.15, pitch: 0.5, dist: 16 }, walk, update, ticks, name: `Lab · ${slug}` };
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

// bridge builds the bridge. ui: {transport, toLab(slug), prs(), issues(),
// notes(), config(), say(text, obj)}.
export async function bridge(prefix, ui) {
  const deco = decor('bridge:' + prefix);
  const { r } = deco;
  const scene = new THREE.Scene();
  sky(scene, r);
  lights(scene, 0x7fb7ff, r);
  const { W, D } = await shell(scene, 10, 7, { ...deco, backWall: (i) => (i >= 2 && i <= 7 ? 'wall-window' : 'wall') });

  const view = screen(8, 2.2, { size: 30, fg: '#bfe9ff' });
  view.position.set(0, 3.0, -D + 0.25);
  scene.add(view);
  const title = label(`Bridge · ${prefix}`, { size: 0.55, color: '#9fe7ff' });
  title.position.set(0, 4.5, -D + 0.3);
  scene.add(title);

  await place(scene, 'chair-armrest-headrest', 0, 2.5, Math.PI);
  const holo = await place(scene, 'table-display-planet', 0, 0, 0, 0.6);
  holo.userData.solid = true;
  const beacons = new THREE.Group();
  beacons.position.set(0, 1.4, 0);
  scene.add(beacons);

  const station = async (x, z, ry, name, tip, act, glyph) => {
    const t = await place(scene, 'table-display', x, z, ry, 0.6);
    const sc = screen(1.6, 0.9);
    sc.position.set(x, 1.6, z);
    sc.rotation.y = ry;
    sc.translateZ(-0.2);
    sc.rotation.x = -0.25;
    pickable(sc, tip, act);
    pickable(t, tip, act);
    scene.add(sc);
    const l = label(`${glyph} ${name}`, { size: 0.32 });
    l.position.set(x, 2.5, z);
    scene.add(l);
    return sc;
  };
  const prs = await station(-5.5, -2.5, 0.6, 'Pull requests', 'Pull requests', ui.prs, '⇄');
  const issues = await station(-5.5, 1.8, 1.2, 'Issues', 'GitHub issues', ui.issues, '◉');
  const notes = await station(5.5, -2.5, -0.6, 'Notes', "The agents' shared notes", ui.notes, '✎');
  const eng = await station(5.5, 1.8, -1.2, 'Engineering', 'Project settings, images, library', ui.config, '⚙');
  issues.update(['GitHub issues', 'click to scan'], 'Issues');
  notes.update(['shared agent notes', 'read · edit · add'], 'Notes');
  eng.update(['settings', 'images', 'library'], 'Engineering');

  const door = await place(scene, 'door-double', W - 0.25, -D + 3, -Math.PI / 2);
  door.userData.block = false;
  pickable(door, 'Turbolift: go to a lab', ui.transport);
  const lift = label('Turbolift', { size: 0.3 });
  lift.position.set(W - 0.4, 2.0, -D + 3);
  scene.add(lift);
  const pad = transporter(0x7fb7ff, ui.transport);
  pad.position.set(-W + 2.2, 0, D - 2);
  scene.add(pad);

  const pet = cat(r, { x0: -W + 2, x1: W - 2, z0: -D + 2.5, z1: D - 1 }, ui.say);
  scene.add(pet);
  const ticks = [pad.tick, pet.tick, (dt) => { beacons.rotation.y += dt * 0.2; }];

  const update = (st) => {
    const lines = st.worktrees.map((w) => ({
      text: `${w.base ? '★' : '·'} ${w.slug.padEnd(18).slice(0, 18)} ${w.stack.padEnd(8)} ${w.agent ? '◆ ' + w.agent.state : ''}`,
      color: css(colorOf(w.stack)),
    }));
    view.update(lines.length ? lines : ['no worktrees'], `${prefix} · ${st.worktrees.length} labs`);
    const open = st.worktrees.filter((w) => w.pr);
    prs.update(open.length ? open.slice(0, 5).map((w) => `#${w.pr.number} ${w.pr.checks || ''} ${w.slug}`) : [st.pr_error ? 'unavailable' : 'no open PRs'], 'Pull requests');
    beacons.clear();
    st.worktrees.forEach((w, i) => {
      const a = (i / Math.max(1, st.worktrees.length)) * Math.PI * 2;
      const m = new THREE.Mesh(new THREE.CylinderGeometry(0.08, 0.08, 0.5 + (w.stack === 'up' ? 0.4 : 0), 8),
        new THREE.MeshStandardMaterial({ color: colorOf(w.stack), emissive: colorOf(w.stack), emissiveIntensity: 1.5 }));
      m.material.userData.own = true;
      m.position.set(Math.cos(a) * 0.7, 0.3, Math.sin(a) * 0.7);
      pickable(m, `Lab ${w.slug}: ${w.stack}`, () => ui.toLab(w.slug));
      beacons.add(m);
    });
  };
  const walk = {
    bounds: { x0: -W + 0.5, x1: W - 0.5, z0: -D + 0.5, z1: D - 0.3 },
    start: { x: 0, z: D - 0.8, yaw: 0 },
    pads: [
      { x: pad.position.x, z: pad.position.z, r: 0.8, act: ui.transport },
      { x: W - 0.8, z: -D + 3, r: 0.8, act: ui.transport }, // the turbolift
    ],
  };
  return { scene, view: { target: new THREE.Vector3(0, 1.2, -1), yaw: 0, pitch: 0.42, dist: 19, max: 30 }, walk, update, ticks, name: `Bridge · ${prefix}` };
}
