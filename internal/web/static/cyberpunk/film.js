// The film set: what makes walking a floor feel like a scene from a cyberpunk
// film rather than an evenly lit office, on a modest GPU (no shadows, no
// extra passes): low-key light from practicals, warm (lamps, café) against
// cool (screens, the city), haze, light shafts through half-drawn blinds,
// dust in the air, cables underfoot, a tube that flickers, neon kept for
// signs. Shafts, dust, haze and sign rods show in first person only.
import { THREE, rng } from './world.js';

const PI = Math.PI;

// LAMPS are a floor's lights: [x, y, z, colour, intensity, distance]. Warm
// where people sit, cool where machines are, dim along the corridors.
// LIFT brightens every lamp at once (the office read too dark).
const LIFT = 1.3;
const LAMPS = [
  [-9.6, 2.7, -6.4, 0x7fd8ff, 5, 7], // server room: the tube that flickers
  [10.8, 2.3, 5.2, 0xffa040, 6, 6], // café counter
  [-7.8, 1.7, 7.2, 0xff9a50, 4, 5], // lounge floor lamp
  [0.55, 2.5, -6.4, 0xcfe0ff, 5, 6], // your desk's pendant
  [2.95, 2.5, -6.4, 0xcfe0ff, 4, 6], // the agent's
  [3.55, 1.1, -7.0, 0xffb060, 2.5, 3.5], // the agent's desk lamp
  [8.4, 2.6, -5.0, 0xa8c8ff, 5, 6], // meeting
  [-2, 2.8, 3.0, 0xb0b8d0, 3.5, 6], // spine
  [-2, 2.8, -4.2, 0xb0b8d0, 3.5, 6], // landing
  [7.0, 2.8, 0.2, 0xb0b8d0, 3.5, 7], // the aisle by the meeting room
  [-8.0, 2.8, 0.0, 0xb0b8d0, 3.5, 7], // the aisle between the glass rooms
  [1.5, 2.8, 5.0, 0xb0b8d0, 3, 6], // the pet's corner
];

// stripes is the light through a window: open below, blinds above.
function stripes() {
  const c = document.createElement('canvas');
  c.width = 4;
  c.height = 128;
  const g = c.getContext('2d');
  g.fillStyle = '#fff';
  g.fillRect(0, 64, 4, 64); // v 0…0.5 (the lower half, canvas bottom): open
  for (let y = 0; y < 64; y += 8) g.fillRect(0, y, 4, 5); // the upper half: slats
  const t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  return t;
}

// shaft is the city's light through one window (wall-local: the window spans
// x ±w/2, y y0…y1 on the wall's face at z 0, the room at +z), falling at
// `fall` metres in per metre down: the beam and its patch on the floor.
function shaft(w, y0, y1, fall, mat) {
  const pts = [], uvs = [], cols = [];
  const quad = (a, b, c, d) => {
    for (const p of [a, b, c, a, c, d]) {
      pts.push(p[0], p[1], p[2]);
      uvs.push(p[3], p[4]);
      const k = p[5];
      cols.push(k, k, k);
    }
  };
  const W = (x, y) => [x, y, 0, x / w + 0.5, (y - y0) / (y1 - y0), 1]; // at the window
  const F = (x, y) => [x, 0.01, y * fall, x / w + 0.5, (y - y0) / (y1 - y0), 0.55]; // the ray from y, on the floor
  const h = w / 2;
  quad(W(-h, y0), W(h, y0), F(h, y0), F(-h, y0)); // the beam's underside
  quad(W(-h, y1), W(h, y1), F(h, y1), F(-h, y1)); // its top
  quad(W(-h, y0), W(-h, y1), F(-h, y1), F(-h, y0)); // its sides
  quad(W(h, y0), W(h, y1), F(h, y1), F(h, y0));
  const beam = new THREE.BufferGeometry();
  beam.setAttribute('position', new THREE.Float32BufferAttribute(pts, 3));
  beam.setAttribute('uv', new THREE.Float32BufferAttribute(uvs, 2));
  beam.setAttribute('color', new THREE.Float32BufferAttribute(cols.map((k) => k * 0.05), 3));
  const g = new THREE.Group();
  g.add(new THREE.Mesh(beam, mat));
  const patch = new THREE.BufferGeometry(); // the stripes on the floor
  pts.length = uvs.length = cols.length = 0;
  quad(F(-h, y0), F(h, y0), F(h, y1), F(-h, y1));
  patch.setAttribute('position', new THREE.Float32BufferAttribute(pts, 3));
  patch.setAttribute('uv', new THREE.Float32BufferAttribute(uvs, 2));
  patch.setAttribute('color', new THREE.Float32BufferAttribute(cols.map(() => 0.14), 3));
  g.add(new THREE.Mesh(patch, mat));
  return g;
}

