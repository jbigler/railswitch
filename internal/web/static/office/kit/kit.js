// The Virtual Office's asset kit: loads a set's glTF files (the built-in set is
// built by tools/office-kit/, see docs/office-kit.md), draws a box for each
// piece the set lacks (D153), recolors the highlight, and drives the named
// moving parts. No import of its own: the dashboard's CSP allows no import
// map, so the caller passes three.js and a GLTFLoader from it.
//
//   // set: /office/set.json ({name, base, have}); pieces: kit/pieces.json
//   const kit = await loadOfficeKit(set, pieces, names, new GLTFLoader(), THREE);
//   const tank = kit.spawn('Pet_Aquarium'); scene.add(tank);
//   kit.point('Furn_Desk', 'top');          // [x, y, z] in the piece's frame
//   kit.setAccent('#ff2b6d');               // neon, accent LEDs, every screen
//   kit.setSecondary('#ffaa00');            // violet trim, jellyfish tentacles
//   kit.colliders(scene);                   // hidden box meshes (*_COL, *_COL1, ...) for your physics
//   kit.animate(scene, t);                  // per frame, t in seconds: pets, fans, bubbles
//   kit.setElevatorDoors(floorNode, 0..1);  // 0 closed, 1 open (also works on the cab)
//   const cat = createRoamer({ walk: kit.spawn('Pet_Cat_Walk'), rest: kit.spawn('Pet_Cat_Sitting'), colliders, bounds });
//   cat.update(dt, player);                 // per frame: wanders, rests, stops near the player
//   const bird = createFlyer({ bird: kit.spawn('Pet_Parrot_Fly'), perches });
//
// Units are meters, origin on the floor (ceiling pieces: on the ceiling, hanging
// down), front +Z, floors 4 m apart. Neon is emissive at strength 5–8: add
// UnrealBloomPass (threshold ~1) for the glow.
const isAccent = (n) => n === 'RS_Neon_Accent' || n === 'RS_LED_Accent' || (n.startsWith('RS_Screen') && n !== 'RS_ScreenOff');
const isSecondary = (n) => n === 'RS_Neon_Secondary';
const IS_COL = /_COL\d*$/; // one box per node: <piece>_COL, <piece>_COL1, ...

