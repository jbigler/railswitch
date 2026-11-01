// The ship's crew, built in code: the deck cat and the agent partner.
import { THREE } from './world.js';

function mat(color, extra = {}) {
  const m = new THREE.MeshStandardMaterial({ color, roughness: 0.7, ...extra });
  m.userData.own = true;
  return m;
}

function part(geo, m, x, y, z) {
  const p = new THREE.Mesh(geo, m);
  p.position.set(x, y, z);
  p.castShadow = true;
  return p;
}

// Cat coats, picked by the deck's seed.
const coats = [
  { fur: 0xe39a4a, belly: 0xf6dcb7 }, // ginger
  { fur: 0x2b2b30, belly: 0x3c3c44 }, // black
  { fur: 0x9aa0a8, belly: 0xd9dde2 }, // grey
  { fur: 0xf2efe8, belly: 0xffffff }, // white
  { fur: 0x6b4a32, belly: 0xc9a27c }, // tabby brown
];
const catNames = ['Nova', 'Pixel', 'Quark', 'Biscuit', 'Comet', 'Miso', 'Tribble', 'Nebula', 'Mochi', 'Sputnik'];

// cat is a low-poly cat that wanders between points of the floor
// (bounds {x0,x1,z0,z1}), sits a while, and purrs when clicked.
export function cat(r, bounds, say) {
  const coat = coats[Math.floor(r() * coats.length)];
  const name = catNames[Math.floor(r() * catNames.length)];
  const fur = mat(coat.fur);
  const belly = mat(coat.belly);
  const pink = mat(0xff9fb0);
  const eye = mat(0x9cff6a, { emissive: 0x2a6d10 });
  const g = new THREE.Group();
  const body = part(new THREE.CapsuleGeometry(0.17, 0.42, 4, 8), fur, 0, 0.32, 0);
  body.rotation.z = Math.PI / 2;
  g.add(body);
  g.add(part(new THREE.SphereGeometry(0.15, 10, 8), belly, 0, 0.27, 0));
  const head = new THREE.Group();
  head.position.set(0.4, 0.5, 0);
  head.add(part(new THREE.SphereGeometry(0.16, 12, 10), fur, 0, 0, 0));
  for (const z of [-0.08, 0.08]) {
    const ear = part(new THREE.ConeGeometry(0.06, 0.12, 4), fur, 0, 0.16, z);
    ear.rotation.y = Math.PI / 4;
    head.add(ear);
    head.add(part(new THREE.SphereGeometry(0.025, 6, 6), eye, 0.14, 0.03, z * 0.8));
  }
  head.add(part(new THREE.SphereGeometry(0.025, 6, 6), pink, 0.16, -0.03, 0));
  g.add(head);
  const legs = [];
  for (const [x, z] of [[0.22, 0.09], [0.22, -0.09], [-0.22, 0.09], [-0.22, -0.09]]) {
    const l = part(new THREE.CylinderGeometry(0.04, 0.035, 0.24, 6), fur, x, 0.12, z);
    legs.push(l);
    g.add(l);
  }
  const tail = new THREE.Group();
  tail.position.set(-0.4, 0.38, 0);
  const t = part(new THREE.CylinderGeometry(0.03, 0.02, 0.45, 6), fur, 0, 0.2, 0);
  tail.add(t);
  tail.rotation.z = 0.5;
  g.add(tail);
  g.userData.pick = { tip: `${name} the cat`, act: () => say(`${name}: purrrr… 🐾`, g) };
  const pos = new THREE.Vector3((bounds.x0 + bounds.x1) / 2, 0, (bounds.z0 + bounds.z1) / 2);
  let goal = pos.clone();
  let rest = 2;
  g.position.copy(pos);
  g.tick = (dt, time) => {
    tail.rotation.x = Math.sin(time * 2.2) * 0.4;
    if (rest > 0) {
      rest -= dt;
      body.position.y = 0.28;
      for (const l of legs) l.rotation.z = 0;
      if (rest <= 0) goal = new THREE.Vector3(bounds.x0 + r() * (bounds.x1 - bounds.x0), 0, bounds.z0 + r() * (bounds.z1 - bounds.z0));
      return;
    }
    const d = goal.clone().sub(g.position);
    if (d.length() < 0.05) {
      rest = 2 + r() * 6;
      return;
    }
    const step = Math.min(d.length(), dt * 0.9);
    g.position.add(d.normalize().multiplyScalar(step));
    g.rotation.y = Math.atan2(-d.z, d.x);
    body.position.y = 0.32;
    legs.forEach((l, i) => { l.rotation.z = Math.sin(time * 10 + (i % 2 ? 0 : Math.PI)) * 0.5; });
  };
  return g;
}

