// The agent partner, built in code (the starship's robot, D144): its visor
// and ring follow the agent's state.
import { THREE } from './world.js';

function mat(color, extra = {}) {
  return new THREE.MeshStandardMaterial({ color, roughness: 0.7, ...extra });
}

function part(geo, m, x, y, z) {
  const p = new THREE.Mesh(geo, m);
  p.position.set(x, y, z);
  return p;
}

// States are agentstatus's (D58): permission and question wait on you.
export const stateColor = {
  working: 0x5fd3ff,
  permission: 0xff6b6b,
  question: 0xffc857,
  done: 0x63e07c,
  idle: 0x8fa3c7,
  off: 0x56627a,
};

// robot is the agent partner; set(state) recolours its visor and status ring.
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
  head.add(part(new THREE.SphereGeometry(0.2, 16, 8, -Math.PI / 2.6, Math.PI / 1.3, Math.PI / 3.2, Math.PI / 3), visor, 0, 0, 0.06));
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
