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
// shell's walls are k Kenney walls tall (2k units), under a ceiling that
// shows only in first person (World.setMode).
async function shell(scene, nx, nz, deco, k = 1.5) {
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
    w.scale.y *= k;
    w.userData.solid = true;
    scene.add(w);
  }));
  for (let i = 0; i < nx; i++) wall(deco.backWall(i), -W + S / 2 + i * S, -D, 0);
  for (let j = 0; j < nz; j++) {
    wall(deco.sideWall(j), -W, -D + S / 2 + j * S, Math.PI / 2);
    wall(deco.sideWall(j + 7), W, -D + S / 2 + j * S, -Math.PI / 2);
  }
  await Promise.all(jobs);
  const H = S * k;
  const ceiling = new THREE.Group(); // and the front wall: first person only
  const panel = new THREE.MeshStandardMaterial({ color: deco.wallTint, roughness: 0.9, side: THREE.DoubleSide });
  panel.color.multiplyScalar(0.45);
  const top = new THREE.Mesh(new THREE.PlaneGeometry(W * 2 + 1, D * 2 + 1), panel);
  top.rotation.x = Math.PI / 2;
  top.position.y = H;
  ceiling.add(top);
  const strip = new THREE.MeshStandardMaterial({ color: 0xffffff, emissive: 0xdfeaff, emissiveIntensity: 1.4 });
  for (let x = -W + S; x < W; x += S * 2) {
    const s = new THREE.Mesh(new THREE.BoxGeometry(0.25, 0.05, D * 2 - 1.5), strip);
    s.position.set(x, H - 0.03, 0);
    ceiling.add(s);
  }
  const fronts = [];
  for (let i = 0; i < nx; i++) {
    fronts.push(tinted(i % 3 === 1 ? 'wall-window' : 'wall', deco.wallTint).then((w) => {
      w.position.set(-W + S / 2 + i * S, 0, D);
      w.rotation.y = Math.PI;
      w.scale.y *= k;
      ceiling.add(w);
    }));
  }
  await Promise.all(fronts);
  ceiling.userData.solid = true;
  ceiling.visible = false;
  scene.add(ceiling);
  scene.userData.ceiling = ceiling;
  return { W, D, H };
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
    const p = new THREE.PointLight(accent, 10, 12);
    p.position.set(-8 + i * 8 + r() * 2, 2.3, -2 + r() * 3);
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

// nebulaTexture paints an equirectangular nebula: soft clouds of two
// hues near the deck's own, dust lanes, and a band of faint stars.
function nebulaTexture(r, hue) {
  const c = document.createElement('canvas');
  c.width = 2048;
  c.height = 1024;
  const g = c.getContext('2d');
  g.fillStyle = '#020309';
  g.fillRect(0, 0, c.width, c.height);
  const hues = [hue, (hue + 0.12 + r() * 0.2) % 1, (hue + 0.55) % 1];
  g.globalCompositeOperation = 'lighter';
  const cloud = (x, y, rad, h, a) => {
    const grad = g.createRadialGradient(x, y, 0, x, y, rad);
    grad.addColorStop(0, `hsla(${h * 360},80%,60%,${a})`);
    grad.addColorStop(0.4, `hsla(${h * 360},75%,45%,${a * 0.45})`);
    grad.addColorStop(1, 'hsla(0,0%,0%,0)');
    g.fillStyle = grad;
    // the texture wraps at x: draw across the seam too
    for (const dx of [0, -c.width, c.width]) {
      g.beginPath();
      g.arc(x + dx, y, rad, 0, Math.PI * 2);
      g.fill();
    }
  };
  // The nebula sits along a wavy band, centred towards -z (u = 0.25 in
  // three's equirectangular mapping), seen through the back windows.
  for (let i = 0; i < 90; i++) {
    const u = 0.25 + (r() - 0.5) * 0.7;
    const x = ((u % 1) + 1) % 1 * c.width;
    const y = c.height * (0.42 + Math.sin(u * 9) * 0.06 + (r() - 0.5) * 0.18);
    cloud(x, y, 40 + r() * 240, hues[i % 3 === 2 && r() < 0.5 ? 2 : i % 2], 0.08 + r() * 0.16);
  }
  g.globalCompositeOperation = 'source-over';
  for (let i = 0; i < 40; i++) { // dark dust lanes
    const x = (0.25 + (r() - 0.5) * 0.5) * c.width;
    const y = c.height * (0.42 + (r() - 0.5) * 0.12);
    const grad = g.createRadialGradient(x, y, 0, x, y, 30 + r() * 90);
    grad.addColorStop(0, 'rgba(2,3,9,.55)');
    grad.addColorStop(1, 'rgba(2,3,9,0)');
    g.fillStyle = grad;
    g.fillRect(x - 130, y - 130, 260, 260);
  }
  for (let i = 0; i < 3000; i++) { // faint background stars, denser in the band
    const y = r() < 0.6 ? c.height * (0.42 + (r() - 0.5) * 0.3) : r() * c.height;
    g.fillStyle = `rgba(255,255,255,${0.2 + r() * 0.6})`;
    g.fillRect(r() * c.width, y, r() < 0.05 ? 2 : 1, 1);
  }
  const tex = new THREE.CanvasTexture(c);
  tex.colorSpace = THREE.SRGBColorSpace;
  tex.mapping = THREE.EquirectangularReflectionMapping;
  return tex;
}