// blinds are the slats over a window's upper half, tilted.
function blinds(w, y0, y1) {
  const n = Math.round((y1 - y0) / 0.1);
  const m = new THREE.InstancedMesh(new THREE.BoxGeometry(w, 0.006, 0.06), new THREE.MeshStandardMaterial({ color: 0x8a8f99, roughness: 0.6, metalness: 0.3 }), n);
  const o = new THREE.Object3D();
  for (let i = 0; i < n; i++) {
    o.position.set(0, y1 - i * 0.1, 0.05);
    o.rotation.x = 0.5;
    o.updateMatrix();
    m.setMatrixAt(i, o.matrix);
  }
  return m;
}

// cable lays a bundle of cables along points [x, z] on the floor.
function cable(parent, path, mats) {
  mats.forEach((mat, i) => {
    const off = (i - (mats.length - 1) / 2) * 0.028;
    const curve = new THREE.CatmullRomCurve3(path.map(([x, z], j) => new THREE.Vector3(x + off * (j % 2 ? 1 : -0.6), 0.012, z + off)));
    const m = new THREE.Mesh(new THREE.TubeGeometry(curve, path.length * 12, 0.012, 5), mat);
    m.userData.decal = true;
    parent.add(m);
  });
}

// droop hangs a cable between two ceiling points [x, z].
function droop(parent, a, b, sag, mat) {
  const pts = [];
  for (let i = 0; i <= 12; i++) {
    const t = i / 12;
    pts.push(new THREE.Vector3(a[0] + (b[0] - a[0]) * t, 3.97 - sag * 4 * t * (1 - t), a[1] + (b[1] - a[1]) * t));
  }
  parent.add(new THREE.Mesh(new THREE.TubeGeometry(new THREE.CatmullRomCurve3(pts), 24, 0.012, 5), mat));
}

