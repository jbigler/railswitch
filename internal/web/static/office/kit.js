// The virtual office's asset kit: loads the GLBs in ./models/ (built by
// tools/office-kit/, see docs/office-kit.md), recolors the highlight, and drives
// the named moving parts. No import of its own: the dashboard's CSP allows no
// import map, so the caller passes a GLTFLoader from whatever three.js it bundles.
//
//   const kit = await loadOfficeKit('/static/office/models/', names, new GLTFLoader());
//   const tank = kit.spawn('Pet_Aquarium'); scene.add(tank);
//   kit.setAccent('#ff2b6d');               // neon, accent LEDs, every screen
//   kit.setSecondary('#ffaa00');            // violet trim, jellyfish tentacles
//   kit.colliders(scene);                   // hidden box meshes (*_COL, *_COL1, ...) for your physics
//   kit.animate(scene, t);                  // per frame, t in seconds: pets, fans, bubbles
//   kit.setElevatorDoors(floorNode, 0..1);  // 0 closed, 1 open (also works on the cab)
//
// Units are meters, origin on the floor (ceiling pieces: on the ceiling, hanging
// down), front +Z, floors 4 m apart. Neon is emissive at strength 5–8: add
// UnrealBloomPass (threshold ~1) for the glow.
const isAccent = (n) => n === 'RS_Neon_Accent' || n === 'RS_LED_Accent' || (n.startsWith('RS_Screen') && n !== 'RS_ScreenOff');
const isSecondary = (n) => n === 'RS_Neon_Secondary';
const IS_COL = /_COL\d*$/; // one box per node: <piece>_COL, <piece>_COL1, ...

export async function loadOfficeKit(baseUrl, names, loader) {
  const templates = new Map();
  const accentMats = new Set();
  const secondaryMats = new Set();
  await Promise.all(names.map(async (name) => {
    const gltf = await loader.loadAsync(`${baseUrl}${name}.glb`);
    gltf.scene.traverse((o) => {
      if (IS_COL.test(o.name)) o.visible = false;
      for (const m of [o.material].flat().filter(Boolean)) {
        if (isAccent(m.name)) accentMats.add(m);
        if (isSecondary(m.name)) secondaryMats.add(m);
      }
    });
    templates.set(name, gltf.scene);
  }));

  return {
    names: [...templates.keys()],
    spawn(name) {
      const t = templates.get(name);
      if (!t) throw new Error(`office asset not loaded: ${name}`);
      return t.clone(); // shares materials, so setAccent reaches every copy
    },
    setAccent(color) { for (const m of accentMats) m.emissive.set(color); },
    setSecondary(color) { for (const m of secondaryMats) m.emissive.set(color); },
    colliders(root) {
      const out = [];
      root.traverse((o) => { if (o.isMesh && IS_COL.test(o.name)) out.push(o); });
      return out;
    },
    animate,
    setElevatorDoors,
  };
}

// Remember each moving node's authored transform once, then offset from it.
function rest(o) {
  return (o.userData.rest ??= { p: o.position.clone(), r: o.rotation.clone(), s: o.scale.clone() });
}

/** Idle motion for every named moving part under root. t in seconds. */
export function animate(root, t) {
  root.traverse((o) => {
    const n = o.name;
    if (!n) return;
    const seed = o.id * 1.37;
    if (n === 'Fan_Blades') { o.rotation.y = t * 4; return; }
    if (/^Fish_\d/.test(n)) {
      const r = rest(o);
      o.position.x = r.p.x + Math.sin(t * 0.4 + seed) * 0.25;
      o.position.z = r.p.z + Math.sin(t * 0.7 + seed) * 0.05;
      o.position.y = r.p.y + Math.sin(t * 1.1 + seed) * 0.02;
      o.rotation.y = Math.cos(t * 0.4 + seed) >= 0 ? Math.PI / 2 : -Math.PI / 2; // face the way it swims
      return;
    }
    if (/^Jelly_\d/.test(n)) {
      const r = rest(o);
      const pulse = Math.sin(t * 1.6 + seed);
      o.position.y = r.p.y + Math.sin(t * 0.3 + seed) * 0.15 + pulse * 0.01;
      o.scale.set(r.s.x * (1 + pulse * 0.08), r.s.y * (1 - pulse * 0.08), r.s.z * (1 + pulse * 0.08));
      return;
    }
    if (n.endsWith('_Bubbles')) { const r = rest(o); o.position.y = r.p.y + ((t * 0.08) % 0.06); return; }
    if (/(Cat|Dog)_Tail$/.test(n)) { const r = rest(o); o.rotation.y = r.r.y + Math.sin(t * (n.startsWith('Dog') ? 6 : 1.5) + seed) * 0.35; return; }
    if (/(Cat|Dog|Parrot)_Head$/.test(n)) {
      const r = rest(o);
      o.rotation.y = r.r.y + Math.sin(t * 0.35 + seed) * 0.5;
      o.rotation.x = r.r.x + (n.startsWith('Parrot') ? Math.max(0, Math.sin(t * 3 + seed)) * 0.25 : 0);
      return;
    }
    if (n === 'CatCurled_Body' || n === 'Dog_Body' || n === 'Gecko') {
      const r = rest(o);
      const b = 1 + Math.sin(t * 1.8 + seed) * 0.025; // breathing
      o.scale.set(r.s.x * b, r.s.y * b, r.s.z);
    }
  });
}

/** Slide an elevator's landing doors (Door_L/R) or cab doors (CabDoor_L/R): 0 closed, 1 open. */
export function setElevatorDoors(root, open) {
  root.traverse((o) => {
    const m = /^(Cab)?Door_([LR])$/.exec(o.name);
    if (!m) return;
    const r = rest(o);
    const travel = m[1] ? 0.5 : 0.58; // leaves a lip so the leaves stay in their pockets
    o.position.x = r.p.x + (m[2] === 'L' ? -1 : 1) * travel * Math.min(1, Math.max(0, open));
  });
}
