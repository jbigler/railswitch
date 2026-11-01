// The cyberpunk office's engine: renderer and bloom, camera, picking,
// walking, labels and screens. The starship's (D144), on the office kit
// (static/office/, D145): its pieces carry their own collider boxes.
import * as THREE from './three.js';
import { loadOfficeKit, animate, setElevatorDoors, createRoamer, createFlyer } from '../office/kit.js';

export { THREE, animate, setElevatorDoors, createRoamer, createFlyer };

// rng is a small seeded PRNG (mulberry32): the same seed, the same decor.
export function rng(seed) {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// hash is FNV-1a of a string: a worktree's name picks its floor's look.
export function hash(s) {
  let h = 0x811c9dc5;
  for (const c of new TextEncoder().encode(s)) {
    h ^= c;
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

// kit loads the office pieces once (names: every piece a room may use).
let kitP = null;
export function kit(names) {
  kitP ??= loadOfficeKit('/static/office/models/', names, new THREE.GLTFLoader());
  return kitP;
}

// textCanvas draws lines on a canvas: the faces of screens and labels.
export function textCanvas(lines, { w = 512, h = 256, bg = '#03060c', fg = '#9ff3ff', size = 28, title } = {}) {
  const c = document.createElement('canvas');
  c.width = w;
  c.height = h;
  const g = c.getContext('2d');
  g.fillStyle = bg;
  g.fillRect(0, 0, w, h);
  let y = 12;
  if (title) {
    g.fillStyle = fg;
    g.font = `700 ${size}px ui-monospace, monospace`;
    g.textBaseline = 'top';
    g.fillText(title, 14, y);
    y += size * 1.4;
    g.fillStyle = 'rgba(159,243,255,.35)';
    g.fillRect(14, y - 6, w - 28, 2);
  }
  g.font = `${Math.round(size * 0.78)}px ui-monospace, monospace`;
  g.textBaseline = 'top';
  for (const l of lines) {
    if (y > h - size) break;
    g.fillStyle = l.color || fg;
    g.fillText(String(l.text ?? l).slice(0, 64), 14, y);
    y += size * 1.05;
  }
  return c;
}

// screen is a glowing plane showing lines; update(lines, title) redraws it.
export function screen(width, height, opts = {}) {
  const px = Math.round(256 * Math.max(1, height / 0.9));
  const canvas = textCanvas([], { w: Math.round(px * width / height), h: px, ...opts, size: (opts.size || 28) * px / 256 });
  const tex = new THREE.CanvasTexture(canvas);
  tex.colorSpace = THREE.SRGBColorSpace;
  const mesh = new THREE.Mesh(new THREE.PlaneGeometry(width, height), new THREE.MeshBasicMaterial({ map: tex, toneMapped: false }));
  mesh.update = (lines, title) => {
    const c = textCanvas(lines, { w: canvas.width, h: canvas.height, title, ...opts, size: (opts.size || 28) * px / 256 });
    canvas.getContext('2d').drawImage(c, 0, 0);
    tex.needsUpdate = true;
  };
  return mesh;
}

// label is a sprite with text that always faces the camera.
export function label(text, { color = '#e6f6ff', size = 0.32, bg = 'rgba(5,8,16,.75)' } = {}) {
  const c = document.createElement('canvas');
  const g = c.getContext('2d');
  const font = '600 44px ui-sans-serif, system-ui, sans-serif';
  g.font = font;
  c.width = Math.ceil(g.measureText(text).width) + 36;
  c.height = 64;
  g.font = font;
  g.fillStyle = bg;
  g.beginPath();
  g.roundRect(0, 0, c.width, c.height, 14);
  g.fill();
  g.fillStyle = color;
  g.textBaseline = 'middle';
  g.fillText(text, 18, 34);
  const tex = new THREE.CanvasTexture(c);
  tex.colorSpace = THREE.SRGBColorSpace;
  const s = new THREE.Sprite(new THREE.SpriteMaterial({ map: tex, depthTest: false, toneMapped: false }));
  s.scale.set(size * c.width / c.height, size, 1);
  s.renderOrder = 10;
  return s;
}

const shown = (o) => { for (; o; o = o.parent) if (!o.visible) return false; return true; };
const IS_COL = /_COL\d*$/;

// World owns the renderer, the camera, its orbit, walking and picking. A
// clickable object carries userData.pick = {tip, act()}; its first such
// ancestor wins.
export class World {
  constructor(el) {
    this.el = el;
    this.renderer = new THREE.WebGLRenderer({ antialias: true });
    this.renderer.setPixelRatio(Math.min(devicePixelRatio, 2));
    this.renderer.toneMapping = THREE.ACESFilmicToneMapping;
    el.appendChild(this.renderer.domElement);
    this.camera = new THREE.PerspectiveCamera(60, 1, 0.05, 400);
    this.scene = new THREE.Scene();
    this.composer = new THREE.EffectComposer(this.renderer);
    this.pass = new THREE.RenderPass(this.scene, this.camera);
    this.bloom = new THREE.UnrealBloomPass(new THREE.Vector2(256, 256), 0.28, 0.25, 1.2);
    this.composer.addPass(this.pass);
    this.composer.addPass(this.bloom);
    this.composer.addPass(new THREE.OutputPass());
    this.timer = new THREE.Timer();
    this.timer.connect(document);
    this.tickers = new Set();
    this.orbit = { target: new THREE.Vector3(), yaw: 0, pitch: 0.75, dist: 17, min: 5, max: 30 };
    // walk is first person: where you stand and look, the walkable areas,
    // the pads that act when stepped on, the keys held, a click's goal.
    this.mode = 'orbit';
    this.walk = { pos: new THREE.Vector3(), yaw: 0, pitch: 0, areas: [], pads: [], onPad: null, keys: new Set(), boxes: null, goal: null };
    this.ray = new THREE.Raycaster();
    this.hover = null;
    this.onTip = () => {};
    this.resize();
    addEventListener('resize', () => this.resize());
    this.input();
    this.renderer.setAnimationLoop(() => this.frame());
  }

  resize() {
    const w = this.el.clientWidth || innerWidth;
    const h = this.el.clientHeight || innerHeight;
    this.renderer.setSize(w, h);
    this.composer.setSize(w, h);
    this.camera.aspect = w / h;
    this.camera.updateProjectionMatrix();
  }

  // set replaces the scene; view is where the overview camera looks from;
  // walk is {areas: [[x0, x1, z0, z1]…], start: {x, z, yaw}, pads}.
  set(scene, view, walk) {
    this.scene.traverse((o) => {
      const mats = Array.isArray(o.material) ? o.material : [o.material];
      for (const m of mats) if (m?.map?.isCanvasTexture) m.map.dispose();
    });
    this.tickers.clear();
    this.scene = scene;
    this.pass.scene = scene;
    Object.assign(this.orbit, view);
    this.hover = null;
    const w = this.walk;
    w.areas = walk?.areas ?? [];
    w.pads = walk?.pads ?? [];
    w.onPad = null;
    w.boxes = null;
    w.goal = null;
    if (walk) {
      w.pos.set(walk.start.x, 0, walk.start.z);
      w.yaw = walk.start.yaw;
      w.pitch = -0.08;
    }
    this.cutaway();
  }

  // setMode switches between 'orbit' (the floor from above, ceiling and
  // near walls cut away) and 'walk' (first person).
  setMode(m) {
    this.mode = m === 'walk' && this.walk.areas.length ? 'walk' : 'orbit';
    this.walk.keys.clear();
    this.walk.goal = null;
    this.cutaway();
    return this.mode;
  }

  cutaway() {
    const u = this.scene.userData;
    if (u.ceiling) u.ceiling.visible = this.mode === 'walk';
    if (u.hemi) u.hemi.intensity = this.mode === 'walk' ? 0.6 : 2.0;
    if (this.mode === 'walk') for (const w of u.walls ?? []) w.visible = true;
  }

  tick(fn) {
    this.tickers.add(fn);
  }

  input() {
    const c = this.renderer.domElement;
    let drag = null;
    c.addEventListener('pointerdown', (e) => {
      const v = this.mode === 'walk' ? this.walk : this.orbit;
      drag = { x: e.clientX, y: e.clientY, lx: e.clientX, ly: e.clientY, yaw: v.yaw, pitch: v.pitch, moved: false };
      c.setPointerCapture(e.pointerId);
    });
    c.addEventListener('pointermove', (e) => {
      if (drag) {
        // a jump of the pointer (remote and virtual-machine sessions) is not a drag
        if (Math.abs(e.clientX - drag.lx) > 120 || Math.abs(e.clientY - drag.ly) > 120) {
          drag.x += e.clientX - drag.lx;
          drag.y += e.clientY - drag.ly;
        }
        drag.lx = e.clientX;
        drag.ly = e.clientY;
        const dx = e.clientX - drag.x;
        const dy = e.clientY - drag.y;
        if (Math.abs(dx) + Math.abs(dy) > 5) drag.moved = true;
        if (this.mode === 'walk') {
          this.walk.yaw = drag.yaw + dx * 0.004;
          this.walk.pitch = Math.min(1.2, Math.max(-1.2, drag.pitch + dy * 0.004));
        } else {
          this.orbit.yaw = drag.yaw - dx * 0.006;
          this.orbit.pitch = Math.min(1.4, Math.max(0.15, drag.pitch + dy * 0.004));
        }
      }
      this.pointer = { x: e.clientX, y: e.clientY };
    });
    c.addEventListener('pointerup', (e) => {
      const click = drag && !drag.moved;
      drag = null;
      if (!click) return;
      const p = this.pick(e.clientX, e.clientY);
      if (p) p.userData.pick.act();
      else if (this.mode === 'walk') this.walkTo(e.clientX, e.clientY);
    });
    c.addEventListener('pointercancel', () => { drag = null; });
    c.addEventListener('pointerleave', () => {
      this.pointer = null;
    });
    const moveKeys = new Set(['w', 'a', 's', 'd', 'arrowup', 'arrowdown', 'arrowleft', 'arrowright', 'shift']);
    addEventListener('keydown', (e) => {
      if (this.mode !== 'walk' || e.target.closest?.('input, textarea, select') || e.ctrlKey || e.metaKey || e.altKey) return;
      const k = e.key.toLowerCase();
      if (moveKeys.has(k)) {
        this.walk.keys.add(k);
        this.walk.goal = null;
        e.preventDefault();
      }
    });
    addEventListener('keyup', (e) => this.walk.keys.delete(e.key.toLowerCase()));
    addEventListener('blur', () => { this.walk.keys.clear(); drag = null; });
    c.addEventListener('wheel', (e) => {
      e.preventDefault();
      if (this.mode === 'walk') return;
      this.orbit.dist = Math.min(this.orbit.max, Math.max(this.orbit.min, this.orbit.dist * (1 + Math.sign(e.deltaY) * 0.1)));
    }, { passive: false });
  }

  // hits: the visible surfaces under a screen point, nearest first.
  hits(x, y) {
    const r = this.renderer.domElement.getBoundingClientRect();
    this.ray.setFromCamera(new THREE.Vector2(((x - r.left) / r.width) * 2 - 1, -((y - r.top) / r.height) * 2 + 1), this.camera);
    return this.ray.intersectObjects(this.scene.children, true).filter((h) => !IS_COL.test(h.object.name) && h.object.isMesh !== false && shown(h.object));
  }

  pick(x, y) {
    for (const hit of this.hits(x, y)) {
      for (let o = hit.object; o; o = o.parent) {
        if (o.userData.pick) return o;
        if (o.userData.solid) break;
      }
      return null; // the nearest surface decides
    }
    return null;
  }

  walkTo(x, y) {
    const h = this.hits(x, y).find((hh) => !hh.object.isSprite);
    if (h && h.point.y < 2.2) this.walk.goal = { x: h.point.x, z: h.point.z, stalled: 0 };
  }

  // Collision: a 0.3 m circle against the colliders (the kit's *_COL boxes
  // and objects marked userData.block) that stand between 0.25 and 1.8 m.
  // Inside one, or outside the floor, any step that doesn't lead further
  // out is allowed, so the walker never gets stuck.
  blockers() {
    const w = this.walk;
    if (!w.boxes) {
      w.boxes = [];
      this.scene.updateMatrixWorld(true);
      this.scene.traverse((o) => {
        if (!(IS_COL.test(o.name) || o.userData.block)) return;
        if (/Door_[LR]_COL\d*$/.test(o.name)) return; // the elevator doors move: scene.userData.movingBoxes
        if (!shown(o) && !IS_COL.test(o.name)) return;
        const b = new THREE.Box3().setFromObject(o, true);
        if (b.max.y >= 0.25 && b.min.y <= 1.8) w.boxes.push(b);
      });
    }
    return [...w.boxes, ...(this.scene.userData.movingBoxes?.() ?? [])];
  }

  move(dx, dz) {
    const w = this.walk;
    const boxes = this.blockers();
    const outside = (x, z) => Math.min(...w.areas.map(([x0, x1, z0, z1]) => Math.hypot(Math.max(x0 - x, 0, x - x1), Math.max(z0 - z, 0, z - z1))));
    const hit = (x, z) => boxes.some((b) => Math.hypot(x - Math.min(Math.max(x, b.min.x), b.max.x), z - Math.min(Math.max(z, b.min.z), b.max.z)) < 0.3);
    const ok = (x, z) => outside(x, z) === 0 && !hit(x, z);
    const { x, z } = w.pos;
    if (outside(x, z) > 0 || hit(x, z)) {
      if (outside(x + dx, z + dz) <= Math.max(outside(x, z), 0.6)) { w.pos.x += dx; w.pos.z += dz; }
      return;
    }
    if (ok(x + dx, z + dz)) { w.pos.x += dx; w.pos.z += dz; } else if (ok(x + dx, z)) w.pos.x += dx; else if (ok(x, z + dz)) w.pos.z += dz;
  }

  step(dt) {
    const w = this.walk;
    const k = w.keys;
    const fwd = (k.has('w') || k.has('arrowup') ? 1 : 0) - (k.has('s') || k.has('arrowdown') ? 1 : 0);
    const side = (k.has('d') ? 1 : 0) - (k.has('a') ? 1 : 0);
    w.yaw += ((k.has('arrowright') ? 1 : 0) - (k.has('arrowleft') ? 1 : 0)) * dt * 1.8;
    if (fwd || side) {
      const speed = (k.has('shift') ? 5 : 2.8) * dt / Math.hypot(fwd, side);
      const sin = Math.sin(w.yaw);
      const cos = Math.cos(w.yaw);
      this.move((sin * fwd + cos * side) * speed, (-cos * fwd + sin * side) * speed);
    } else if (w.goal) {
      const dx = w.goal.x - w.pos.x, dz = w.goal.z - w.pos.z, d = Math.hypot(dx, dz);
      const turn = ((Math.atan2(dx, -dz) - w.yaw + Math.PI * 3) % (Math.PI * 2)) - Math.PI;
      w.yaw += Math.max(-4 * dt, Math.min(4 * dt, turn));
      const s = Math.min(d, 2.8 * dt), bx = w.pos.x, bz = w.pos.z;
      if (d > 1e-4) this.move(dx / d * s, dz / d * s);
      w.goal.stalled = Math.hypot(w.pos.x - bx, w.pos.z - bz) < s * 0.3 ? w.goal.stalled + dt : 0;
      if (d < 0.35 || w.goal.stalled > 0.4) w.goal = null;
    }
    const pad = w.pads.find((p) => Math.hypot(p.x - w.pos.x, p.z - w.pos.z) < p.r) ?? null;
    if (pad && pad !== w.onPad) pad.act();
    w.onPad = pad;
    this.camera.position.set(w.pos.x, 1.7, w.pos.z);
    this.camera.rotation.set(-w.pitch, -w.yaw, 0, 'YXZ');
  }

  // where the viewer stands, for the pets to keep their distance
  viewer() {
    return this.mode === 'walk' ? { x: this.walk.pos.x, z: this.walk.pos.z } : null;
  }

  frame() {
    this.timer.update();
    const dt = Math.min(this.timer.getDelta(), 0.1);
    const t = this.timer.getElapsed();
    for (const f of this.tickers) f(dt, t);
    if (this.mode === 'walk') {
      this.step(dt);
    } else {
      const o = this.orbit;
      this.camera.position.set(
        o.target.x + o.dist * Math.cos(o.pitch) * Math.sin(o.yaw),
        o.target.y + o.dist * Math.sin(o.pitch),
        o.target.z + o.dist * Math.cos(o.pitch) * Math.cos(o.yaw),
      );
      this.camera.lookAt(o.target);
      const fwd = new THREE.Vector3();
      for (const wall of this.scene.userData.walls ?? []) { // dollhouse: hide the walls facing the camera
        wall.getWorldDirection(fwd);
        wall.visible = fwd.dot(this.camera.position.clone().sub(wall.position)) > 0;
      }
    }
    animate(this.scene, t);
    const p = this.pointer ? this.pick(this.pointer.x, this.pointer.y) : null;
    if (p !== this.hover) {
      glow(this.hover, false);
      glow(p, true);
      this.hover = p;
      this.renderer.domElement.style.cursor = p ? 'pointer' : 'grab';
    }
    this.onTip(p && this.pointer ? { text: p.userData.pick.tip, ...this.pointer } : null);
    this.composer.render();
  }
}

// glow lights up a hovered object's meshes (on its own copies of their materials).
function glow(o, on) {
  o?.traverse((m) => {
    if (!m.isMesh || !m.material || !('emissive' in m.material) || IS_COL.test(m.name)) return;
    if (!m.userData.ownMat && m.material.emissive.getHex() !== 0) return; // neon and screens already glow
    if (!m.userData.ownMat) {
      m.material = m.material.clone();
      m.userData.ownMat = true;
      m.userData.emissive = m.material.emissive.clone();
    }
    m.material.emissive.copy(on ? new THREE.Color(0x1a4d66) : m.userData.emissive);
  });
}