// planet is a banded gas giant with a ring and an atmosphere glow.
function planet(r, hue) {
  const c = document.createElement('canvas');
  c.width = 512;
  c.height = 256;
  const g = c.getContext('2d');
  const h = (hue + 0.4 + r() * 0.3) % 1;
  for (let y = 0; y < c.height; y += 2) {
    const band = Math.sin(y * 0.09 + r() * 0.3) * 0.5 + Math.sin(y * 0.031) * 0.5;
    g.fillStyle = `hsl(${h * 360 + band * 18},${45 + band * 15}%,${42 + band * 14}%)`;
    g.fillRect(0, y, c.width, 2);
  }
  const tex = new THREE.CanvasTexture(c);
  tex.colorSpace = THREE.SRGBColorSpace;
  const grp = new THREE.Group();
  const ball = new THREE.Mesh(new THREE.SphereGeometry(28, 48, 32), new THREE.MeshStandardMaterial({ map: tex, roughness: 1 }));
  grp.add(ball);
  const ringMat = new THREE.MeshBasicMaterial({ color: new THREE.Color().setHSL(h, 0.35, 0.7), transparent: true, opacity: 0.45, side: THREE.DoubleSide, depthWrite: false });
  const ring = new THREE.Mesh(new THREE.RingGeometry(36, 52, 96), ringMat);
  ring.rotation.x = Math.PI / 2 - 0.35;
  grp.add(ring);
  const glow = new THREE.Mesh(new THREE.SphereGeometry(30.5, 32, 16), new THREE.MeshBasicMaterial({ color: new THREE.Color().setHSL(h, 0.8, 0.6), transparent: true, opacity: 0.12, side: THREE.BackSide, depthWrite: false }));
  grp.add(glow);
  const sun = new THREE.DirectionalLight(0xfff1dd, 2.2);
  sun.position.set(-1, 0.4, 0.6);
  grp.add(sun);
  sun.target = ball;
  grp.position.set(-55 + r() * 110, 18 + r() * 14, -170);
  grp.rotation.z = (r() - 0.5) * 0.6;
  grp.tick = (dt) => { ball.rotation.y += dt * 0.01; };
  return grp;
}

// sky is the space outside: a nebula in the deck's colours, a planet and
// stars, turning slowly.
function sky(scene, r, accent) {
  const hue = new THREE.Color(accent).getHSL({}).h;
  const neb = nebulaTexture(r, hue);
  scene.background = neb;
  scene.backgroundIntensity = 1;
  const field = stars(r);
  scene.add(field);
  const world = planet(r, hue);
  scene.add(world);
  return (dt) => {
    world.tick(dt);
    field.rotation.y += dt * 0.002;
    scene.backgroundRotation.y += dt * 0.002;
  };
}

// lab builds a worktree's lab. ui: {transport, service(slug, svc), desk(slug),
// agentDesk(slug), agent(slug), say(text, obj)}.
export async function lab(slug, ui) {
  const deco = decor(slug);
  const { r } = deco;
  const scene = new THREE.Scene();
  const drift = sky(scene, r, deco.accent);
  lights(scene, deco.accent, r);
  const { W, D } = await shell(scene, 8, 6, deco);
  const sign = label(slug, { size: 0.6, color: css(deco.accent) });
  sign.position.set(0, 2.9, -D + 0.5);
  scene.add(sign);

  // Server room: racks along the left wall, behind a rail.
  const racks = new Map();
  // Racks stand in a row against the left wall, fronts to the aisle
  // (x from -W+1.1 to the rail); past eight, a second row faces them
  // with its back to the rail. The way in is past the rail's end.
  const rackSpot = (i) => (i < 8
    ? { x: -W + 0.65, z: -D + 0.9 + i * 1.25, ry: Math.PI / 2 }
    : { x: -W + 3.5, z: -D + 0.9 + (i - 8) * 1.25, ry: -Math.PI / 2 });
  const roomSign = label('Server room', { size: 0.34 });
  roomSign.position.set(-W + 1.9, 3.3, -D + 0.4);
  scene.add(roomSign);
  for (let j = 0; j < 4; j++) await place(scene, 'rail', -W + 4.2, -D + 1 + j * 2, Math.PI / 2);

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

  const ticks = [partner.tick, pad.tick, pet.tick, drift];
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
        rk.rotation.y = p.ry;
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
  const drift = sky(scene, r, deco.accent);
  lights(scene, 0x7fb7ff, r);
  const { W, D } = await shell(scene, 10, 7, { ...deco, backWall: (i) => (i >= 2 && i <= 7 ? 'wall-window' : 'wall') }, 2.4);

  const view = screen(7, 1.7, { size: 30, fg: '#bfe9ff' });
  view.position.set(0, 3.9, -D + 0.45); // above the windows
  scene.add(view);

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
  const ticks = [pad.tick, pet.tick, drift, (dt) => { beacons.rotation.y += dt * 0.2; }];

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