// The agent partner's look follows its state.
// States are agentstatus's (D58): permission and question wait on you.
const stateColor = {
  working: 0x5fd3ff,
  permission: 0xff6b6b,
  question: 0xffc857,
  done: 0x63e07c,
  idle: 0x8fa3c7,
  off: 0x56627a,
};

// robot is the agent partner, seated at its desk; set(state) recolours
// its visor and status ring.
export function robot(r, accent) {
  const shell = mat(0xe8edf5, { metalness: 0.3, roughness: 0.4 });
  const trim = mat(accent, { metalness: 0.4, roughness: 0.35 });
  const visor = mat(0x111a2a, { emissive: stateColor.off, emissiveIntensity: 1.2 });
  const g = new THREE.Group();
  g.add(part(new THREE.CapsuleGeometry(0.28, 0.45, 4, 10), shell, 0, 1.05, 0));
  const belt = part(new THREE.TorusGeometry(0.29, 0.04, 6, 20), trim, 0, 1.0, 0);
  belt.rotation.x = Math.PI / 2;
  g.add(belt);
  const head = new THREE.Group();
  head.position.set(0, 1.72, 0);
  head.add(part(new THREE.SphereGeometry(0.25, 16, 12), shell, 0, 0, 0));
  const v = part(new THREE.SphereGeometry(0.2, 16, 8, -Math.PI / 2.6, Math.PI / 1.3, Math.PI / 3.2, Math.PI / 3), visor, 0, 0, 0.06);
  head.add(v);
  head.add(part(new THREE.CylinderGeometry(0.012, 0.012, 0.22, 6), trim, 0.1, 0.3, 0));
  const bulb = part(new THREE.SphereGeometry(0.04, 8, 8), mat(0xffffff, { emissive: stateColor.off }), 0.1, 0.42, 0);
  head.add(bulb);
  g.add(head);
  for (const x of [-0.36, 0.36]) {
    const arm = part(new THREE.CapsuleGeometry(0.07, 0.4, 4, 6), shell, x, 1.05, 0.15);
    arm.rotation.x = -1.0;
    g.add(arm);
  }
  const ring = new THREE.Mesh(new THREE.TorusGeometry(0.55, 0.03, 6, 40), mat(0x000000, { emissive: stateColor.off }));
  ring.rotation.x = Math.PI / 2;
  ring.position.y = 0.05;
  g.add(ring);
  let state = 'off';
  const phase = r() * 6;
  g.set = (s) => {
    state = stateColor[s] ? s : 'off';
    for (const m of [visor, bulb.material, ring.material]) m.emissive.setHex(stateColor[state]);
  };
  g.tick = (dt, t) => {
    head.rotation.y = Math.sin(t * 0.6 + phase) * (state === 'working' ? 0.5 : 0.15);
    head.position.y = 1.72 + Math.sin(t * 2 + phase) * 0.015;
    bulb.material.emissiveIntensity = state === 'working' ? 1 + Math.sin(t * 8) * 0.8 : 1;
    ring.scale.setScalar(1 + (state === 'permission' || state === 'question' ? Math.sin(t * 4) * 0.08 : 0));
  };
  return g;
}