export async function loadOfficeKit(set, pieces, names, loader, THREE) {
  const templates = new Map();
  const missing = new Set(); // names drawn as boxes
  const accentMats = new Set();
  const secondaryMats = new Set();
  const have = new Set(set.have);
  const grey = new THREE.MeshStandardMaterial({ name: 'RS_Placeholder', color: 0x9aa0a8, roughness: 0.8 });
  const red = new THREE.MeshStandardMaterial({ name: 'RS_Placeholder_Unknown', color: 0xd03030, roughness: 0.8 });
  const edge = new THREE.LineBasicMaterial({ color: 0x5a6068 });
  // box is a stand-in from the piece's min to its max, with one collider box
  // when the built-in piece has colliders; a name the list lacks is red.
  const box = (name) => {
    const p = pieces.pieces[name];
    if (!p) console.error(`office: ${name} is not in the piece list`);
    const lo = p?.min ?? [-0.25, 0, -0.25], hi = p?.max ?? [0.25, 0.5, 0.25];
    const size = [0, 1, 2].map((k) => Math.max(hi[k] - lo[k], 0.01));
    const geo = new THREE.BoxGeometry(...size);
    geo.translate(...[0, 1, 2].map((k) => (lo[k] + hi[k]) / 2));
    const g = new THREE.Group();
    g.name = name;
    g.add(new THREE.Mesh(geo, p ? grey : red), new THREE.LineSegments(new THREE.EdgesGeometry(geo), edge));
    if (p?.extras?.includes('collider')) {
      const col = new THREE.Mesh(geo, grey);
      col.name = `${name}_COL`;
      g.add(col);
    }
    missing.add(name);
    return g;
  };
  await Promise.all(names.map(async (name) => {
    let root;
    if (have.has(name) && pieces.pieces[name]) {
      try {
        root = (await loader.loadAsync(`${set.base}${name}.gltf`)).scene;
      } catch (e) {
        console.error(`office: ${set.name}'s ${name} did not load`, e);
      }
    }
    root ??= box(name);
    root.traverse((o) => {
      if (IS_COL.test(o.name)) o.visible = false;
      for (const m of [o.material].flat().filter(Boolean)) {
        if (isAccent(m.name)) accentMats.add(m);
        if (isSecondary(m.name)) secondaryMats.add(m);
      }
    });
    templates.set(name, root);
  }));

  return {
    names: [...templates.keys()],
    missing,
    spawn(name) {
      const t = templates.get(name);
      if (!t) throw new Error(`office asset not loaded: ${name}`);
      const o = t.clone(); // shares materials, so setAccent reaches every copy
      // a missing piece says so on hover; not the building's own pieces, whose clicks walk (functions don't survive clone())
      if (missing.has(name) && !name.startsWith('Arch_')) o.userData.pick = { tip: `${name} · missing in ${set.name}`, act: () => {} };
      return o;
    },
    point(piece, name) {
      const v = pieces.pieces[piece]?.points?.[name];
      if (!v) throw new Error(`office: no point ${name} on ${piece}`);
      return v;
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
    createRoamer,
    createFlyer,
  };
}

// motion is the nearest ancestor's userData that carries a walker's or flyer's
// state (createRoamer and createFlyer set it on the object they were given,
// which may be a wrapper above the model's own root node).
function motion(o) {
  for (let p = o.parent; p; p = p.parent) {
    const u = p.userData;
    if (u.gait !== undefined || u.flap !== undefined || u.typing !== undefined) return u;
  }
  return {};
}

// limbAngle is an android limb's rotation about x from its rest pose, for
// the walk cycle (u.phase: strides, u.gait: 0 standing to 1 walking), the
// agent's typing (u.typing) and its sitting (u.sit: 0 to 1). Negative swings
// forward. A model with knees and elbows (Shin_L/R, Forearm_L/R under the
// leg and arm) walks with them: the knee bends as the leg swings through
// and straightens to land, the elbows stay soft; one without swings each
// limb whole, as the kit's first androids did.
function limbAngle(o, part, side, u, t, seed) {
  const gait = u.gait ?? 0, sit = u.sit ?? 0;
  const ph = (u.phase ?? 0) * Math.PI * 2 + (side === 'L' ? 0 : Math.PI); // this side's leg
  const jointed = (o.userData.jointed ??= o.children.some((c) => /_(Shin|Forearm)_[LR]$/.test(c.name)));
  const walking = 1 - sit;
  switch (part) {
    case 'Leg': { // the hip: forward while sin > 0; folded flat to sit
      const swing = -Math.sin(ph) * (jointed ? 0.5 : 0.55) * gait;
      return swing * walking - sit * Math.PI / 2;
    }
    case 'Shin': { // the knee: bent through the swing (the leg moving forward: cos > 0), most just after
      // the toe leaves the ground, before the leg passes under the hip (ph = 0); nearly straight in stance
      const flex = (Math.max(0, Math.cos(ph + 0.4)) ** 2 * 1.0 + 0.1) * gait;
      return flex * walking + sit * Math.PI / 2;
    }
    case 'Arm': { // the shoulder: against the same side's leg; reaching for the keys while typing
      if (u.typing) return jointed ? -0.55 : -0.95 + Math.sin(t * 14 + (side === 'L' ? 0 : 1.7)) * 0.05;
      return (Math.sin(ph) * (jointed ? 0.4 : 0.45) * gait) * walking - (jointed ? 0.25 : 0.6) * sit + Math.sin(t * 1.3 + seed) * 0.03;
    }
    case 'Forearm': { // the elbow: soft, more as the arm swings forward; level over the keys while typing
      if (u.typing) return -0.95 + Math.sin(t * 14 + (side === 'L' ? 0 : 1.7)) * 0.06;
      return -(0.12 + (0.22 + 0.3 * Math.max(0, -Math.sin(ph))) * gait) * walking - 1.0 * sit; // seated: forearms over the lap
    }
  }
  return 0;
}

// Remember each moving node's authored transform once, then offset from it.
function rest(o) {
  return (o.userData.rest ??= { p: o.position.clone(), r: o.rotation.clone(), s: o.scale.clone() });
}

/** Idle motion for every named moving part under root. t in seconds.
 * Walkers' legs and the flyer's wings follow their root's userData (gait, phase,
 * flap), which createRoamer and createFlyer set. */
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
    if (/^Jelly_\d+_T\d+$/.test(n)) {
      // tentacles trail the bell: they flare out as it contracts and sway out of step
      const r = rest(o);
      const ang = Math.atan2(r.p.z, r.p.x);
      const flare = 0.08 + Math.sin(t * 1.6 + (o.parent?.id ?? 0) * 1.37 - 0.6) * 0.12;
      o.rotation.x = r.r.x - flare * Math.sin(ang) + Math.sin(t * 1.1 + seed) * 0.12;
      o.rotation.z = r.r.z + flare * Math.cos(ang) + Math.cos(t * 0.8 + seed * 0.7) * 0.1;
      return;
    }
    if (/^Jelly_\d+$/.test(n)) {
      const r = rest(o);
      const pulse = Math.sin(t * 1.6 + seed);
      o.position.y = r.p.y + Math.sin(t * 0.3 + seed) * 0.15 + pulse * 0.01;
      o.scale.set(r.s.x * (1 + pulse * 0.08), r.s.y * (1 - pulse * 0.08), r.s.z * (1 + pulse * 0.08));
      return;
    }
    if (n.endsWith('_Bubbles')) { const r = rest(o); o.position.y = r.p.y + ((t * 0.08) % 0.06); return; }
    const limb = /_(Leg|Arm|Shin|Forearm)_([LR])$/.exec(n);
    if (limb) { o.rotation.x = rest(o).r.x + limbAngle(o, limb[1], limb[2], motion(o), t, seed); return; }
    const leg = /_Leg_([FB])([LR])$/.exec(n);
    if (leg) { // trot: front-left with back-right, front-right with back-left
      const r = rest(o), u = motion(o);
      const off = (leg[1] === 'F') === (leg[2] === 'L') ? 0 : Math.PI;
      o.rotation.x = r.r.x + Math.sin((u.phase ?? 0) * Math.PI * 2 + off) * 0.55 * (u.gait ?? 0);
      return;
    }
    const wing = /_Wing_([LR])$/.exec(n);
    if (wing) { // Wing_L sits at +x; folded at rest, spread and beating in flight
      const r = rest(o), f = motion(o).flap ?? 0;
      o.rotation.z = r.r.z + (wing[1] === 'L' ? 1 : -1) * f * (1.15 + Math.sin(t * 16) * 0.55);
      return;
    }
    if (n.endsWith('_Tail')) {
      const r = rest(o);
      o.rotation.y = r.r.y + Math.sin(t * (n.includes('Dog') ? 6 : 1.5) + seed) * 0.35;
      return;
    }
    if (n.endsWith('_Head')) {
      const r = rest(o);
      o.rotation.y = r.r.y + Math.sin(t * 0.35 + seed) * 0.5;
      o.rotation.x = r.r.x + (n.startsWith('Parrot') ? Math.max(0, Math.sin(t * 3 + seed)) * 0.25 : 0);
      return;
    }
    if (n === 'CatCurled_Body' || n === 'Dog_Body' || n === 'Gecko' || n === 'Pet_Dog_Lying') {
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

const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v));
const between = ([lo, hi], rng) => lo + (hi - lo) * rng();
const angleTo = (from, to) => ((to - from + Math.PI * 3) % (Math.PI * 2)) - Math.PI;

