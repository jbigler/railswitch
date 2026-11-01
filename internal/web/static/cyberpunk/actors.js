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

// agentAndroid is the agent partner, standing at its desk; set(state)
// recolours its visor and ring (its own copies of their materials).
export function agentAndroid(k) {
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
  g.set = (s) => {
    state = stateColor[s] ? s : 'off';
    base.setHex(stateColor[state]).multiplyScalar(1.6);
    visor.color.copy(base);
    ring.color.copy(base);
    g.userData.typing = state === 'working';
  };
  g.tick = (dt, t) => {
    const waits = state === 'permission' || state === 'question';
    ring.color.copy(base).multiplyScalar(waits ? 0.6 + Math.abs(Math.sin(t * 4)) * 0.8 : 1);
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
