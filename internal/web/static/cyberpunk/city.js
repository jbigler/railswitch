// The city outside the windows (the right wall, x = 12, and the front, z =
// 8): tower bands hazier with distance, a smog-red horizon, tall flickering
// ad signs, spinners crossing between the towers. Simple shapes and a few
// canvas textures; no part of it uses the room's fog (the room's would hide
// it), each band is hazed by its distance instead. city(scene, seed)
// returns its tick.
import { THREE, rng } from './world.js';

const PI = Math.PI;
const HAZE = new THREE.Color(0x2a1420); // the smog the far towers fade into

// windowsTexture is a tower face at night: dark, with lit windows in rows.
function windowsTexture(r, lit) {
  const c = document.createElement('canvas');
  c.width = 64;
  c.height = 256;
  const g = c.getContext('2d');
  g.fillStyle = '#05060a';
  g.fillRect(0, 0, 64, 256);
  const tones = ['#ffd9a0', '#9fd8ff', '#ff9ad8', '#fff2d0'];
  for (let y = 4; y < 256; y += 6) for (let x = 3; x < 64; x += 6) {
    if (r() < lit) {
      g.fillStyle = tones[Math.floor(r() * tones.length)];
      g.globalAlpha = 0.5 + r() * 0.5;
      g.fillRect(x, y, 3, 3);
    }
  }
  g.globalAlpha = 1;
  const t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  t.wrapS = t.wrapT = THREE.RepeatWrapping;
  return t;
}

// skyTexture is the dome: smog glow at the horizon, dark above.
function skyTexture() {
  const c = document.createElement('canvas');
  c.width = 4;
  c.height = 256;
  const g = c.getContext('2d');
  const grad = g.createLinearGradient(0, 0, 0, 256);
  grad.addColorStop(0, '#020205');
  grad.addColorStop(0.5, '#0a0710');
  grad.addColorStop(0.72, '#3a1622');
  grad.addColorStop(0.82, '#7a3020');
  grad.addColorStop(1, '#1a0a10');
  g.fillStyle = grad;
  g.fillRect(0, 0, 4, 256);
  const t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  return t;
}

// adTexture is a tall glowing sign: a kanji, a bowl, an eye, a word.
function adTexture(kind, color) {
  const c = document.createElement('canvas');
  c.width = 128;
  c.height = 384;
  const g = c.getContext('2d');
  g.fillStyle = '#040208';
  g.fillRect(0, 0, 128, 384);
  g.strokeStyle = color;
  g.fillStyle = color;
  g.lineWidth = 4;
  g.shadowColor = color;
  g.shadowBlur = 10;
  g.strokeRect(6, 6, 116, 372);
  g.textAlign = 'center';
  g.textBaseline = 'middle';
  if (kind === 'kanji') {
    g.font = '700 88px sans-serif';
    for (const [ch, y] of [['未', 80], ['来', 190], ['都', 300]]) g.fillText(ch, 64, y);
  } else if (kind === 'eye') {
    g.beginPath();
    g.ellipse(64, 150, 48, 26, 0, 0, PI * 2);
    g.stroke();
    g.beginPath();
    g.arc(64, 150, 14, 0, PI * 2);
    g.fill();
    g.font = '700 34px sans-serif';
    g.fillText('WATCH', 64, 300);
  } else {
    g.beginPath();
    g.arc(64, 120, 40, 0, PI);
    g.stroke();
    for (const x of [44, 64, 84]) { g.beginPath(); g.moveTo(x, 40); g.lineTo(x + 6, 100); g.stroke(); }
    g.font = '700 30px sans-serif';
    g.fillText('NOODLE', 64, 230);
    g.fillText('BAR', 64, 280);
  }
  const t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  return t;
}