/**
 * A pet that wanders a floor: it rests a while, picks a free spot (or one of its
 * homes), walks there around the colliders, and rests again. It stops while the
 * player is within `shy` meters.
 *   walk      the walking model (Pet_Cat_Walk, Pet_Dog_Walk): legs animate from its userData
 *   rest      the model shown while resting (Pet_Cat_Sitting, Pet_Dog_Lying), or null
 *   colliders kit.colliders(scene); boxes from the floor up to `height` block the pet
 *   bounds    { minX, maxX, minZ, maxZ, y }: the walkable floor, world coordinates
 *   homes     [{ x, z, ry }]: spots it goes back to (a bed), with chance homeChance
 *   bob, sway a biped's body dip (m) and roll (rad) per step at full gait; 0 for a pet
 * Both models must be in the scene; the roamer sets their position, rotation and visibility.
 */
export function createRoamer({ walk, rest = null, colliders = [], bounds, start = null, homes = [], homeChance = 0.35,
  radius = 0.25, height = 0.5, speed = 0.6, stride = 0.35, idle = [4, 12], shy = 1.2, cell = 0.25, bob = 0, sway = 0, rng = Math.random }) {
  const cols = Math.ceil((bounds.maxX - bounds.minX) / cell), rows = Math.ceil((bounds.maxZ - bounds.minZ) / cell);
  const boxes = [];
  for (const c of colliders) {
    c.updateWorldMatrix(true, false);
    if (!c.geometry.boundingBox) c.geometry.computeBoundingBox();
    const b = c.geometry.boundingBox.clone().applyMatrix4(c.matrixWorld);
    if (b.max.y > bounds.y + 0.05 && b.min.y < bounds.y + height) boxes.push(b);
  }
  const cx = (i) => bounds.minX + (i + 0.5) * cell, cz = (j) => bounds.minZ + (j + 0.5) * cell;
  const solid = (x, z) => x < bounds.minX + radius || x > bounds.maxX - radius || z < bounds.minZ + radius || z > bounds.maxZ - radius
    || boxes.some((b) => x > b.min.x - radius && x < b.max.x + radius && z > b.min.z - radius && z < b.max.z + radius);
  const grid = new Uint8Array(cols * rows); // 1 = blocked
  const open = [];
  for (let j = 0; j < rows; j++) for (let i = 0; i < cols; i++) {
    grid[j * cols + i] = solid(cx(i), cz(j)) ? 1 : 0;
    if (!grid[j * cols + i]) open.push(j * cols + i);
  }
  const key = (x, z) => clamp(Math.floor((z - bounds.minZ) / cell), 0, rows - 1) * cols + clamp(Math.floor((x - bounds.minX) / cell), 0, cols - 1);
  const clear = (ax, az, bx, bz) => {
    const n = Math.ceil(Math.hypot(bx - ax, bz - az) / (cell * 0.5));
    for (let s = 1; s <= n; s++) if (grid[key(ax + (bx - ax) * s / n, az + (bz - az) * s / n)]) return false;
    return true;
  };

  // A* over the grid, 8 neighbours without cutting corners, then string-pulled.
  function findPath(sx, sz, tx, tz) {
    const start = key(sx, sz), goal = key(tx, tz);
    if (grid[goal]) return null;
    if (start === goal) return [[tx, tz]];
    const g = new Float32Array(cols * rows).fill(Infinity), from = new Int32Array(cols * rows).fill(-1);
    const gi = goal % cols, gj = (goal / cols) | 0;
    const h = (k) => Math.hypot(k % cols - gi, ((k / cols) | 0) - gj);
    const heap = [];
    const push = (f, k) => {
      heap.push([f, k]);
      for (let i = heap.length - 1; i > 0;) { const p = (i - 1) >> 1; if (heap[p][0] <= heap[i][0]) break; [heap[p], heap[i]] = [heap[i], heap[p]]; i = p; }
    };
    const pop = () => {
      const top = heap[0], last = heap.pop();
      if (heap.length) {
        heap[0] = last;
        for (let i = 0; ;) {
          const l = 2 * i + 1, r = l + 1; let m = i;
          if (l < heap.length && heap[l][0] < heap[m][0]) m = l;
          if (r < heap.length && heap[r][0] < heap[m][0]) m = r;
          if (m === i) break;
          [heap[m], heap[i]] = [heap[i], heap[m]]; i = m;
        }
      }
      return top;
    };
    g[start] = 0; push(h(start), start);
    while (heap.length) {
      const [f, k] = pop();
      if (k === goal) break;
      if (f > g[k] + h(k) + 1e-6) continue; // stale entry
      const i = k % cols, j = (k / cols) | 0;
      for (let dj = -1; dj <= 1; dj++) for (let di = -1; di <= 1; di++) {
        const ni = i + di, nj = j + dj;
        if ((!di && !dj) || ni < 0 || nj < 0 || ni >= cols || nj >= rows) continue;
        const nk = nj * cols + ni;
        if (grid[nk] || (di && dj && (grid[j * cols + ni] || grid[nj * cols + i]))) continue;
        const ng = g[k] + (di && dj ? Math.SQRT2 : 1);
        if (ng < g[nk]) { g[nk] = ng; from[nk] = k; push(ng + h(nk), nk); }
      }
    }
    if (from[goal] < 0) return null;
    const pts = [];
    for (let k = goal; k !== start; k = from[k]) pts.push([cx(k % cols), cz((k / cols) | 0)]);
    pts.reverse();
    pts[pts.length - 1] = [tx, tz];
    const out = [];
    for (let ax = sx, az = sz, i = 0; i < pts.length;) {
      let far = i;
      while (far + 1 < pts.length && clear(ax, az, pts[far + 1][0], pts[far + 1][1])) far++;
      out.push(pts[far]); [ax, az] = pts[far]; i = far + 1;
    }
    return out;
  }

  const home0 = start ?? homes[0];
  let x, z, heading = home0?.ry ?? 0;
  if (home0) { x = home0.x; z = home0.z; } else { const k = open[Math.floor(rng() * open.length)]; x = cx(k % cols); z = cz((k / cols) | 0); }
  let path = [], target = null, walking = false, timer = between(idle, rng), gait = 0;
  walk.userData.phase = 0; walk.userData.gait = 0;
  const show = () => { walk.visible = walking || !rest; if (rest) rest.visible = !walking; };
  const place = () => {
    for (const o of [walk, rest]) if (o) { o.position.set(x, bounds.y, z); o.rotation.y = heading; }
    walk.userData.gait = gait;
    // A biped's body: highest as a leg passes under it, lowest with both feet down
    // (twice a stride), rolling a little over the leg it stands on.
    const p = walk.userData.phase * Math.PI * 2;
    walk.position.y -= bob * gait * Math.sin(p) ** 2;
    walk.rotation.z = sway * gait * Math.sin(p);
  };
  show(); place();

  return {
    get position() { return { x, z, heading, walking }; },
    /** dt in seconds; avoid: the player's position ({x, z}), or null. */
    update(dt, avoid = null) {
      if (!walking) {
        timer -= dt;
        if (timer <= 0) {
          for (let tries = 0; tries < 6 && !walking; tries++) {
            const h = homes.length && rng() < homeChance ? homes[Math.floor(rng() * homes.length)] : null;
            const k = open[Math.floor(rng() * open.length)];
            target = h ?? { x: cx(k % cols), z: cz((k / cols) | 0) };
            path = findPath(x, z, target.x, target.z) ?? [];
            walking = path.length > 0;
          }
          timer = walking ? 0 : 2; // nowhere to go: look again soon
          show();
        }
      }
      let want = 0;
      if (walking) {
        const [wx, wz] = path[0];
        const dx = wx - x, dz = wz - z, d = Math.hypot(dx, dz);
        const turn = d > 1e-4 ? angleTo(heading, Math.atan2(dx, dz)) : 0;
        heading += clamp(turn, -3.5 * dt, 3.5 * dt);
        if (!(avoid && Math.hypot(avoid.x - x, avoid.z - z) < shy)) {
          want = Math.abs(turn) > 1.2 ? 0.25 : 1; // turn mostly on the spot
          const step = Math.min(d, speed * want * dt);
          if (d > 1e-4) { x += dx / d * step; z += dz / d * step; }
          walk.userData.phase += step / stride;
          if (d - step < 0.02) {
            path.shift();
            if (!path.length) {
              walking = false; want = 0; timer = between(idle, rng);
              if (target.ry !== undefined) heading = target.ry;
              show();
            }
          }
        }
      }
      gait += clamp(want - gait, -4 * dt, 4 * dt);
      place();
    },
  };
}

