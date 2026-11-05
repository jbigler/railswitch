// The androids (office kit, Char_Android_*): the agent's partner, whose visor
// and status ring follow the agent's state and whose arms type while it works,
// and the wanderers, who roam the floor and have something to say.
import { THREE } from './world.js';

// States are agentstatus's (D58): permission and question wait on you.
export const stateColor = {
  working: 0x5fd3ff,
  permission: 0xff6b6b,
  question: 0xffc857,
  done: 0x63e07c,
  idle: 0x8fa3c7,
  off: 0x56627a,
};

// SIT_DROP is how far an android's body comes down to sit on a chair: its
// hips (0.93 m standing) to just above the seat (0.51 m).
const SIT_DROP = 0.39;
// SIT_TIME is how long (s) the agent takes to sit down or stand up, the
// desk going down or up with it, as a real sit-stand desk does.
const SIT_TIME = 2.5;

// agentAndroid is the agent partner at its sit-stand desk; set(state)
// recolours its visor and ring (its own copies of their materials). It
// stands while the agent works, the desk's top raised (desk.top, by
// desk.travel), and sits otherwise, its chair (desk.chair) pulled in; the
// arms type while it works. Without desk.top (a set lacking the desk) it
// still sits and stands; only the top stays put.
export function agentAndroid(k, desk = {}) {
  const g = k.spawn('Char_Android_Agent');
  const visor = new THREE.MeshBasicMaterial({ toneMapped: false });
  const ring = new THREE.MeshBasicMaterial({ toneMapped: false });
  g.traverse((o) => {
    if (!o.isMesh) return;
    if (o.material.name === 'RS_Android_Visor_Agent') o.material = visor;
    if (o.material.name === 'RS_Android_Status') o.material = ring;
  });
  const base = new THREE.Color();
  let state = 'off';
  g.userData.typing = false;
  g.userData.sit = 1; // 0 standing, 1 sitting, eased: what the limbs and the desk follow
  let raw = 1; // the same, not eased
  const topY = desk.top?.position.y ?? 0;
  let chairHome = null;
  g.set = (s) => {
    state = stateColor[s] ? s : 'off';
    base.setHex(stateColor[state]).multiplyScalar(1.6);
    visor.color.copy(base);
    ring.color.copy(base);
  };
  g.tick = (dt, t) => {
    const waits = state === 'permission' || state === 'question';
    ring.color.copy(base).multiplyScalar(waits ? 0.6 + Math.abs(Math.sin(t * 4)) * 0.8 : 1);
    // Stand to work, sit otherwise; waiting on you keeps the posture it had.
    const u = g.userData;
    const want = state === 'working' ? 0 : waits ? raw : 1;
    raw += Math.max(-dt / SIT_TIME, Math.min(dt / SIT_TIME, want - raw));
    const s = (u.sit = raw * raw * (3 - 2 * raw)); // eased, no jolt at either end
    u.floorY ??= g.position.y;
    g.position.y = u.floorY - SIT_DROP * s;
    // The arms type only once it is up and the desk with it.
    u.typing = state === 'working' && raw < 0.05;
    if (desk.top) desk.top.position.y = topY + (desk.travel ?? 0) * (1 - s);
    if (desk.chair) { // pushed back as it stands, pulled in to sit
      chairHome ??= desk.chair.position.clone();
      const back = new THREE.Vector3(Math.sin(g.rotation.y), 0, Math.cos(g.rotation.y)).multiplyScalar(-0.4 * (1 - s));
      desk.chair.position.copy(chairHome).add(back);
    }
  };
  g.set('off');
  return g;
}

const LINES = [
  'All systems nominal.', 'Running diagnostics.', 'Have you tried turning it off and on again?',
  'Merge conflicts build character.', 'I dreamt of electric sheep again.', 'The build is green. For now.',
  'Deploying… just kidding.', 'Your coffee is 14 minutes old.', 'Rebasing my thoughts.', 'Beep. Boop. Hello.',
];
const NAMES = ['Unit A-7', 'Kestrel', 'Vector', 'Nyx', 'Patch', 'Cobalt', 'Sable', 'Rivet', 'Echo', 'Tango'];

// wanderer is a roaming android of model (Char_Android_A|B|C) with a name and
// a line for each click; the caller roams it (createRoamer, walk only).
export function wanderer(k, model, r, say) {
  const g = k.spawn(model);
  const name = NAMES[Math.floor(r() * NAMES.length)];
  let i = Math.floor(r() * LINES.length);
  g.userData.pick = { tip: `${name}, an android`, act: () => say(`${name}: ${LINES[i++ % LINES.length]}`, g) };
  return g;
}
