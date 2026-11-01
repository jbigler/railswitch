// The starship's engine: renderer, camera, picking, models, labels.
import * as THREE from './three.js';

export { THREE };

// S scales Kenney's Space Station Kit (1-unit tiles) to the world: a tile
// is 2 units, a wall 2 high, a person about 1.7.
export const S = 2;

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

// hash is FNV-1a of a string: a worktree's name picks its decor.
export function hash(s) {
  let h = 0x811c9dc5;
  for (const c of new TextEncoder().encode(s)) {
    h ^= c;
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

const loader = new THREE.GLTFLoader();
const models = new Map();

// model is a clone of a Kenney model (static/ship/models/<name>.glb),
// scaled to the world.
export async function model(name) {
  if (!models.has(name)) {
    models.set(name, loader.loadAsync(`/static/ship/models/${name}.glb`).then((g) => {
      g.scene.traverse((o) => {
        if (o.isMesh) {
          o.castShadow = true;
          o.receiveShadow = true;
        }
      });
      return g.scene;
    }));
  }
  const m = (await models.get(name)).clone(true);
  m.scale.setScalar(S);
  return m;
}

// tinted is a model with every material copied and multiplied by color.
export async function tinted(name, color) {
  const m = await model(name);
  m.traverse((o) => {
    if (o.isMesh) {
      o.material = o.material.clone();
      o.material.color = new THREE.Color(color).multiply(o.material.color);
    }
  });
  return m;
}

// textCanvas draws lines on a canvas: the faces of screens and labels.
export function textCanvas(lines, { w = 512, h = 256, bg = '#020812', fg = '#9fe7ff', size = 28, title } = {}) {
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
    g.fillStyle = 'rgba(159,231,255,.35)';
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

// screen is a glowing plane showing lines; update(lines) redraws it.
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
export function label(text, { color = '#d8e6ff', size = 0.32, bg = 'rgba(5,10,20,.7)' } = {}) {
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

// World owns the renderer, the camera, its orbit and picking. A clickable
// object carries userData.pick = {tip, act()}; its first such ancestor wins.
export class World {
  constructor(el) {
    this.el = el;
    this.renderer = new THREE.WebGLRenderer({ antialias: true });
    this.renderer.setPixelRatio(Math.min(devicePixelRatio, 2));
    this.renderer.shadowMap.enabled = true;
    this.renderer.shadowMap.type = THREE.PCFShadowMap;
    this.renderer.toneMapping = THREE.ACESFilmicToneMapping;
    el.appendChild(this.renderer.domElement);
    this.camera = new THREE.PerspectiveCamera(55, 1, 0.1, 400);
    this.scene = new THREE.Scene();
    this.timer = new THREE.Timer();
    this.timer.connect(document);
    this.tickers = new Set();
    this.orbit = { target: new THREE.Vector3(), yaw: 0, pitch: 0.55, dist: 15, min: 5, max: 26 };
    // walk is first person: where you stand, where you look, the room's
    // floor (bounds), the pads that act when stepped on, the keys held.
    this.mode = 'orbit';
    this.walk = { pos: new THREE.Vector3(), yaw: 0, pitch: 0, bounds: null, pads: [], onPad: null, keys: new Set(), blocks: [], blockN: -1 };
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
    this.camera.aspect = w / h;
    this.camera.updateProjectionMatrix();
  }

  // set replaces the scene; view is where the camera looks from.
  set(scene, view, walk) {
    // Models share geometry with the loader's cache: only what a room
    // drew for itself (canvas textures) is freed.
    this.scene.traverse((o) => {
      const mats = Array.isArray(o.material) ? o.material : [o.material];
      for (const m of mats) if (m?.map?.isCanvasTexture) m.map.dispose();
    });
    this.tickers.clear();
    this.scene = scene;
    Object.assign(this.orbit, view);
    this.hover = null;
    const w = this.walk;
    w.bounds = walk?.bounds ?? null;
    w.pads = walk?.pads ?? [];
    w.onPad = null;
    w.blockN = -1;
    if (scene.userData.ceiling) scene.userData.ceiling.visible = this.mode === 'walk';
    if (walk) {
      w.pos.set(walk.start.x, 0, walk.start.z);
      w.yaw = walk.start.yaw;
      w.pitch = -0.08;
    }
  }

  // setMode switches between 'orbit' (the room from above) and 'walk'
  // (first person).
  setMode(m) {
    this.mode = m === 'walk' && this.walk.bounds ? 'walk' : 'orbit';
    this.walk.keys.clear();
    if (this.scene.userData.ceiling) this.scene.userData.ceiling.visible = this.mode === 'walk';
    return this.mode;
  }

  tick(fn) {
    this.tickers.add(fn);
  }

  input() {
    const c = this.renderer.domElement;
    let drag = null;
    c.addEventListener('pointerdown', (e) => {
      const v = this.mode === 'walk' ? this.walk : this.orbit;
      drag = { x: e.clientX, y: e.clientY, yaw: v.yaw, pitch: v.pitch, moved: false };
      c.setPointerCapture(e.pointerId);
    });
    c.addEventListener('pointermove', (e) => {
      if (drag) {
        const dx = e.clientX - drag.x;
        const dy = e.clientY - drag.y;
        if (Math.abs(dx) + Math.abs(dy) > 4) drag.moved = true;
        if (this.mode === 'walk') {
          this.walk.yaw = drag.yaw + dx * 0.004;
          this.walk.pitch = Math.min(1.2, Math.max(-1.2, drag.pitch + dy * 0.004));
        } else {
          this.orbit.yaw = drag.yaw - dx * 0.006;
          this.orbit.pitch = Math.min(1.35, Math.max(0.12, drag.pitch + dy * 0.004));
        }
      }
      this.pointer = { x: e.clientX, y: e.clientY };
    });
    c.addEventListener('pointerup', (e) => {
      const click = drag && !drag.moved;
      drag = null;
      if (click) {
        const p = this.pick(e.clientX, e.clientY);
        p?.userData.pick.act();
      }
    });
    c.addEventListener('pointerleave', () => {
      this.pointer = null;
    });
    const moveKeys = new Set(['w', 'a', 's', 'd', 'arrowup', 'arrowdown', 'arrowleft', 'arrowright', 'shift']);
    addEventListener('keydown', (e) => {
      if (this.mode !== 'walk' || e.target.closest?.('input, textarea, select') || e.ctrlKey || e.metaKey || e.altKey) return;
      const k = e.key.toLowerCase();
      if (moveKeys.has(k)) {
        this.walk.keys.add(k);
        e.preventDefault();
      }
    });
    addEventListener('keyup', (e) => this.walk.keys.delete(e.key.toLowerCase()));
    addEventListener('blur', () => this.walk.keys.clear());
    c.addEventListener('wheel', (e) => {
      e.preventDefault();
      if (this.mode === 'walk') return;
      this.orbit.dist = Math.min(this.orbit.max, Math.max(this.orbit.min, this.orbit.dist * (1 + Math.sign(e.deltaY) * 0.1)));
    }, { passive: false });
  }

  // step moves the walker by the keys held, sliding along furniture
  // (objects with userData.block) and the walls, then places the camera
  // at eye height; a pad acts once on stepping onto it.
  step(dt) {
    const w = this.walk;
    if (w.blockN !== this.scene.children.length) {
      w.blockN = this.scene.children.length;
      w.blocks = [];
      this.scene.traverse((o) => { if (o.userData.block) w.blocks.push(new THREE.Box3().setFromObject(o).expandByScalar(0.3)); });
    }
    const k = w.keys;
    const fwd = (k.has('w') || k.has('arrowup') ? 1 : 0) - (k.has('s') || k.has('arrowdown') ? 1 : 0);
    const side = (k.has('d') ? 1 : 0) - (k.has('a') ? 1 : 0);
    w.yaw += ((k.has('arrowright') ? 1 : 0) - (k.has('arrowleft') ? 1 : 0)) * dt * 1.8;
    if (fwd || side) {
      const speed = (k.has('shift') ? 6 : 3) * dt;
      const sin = Math.sin(w.yaw);
      const cos = Math.cos(w.yaw);
      const dx = (sin * fwd + cos * side) * speed;
      const dz = (-cos * fwd + sin * side) * speed;
      const free = (x, z) => {
        const b = w.bounds;
        if (x < b.x0 || x > b.x1 || z < b.z0 || z > b.z1) return false;
        const p = new THREE.Vector3(x, 1, z);
        return !w.blocks.some((box) => box.containsPoint(p));
      };
      if (free(w.pos.x + dx, w.pos.z)) w.pos.x += dx;
      if (free(w.pos.x, w.pos.z + dz)) w.pos.z += dz;
    }
    const pad = w.pads.find((p) => Math.hypot(p.x - w.pos.x, p.z - w.pos.z) < p.r) ?? null;
    if (pad && pad !== w.onPad) pad.act();
    w.onPad = pad;
    this.camera.position.set(w.pos.x, 1.6, w.pos.z);
    this.camera.rotation.set(-w.pitch, -w.yaw, 0, 'YXZ');
  }

  pick(x, y) {
    const r = this.renderer.domElement.getBoundingClientRect();
    this.ray.setFromCamera(new THREE.Vector2(((x - r.left) / r.width) * 2 - 1, -((y - r.top) / r.height) * 2 + 1), this.camera);
    for (const hit of this.ray.intersectObjects(this.scene.children, true)) {
      for (let o = hit.object; o; o = o.parent) {
        if (o.userData.pick) return o;
        if (o.userData.solid) break;
      }
    }
    return null;
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
    }
    const p = this.pointer ? this.pick(this.pointer.x, this.pointer.y) : null;
    if (p !== this.hover) {
      glow(this.hover, false);
      glow(p, true);
      this.hover = p;
      this.renderer.domElement.style.cursor = p ? 'pointer' : 'grab';
    }
    this.onTip(p && this.pointer ? { text: p.userData.pick.tip, ...this.pointer } : null);
    this.renderer.render(this.scene, this.camera);
  }
}

// glow lights up a hovered object's meshes.
function glow(o, on) {
  o?.traverse((m) => {
    if (!m.isMesh || !m.material || !('emissive' in m.material)) return;
    if (!m.userData.ownMat) {
      m.material = m.material.clone();
      m.material.userData.own = true;
      m.userData.ownMat = true;
      m.userData.emissive = m.material.emissive.clone();
    }
    m.material.emissive.copy(on ? new THREE.Color(0x1a4d66) : m.userData.emissive);
  });
}