/**
 * A bird that rests on perches and flies between them in an arc, flapping
 * (Pet_Parrot_Fly: wings animate from its userData.flap).
 *   perches  [{ x, y, z, ry }]: where its feet go (a Furn_ParrotPerch bar is 1.284 m up)
 */
export function createFlyer({ bird, perches, speed = 2.2, idle = [6, 16], arc = 0.7, rng = Math.random }) {
  let at = 0, from = null, to = null, s = 0, dur = 1, timer = between(idle, rng), flap = 0;
  const pos = { x: perches[0].x, y: perches[0].y, z: perches[0].z };
  let heading = perches[0].ry ?? 0;
  return {
    get position() { return { ...pos, heading, flying: !!to }; },
    update(dt) {
      if (!to) {
        timer -= dt;
        if (timer <= 0 && perches.length > 1) {
          let n = at;
          while (n === at) n = Math.floor(rng() * perches.length);
          from = perches[at]; to = perches[n]; at = n; s = 0;
          dur = Math.max(1, Math.hypot(to.x - from.x, to.y - from.y, to.z - from.z) / speed);
        }
      }
      if (to) {
        s = Math.min(1, s + dt / dur);
        const e = s * s * (3 - 2 * s), u = 1 - e; // ease in and out
        const span = Math.hypot(to.x - from.x, to.z - from.z);
        const mx = (from.x + to.x) / 2, mz = (from.z + to.z) / 2, my = Math.max(from.y, to.y) + arc + span * 0.1;
        pos.x = u * u * from.x + 2 * u * e * mx + e * e * to.x;
        pos.y = u * u * from.y + 2 * u * e * my + e * e * to.y;
        pos.z = u * u * from.z + 2 * u * e * mz + e * e * to.z;
        const goal = s < 0.9 ? Math.atan2(to.x - from.x, to.z - from.z) : (to.ry ?? heading);
        heading += clamp(angleTo(heading, goal), -4 * dt, 4 * dt);
        if (s >= 1) { to = null; timer = between(idle, rng); }
      }
      flap += clamp((to && s < 0.95 ? 1 : 0) - flap, -4 * dt, 4 * dt);
      bird.userData.flap = flap;
      bird.position.set(pos.x, pos.y, pos.z);
      bird.rotation.y = heading;
    },
  };
}