// film dresses a floor's set: lights, haze, shafts, dust, cables, the
// flickering tube; returns its ticks. walls: the floor's wall pieces; panels:
// its ceiling light panels, the server room's first.
export function film(scene, { walls, panels, ceiling, accent, seed }) {
  const r = rng(seed ^ 0x5eed);
  const fog = new THREE.FogExp2(0x0b0f1c, 0.04);
  scene.fog = fog;
  scene.background = new THREE.Color(0x05060c);
  const hemi = new THREE.HemisphereLight(0x4a5c88, 0x2a1c12, 0.5);
  scene.add(hemi);
  scene.userData.hemi = hemi;
  scene.userData.levels = { walk: { hemi: 2.4, fog: 0.035 }, orbit: { hemi: 9, fog: 0 } };
  const city = new THREE.DirectionalLight(0x6fa0ff, 1.1); // the city, through the windows
  city.position.set(14, 6, 10);
  scene.add(city);
  const lamps = LAMPS.map(([x, y, z, c, i, d]) => {
    const l = new THREE.PointLight(c, i * LIFT, d);
    l.position.set(x, y, z);
    scene.add(l);
    return l;
  });
  const spill = new THREE.PointLight(accent, 2.5, 6); // the big screen's glow on the lounge
  spill.position.set(-11.0, 2.0, 5.75);
  scene.add(spill);

  // Neon is for signs: the walls' trim strips only hint at it, and the floor
  // shines a little, as if just mopped.
  const trim = new THREE.MeshStandardMaterial({ color: 0x000000, emissive: new THREE.Color(accent), emissiveIntensity: 0.16 });
  for (const w of walls) w.traverse((o) => { if (o.isMesh && o.material?.name === 'RS_Neon_Accent') o.material = trim; });
  scene.traverse((o) => {
    const n = o.material?.name ?? '';
    if (n === 'RS_Floor') { o.material.roughness = 0.32; o.material.metalness = 0.45; }
    if (n === 'RS_LightPanel') { o.material.emissiveIntensity = 0.12; o.material.color.setHex(0x1a1c22); o.material.roughness = 1; o.material.metalness = 0; } // diffusers: no glare from the lamp below
  });
  // the city stays clear beyond the haze (its materials are its own)
  for (const o of scene.children) if (o.userData.kit === 'Ext_CityBackdrop') o.traverse((m) => { if (m.material) m.material.fog = false; });

  // What only first person sees: the beams, the dust, the slats.
  const air = new THREE.Group();
  air.visible = false;
  scene.add(air);
  scene.userData.air = air;
  const beam = new THREE.MeshBasicMaterial({ map: stripes(), color: 0x6c9cff, vertexColors: true, transparent: true, blending: THREE.AdditiveBlending, depthWrite: false, side: THREE.DoubleSide, fog: false, toneMapped: false });
  for (const w of walls) {
    if (w.userData.kit !== 'Arch_Wall_Window_4m') continue;
    const s = shaft(3.0, 1.0, 3.0, 1.15, beam);
    const b = blinds(3.0, 2.1, 3.0);
    for (const o of [s, b]) {
      o.position.copy(w.position);
      o.rotation.copy(w.rotation);
      air.add(o);
    }
  }
  const n = 700;
  const dust = new Float32Array(n * 3);
  for (let i = 0; i < n; i++) dust.set([-11.8 + r() * 23.6, 0.2 + r() * 3.6, -7.8 + r() * 15.6], i * 3);
  const motes = new THREE.BufferGeometry();
  motes.setAttribute('position', new THREE.BufferAttribute(dust, 3));
  const dot = document.createElement('canvas');
  dot.width = dot.height = 16;
  const dg = dot.getContext('2d');
  const grad = dg.createRadialGradient(8, 8, 0, 8, 8, 8);
  grad.addColorStop(0, '#fff');
  grad.addColorStop(1, 'rgba(255,255,255,0)');
  dg.fillStyle = grad;
  dg.fillRect(0, 0, 16, 16);
  air.add(new THREE.Points(motes, new THREE.PointsMaterial({ map: new THREE.CanvasTexture(dot), color: 0x8aa6d8, size: 0.012, transparent: true, opacity: 0.4, blending: THREE.AdditiveBlending, depthWrite: false })));

  // Cables: power and network from the server room to the desks along the
  // back, more hanging between the ceiling's services.
  const rubber = [0x0b0b0d, 0x3a0d12, 0x0d1a33].map((c) => new THREE.MeshStandardMaterial({ color: c, roughness: 0.55 }));
  cable(scene, [[-4.2, -6.2], [-3.1, -5.0], [-2.7, -4.75], [-1.2, -4.7], [-0.6, -5.4], [0.2, -6.3], [0.6, -6.9]], rubber);
  cable(scene, [[1.9, -7.8], [2.3, -7.4], [3.0, -7.6], [3.5, -7.3]], rubber.slice(0, 2));
  cable(scene, [[-11.7, 4.6], [-11.6, 5.2], [-11.75, 5.9]], rubber.slice(1));
  for (const [a, b, s] of [[[-9.5, -6.4], [-4.4, -4.4], 0.35], [[-4.2, -4.4], [0.5, -5.0], 0.5], [[1.0, -4.0], [3.6, -4.6], 0.4], [[-9.0, 2.6], [-9.2, 6.4], 0.3]]) {
    droop(ceiling, a, b, s, rubber[0]);
  }

  // The server room's tube fails now and then.
  const tube = panels[0];
  const tubeMats = [];
  tube?.traverse((o) => {
    if (o.isMesh && o.material?.name === 'RS_LightPanel') {
      o.material = o.material.clone();
      tubeMats.push(o.material);
    }
  });
  let next = 2, off = 0;
  const flicker = (dt) => {
    next -= dt;
    if (next < 0) { off = 0.08 + r() * 0.5; next = 3 + r() * 7; }
    let on = 1;
    if (off > 0) { off -= dt; on = r() < 0.5 ? 0.05 : 0.6; }
    lamps[0].intensity = 5 * LIFT * on;
    for (const m of tubeMats) m.emissiveIntensity = 0.12 * on;
  };
  const drift = (dt, t) => { air.children.at(-1).position.y = Math.sin(t * 0.13) * 0.15; air.children.at(-1).rotation.y = t * 0.004; };
  return [flicker, drift];
}

