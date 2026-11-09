// The office kit's roamers (static/office/kit/kit.js) give way to each other (#163).
import { test } from "node:test";
import assert from "node:assert/strict";
import { createRoamer } from "../static/office/kit/kit.js";

const body = () => ({ userData: {}, visible: true, position: { y: 0, set(x, y, z) { Object.assign(this, { x, y, z }); } }, rotation: {} });

test("roamers in a corridor never walk through one another and keep moving", () => {
  let seed = 7;
  const rng = () => ((seed = (seed * 1103515245 + 12345) % 2 ** 31) / 2 ** 31);
  const bounds = { minX: -6, maxX: 6, minZ: -1, maxZ: 1, y: 0 };
  const starts = [{ x: -5, z: 0, ry: 0 }, { x: 0, z: 0, ry: 0 }, { x: 5, z: 0, ry: 0 }];
  const rs = starts.map((start) => createRoamer({ walk: body(), bounds, start, speed: 0.8, radius: 0.3, idle: [0.5, 2], rng }));
  const moved = rs.map(() => 0);
  let closest = Infinity;
  for (let t = 0; t < 600 * 30; t++) {
    rs.forEach((r, i) => {
      const a = r.position;
      r.update(1 / 30, null, rs.filter((o) => o !== r));
      moved[i] += Math.hypot(r.position.x - a.x, r.position.z - a.z);
    });
    for (let i = 0; i < rs.length; i++) for (let j = i + 1; j < rs.length; j++)
      closest = Math.min(closest, Math.hypot(rs[i].position.x - rs[j].position.x, rs[i].position.z - rs[j].position.z));
  }
  assert.ok(closest >= 0.6, `two came ${closest.toFixed(2)} m apart`);
  for (const m of moved) assert.ok(m > 30, `one walked only ${m.toFixed(1)} m in 10 minutes`);
});