export function city(scene, seed) {
  const r = rng(seed ^ 0xc17e);
  const root = new THREE.Group();
  root.userData.city = true;
  scene.add(root);

  // the dome, around and below the view
  const sky = new THREE.Mesh(new THREE.CylinderGeometry(260, 260, 260, 48, 1, true),
    new THREE.MeshBasicMaterial({ map: skyTexture(), side: THREE.BackSide, fog: false, depthWrite: false }));
  sky.position.y = -40;
  root.add(sky);

  // three bands of towers, outside the right and front windows only
  const geo = new THREE.BoxGeometry(1, 1, 1);
  geo.translate(0, 0.5, 0); // origin at the base
  const bands = [
    // top: a rooftop's height against this floor's (we are high up: most
    // roofs sit below or near eye level, a few towers rise past it)
    { n: 70, near: 45, far: 90, w: [8, 16], top: [-35, 12], lit: 0.16, haze: 0.2 },
    { n: 110, near: 90, far: 150, w: [10, 20], top: [-25, 28], lit: 0.13, haze: 0.5 },
    { n: 140, near: 150, far: 240, w: [12, 26], top: [-15, 40], lit: 0.1, haze: 0.78 },
  ];
  const dummy = new THREE.Object3D();
  const tint = new THREE.Color();
  for (const b of bands) {
    const tex = windowsTexture(r, b.lit);
    tex.repeat.set(3, 16); // windows about 1 m apart on a mid-size tower
    const mat = new THREE.MeshBasicMaterial({ map: tex, fog: false });
    const towers = new THREE.InstancedMesh(geo, mat, b.n);
    for (let i = 0; i < b.n; i++) {
      // an angle from the right wall round to the front: x > 0, z > 0 mostly
      const a = -0.35 + r() * (PI / 2 + 0.7);
      const d = b.near + r() * (b.far - b.near);
      const w = b.w[0] + r() * (b.w[1] - b.w[0]);
      const top = b.top[0] + r() * (b.top[1] - b.top[0]);
      dummy.position.set(Math.cos(a) * d, -150, Math.sin(a) * d);
      dummy.rotation.y = r() * PI;
      dummy.scale.set(w, top + 150, w * (0.6 + r() * 0.8));
      dummy.updateMatrix();
      towers.setMatrixAt(i, dummy.matrix);
      tint.setRGB(1, 1, 1).lerp(HAZE, b.haze + r() * 0.1);
      towers.setColorAt(i, tint);
    }
    root.add(towers);
  }

  // tall ad signs on the nearer towers' faces
  const ads = [];
  for (const [kind, color, a, d, y] of [['kanji', '#ff3fae', 0.25, 52, 4], ['noodle', '#29e7ff', 0.95, 58, -2], ['eye', '#ffb02a', 1.35, 70, 10], ['kanji', '#8b5cff', -0.15, 64, 2]]) {
    const m = new THREE.Mesh(new THREE.PlaneGeometry(6, 18), new THREE.MeshBasicMaterial({ map: adTexture(kind, color), fog: false, toneMapped: false }));
    m.position.set(Math.cos(a) * d, y, Math.sin(a) * d);
    m.lookAt(0, y, 0);
    root.add(m);
    ads.push({ m, next: 2 + r() * 8, off: 0 });
  }

  // spinners: a hull and blinking lights, flying slow loops between towers
  const spinners = [];
  for (let i = 0; i < 3; i++) {
    const g = new THREE.Group();
    g.add(new THREE.Mesh(new THREE.BoxGeometry(2.2, 0.5, 1), new THREE.MeshBasicMaterial({ color: 0x0c0e14, fog: false })));
    const glow = new THREE.Mesh(new THREE.BoxGeometry(2.3, 0.08, 1.05), new THREE.MeshBasicMaterial({ color: 0x5ad8ff, fog: false, toneMapped: false }));
    glow.position.y = -0.27;
    g.add(glow);
    const blink = new THREE.Mesh(new THREE.SphereGeometry(0.18, 8, 6), new THREE.MeshBasicMaterial({ color: 0xff2a2a, fog: false, toneMapped: false }));
    blink.position.set(-1.1, 0.3, 0);
    g.add(blink);
    root.add(g);
    spinners.push({ g, blink, rad: 34 + i * 12 + r() * 8, y: 4 + i * 6, speed: 0.025 + r() * 0.02, phase: r() * PI * 2, dir: i % 2 ? 1 : -1 });
  }

  return (dt, t) => {
    for (const s of spinners) {
      const a = s.phase + t * s.speed * s.dir;
      // a loop centred out in the city, through the windows' view
      const cx = 40, cz = 40;
      s.g.position.set(cx + Math.cos(a) * s.rad, s.y + Math.sin(t * 0.3 + s.phase) * 1.5, cz + Math.sin(a) * s.rad);
      s.g.rotation.y = -a + (s.dir > 0 ? 0 : PI);
      s.blink.visible = Math.sin(t * 6 + s.phase) > 0.6;
    }
    for (const ad of ads) {
      ad.next -= dt;
      if (ad.next < 0) { ad.off = 0.1 + r() * 0.3; ad.next = 4 + r() * 10; }
      if (ad.off > 0) { ad.off -= dt; ad.m.visible = r() > 0.4; } else ad.m.visible = true;
    }
  };
}