// neonSign is a hanging sign: the text in neon tubes on a dark board, both
// faces; rods up to the ceiling (first person only, in rods).
export function neonSign(text, color, rods) {
  const font = '700 72px ui-sans-serif, system-ui, sans-serif';
  const c = document.createElement('canvas');
  let g = c.getContext('2d');
  g.font = font;
  const tw = Math.ceil(g.measureText(text.toUpperCase()).width);
  c.width = tw + 120;
  c.height = 150;
  g = c.getContext('2d');
  g.font = font;
  g.textAlign = 'center';
  g.textBaseline = 'middle';
  const css = '#' + new THREE.Color(color).getHexString();
  g.shadowColor = css;
  g.lineJoin = 'round';
  for (const [blur, width, stroke] of [[30, 8, css], [12, 5, css], [0, 2, '#ffffff']]) {
    g.shadowBlur = blur;
    g.lineWidth = width;
    g.strokeStyle = stroke;
    g.strokeText(text.toUpperCase(), c.width / 2, 78);
  }
  g.shadowBlur = 16;
  g.lineWidth = 3;
  g.strokeStyle = css;
  g.strokeRect(14, 14, c.width - 28, c.height - 28);
  const tex = new THREE.CanvasTexture(c);
  tex.colorSpace = THREE.SRGBColorSpace;
  const h = 0.36, w = h * c.width / c.height;
  const sign = new THREE.Group();
  const board = new THREE.Mesh(new THREE.BoxGeometry(w * 0.96, h * 0.86, 0.03), new THREE.MeshStandardMaterial({ color: 0x0a0b10, roughness: 0.4, metalness: 0.5 }));
  sign.add(board);
  const face = new THREE.MeshBasicMaterial({ map: tex, transparent: true, toneMapped: false, depthWrite: false });
  for (const s of [1, -1]) {
    const p = new THREE.Mesh(new THREE.PlaneGeometry(w, h), face);
    p.position.z = s * 0.017;
    p.rotation.y = s > 0 ? 0 : PI;
    sign.add(p);
  }
  sign.rods = (x, y, z, ry) => {
    const rod = new THREE.MeshStandardMaterial({ color: 0x222428, metalness: 0.7, roughness: 0.4 });
    for (const s of [-1, 1]) {
      const m = new THREE.Mesh(new THREE.CylinderGeometry(0.006, 0.006, 4 - y - h / 2), rod);
      m.position.set(x + Math.cos(ry) * s * w * 0.4, (4 + y + h / 2) / 2, z - Math.sin(ry) * s * w * 0.4);
      rods.add(m);
    }
  };
  return sign;
}
