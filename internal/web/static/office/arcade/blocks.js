// Stackfall, the game room's second arcade (D154): falling blocks. The
// well's logic is pure (fits, rotate, land, clearLines) and checked by
// check(); play() runs and draws it.
export const W = 10, H = 20;

// The seven four-block shapes, each with its own neon.
const SHAPES = [
  [[1, 1, 1, 1]],
  [[1, 1], [1, 1]],
  [[0, 1, 0], [1, 1, 1]],
  [[0, 1, 1], [1, 1, 0]],
  [[1, 1, 0], [0, 1, 1]],
  [[1, 0, 0], [1, 1, 1]],
  [[0, 0, 1], [1, 1, 1]],
];
const NEON = ['#ff2bd6', '#22e6ff', '#ffc857', '#9b6bff', '#63e07c', '#ff8a3d', '#5dfff0'];

export const emptyWell = () => Array.from({ length: H }, () => Array(W).fill(0));

// rotate turns a shape a quarter clockwise (dir 1) or anticlockwise (-1).
export function rotate(m, dir = 1) {
  const t = m[0].map((_, i) => m.map((r) => r[i]));
  return dir > 0 ? t.map((r) => r.reverse()) : t.reverse();
}

// fits: the shape at (x, y) stays in the well and off the stack. Above the
// top is open.
export function fits(well, m, x, y) {
  return m.every((r, dy) => r.every((v, dx) => !v || (x + dx >= 0 && x + dx < W && y + dy < H && (y + dy < 0 || !well[y + dy][x + dx]))));
}

// land writes the piece into the well; false when some of it is above the top.
export function land(well, p) {
  let inside = true;
  p.m.forEach((r, dy) => r.forEach((v, dx) => {
    if (!v) return;
    if (p.y + dy < 0) inside = false;
    else well[p.y + dy][p.x + dx] = p.c;
  }));
  return inside;
}

// clearLines removes the full rows and returns how many.
export function clearLines(well) {
  const kept = well.filter((r) => r.some((v) => !v));
  const n = H - kept.length;
  well.splice(0, H, ...Array.from({ length: n }, () => Array(W).fill(0)), ...kept);
  return n;
}

export const POINTS = [0, 100, 300, 500, 800];
export const levelOf = (lines) => 1 + Math.floor(lines / 10);
export const stepMs = (level) => Math.max(70, 800 - (level - 1) * 70);

// check asserts the logic on fixed wells; true when it holds.
export function check() {
  const w = emptyWell();
  w[H - 1].fill(1);
  w[H - 2].fill(1);
  w[H - 2][4] = 0;
  w[H - 3][0] = 2;
  const ok = [clearLines(w) === 1, w[H - 1][4] === 0 && w[H - 2][0] === 2 && w[0].every((v) => !v)];
  ok.push(fits(w, SHAPES[0], 0, -1) && !fits(w, SHAPES[0], 7, 0) && !fits(w, [[1]], 0, H - 2));
  const t = SHAPES[2];
  ok.push(JSON.stringify(rotate(rotate(rotate(rotate(t))))) === JSON.stringify(t) && JSON.stringify(rotate(t, -1)) === JSON.stringify(rotate(rotate(rotate(t)))));
  return ok.every(Boolean);
}

const CELL = 24, SIDE = 120;
const BEST = 'railswitch.office.stackfall.best';

function best(v) {
  try {
    if (v !== undefined) localStorage.setItem(BEST, String(v));
    return Number(localStorage.getItem(BEST)) || 0;
  } catch { return v || 0; } // private window: this visit only
}

// play runs the game on canvas; hud(text) shows its status line.
export function play(canvas, hud) {
  canvas.width = W * CELL + SIDE;
  canvas.height = H * CELL;
  const g = canvas.getContext('2d');
  let well, piece, next, bag, score, lines, over, paused, acc, last, raf, top = best();
  const take = () => {
    if (!bag.length) bag = [0, 1, 2, 3, 4, 5, 6].sort(() => Math.random() - 0.5); // shortcut: a sort shuffle is slightly biased; fine for a game
    return bag.pop();
  };
  const spawn = () => {
    const k = next;
    next = take();
    piece = { m: SHAPES[k], c: k + 1, x: Math.floor((W - SHAPES[k][0].length) / 2), y: -1 };
    if (!fits(well, piece.m, piece.x, piece.y)) end();
  };
  const end = () => {
    over = true;
    if (score > top) top = best(score);
  };
  const lock = () => {
    if (!land(well, piece)) return end();
    const n = clearLines(well);
    lines += n;
    score += POINTS[n] * levelOf(lines - n);
    spawn();
  };
  const move = (dx, dy) => {
    if (!fits(well, piece.m, piece.x + dx, piece.y + dy)) return false;
    piece.x += dx;
    piece.y += dy;
    return true;
  };
  const turn = (dir) => {
    const m = rotate(piece.m, dir);
    for (const kick of [0, -1, 1, -2, 2]) {
      if (fits(well, m, piece.x + kick, piece.y)) {
        piece.m = m;
        piece.x += kick;
        return;
      }
    }
  };
  const block = (x, y, c, alpha = 1) => {
    g.globalAlpha = alpha;
    g.fillStyle = NEON[c - 1];
    g.shadowColor = NEON[c - 1];
    g.shadowBlur = 8;
    g.fillRect(x * CELL + 2, y * CELL + 2, CELL - 4, CELL - 4);
    g.shadowBlur = 0;
    g.globalAlpha = 1;
  };
  const draw = () => {
    g.fillStyle = '#04040a';
    g.fillRect(0, 0, canvas.width, canvas.height);
    g.strokeStyle = '#1b1440';
    for (let x = 0; x <= W; x++) { g.beginPath(); g.moveTo(x * CELL, 0); g.lineTo(x * CELL, H * CELL); g.stroke(); }
    for (let y = 0; y <= H; y++) { g.beginPath(); g.moveTo(0, y * CELL); g.lineTo(W * CELL, y * CELL); g.stroke(); }
    well.forEach((r, y) => r.forEach((v, x) => v && block(x, y, v)));
    if (!over) {
      let gy = piece.y;
      while (fits(well, piece.m, piece.x, gy + 1)) gy++;
      piece.m.forEach((r, dy) => r.forEach((v, dx) => {
        if (v && gy + dy >= 0) block(piece.x + dx, gy + dy, piece.c, 0.18);
        if (v && piece.y + dy >= 0) block(piece.x + dx, piece.y + dy, piece.c);
      }));
    }
    g.fillStyle = '#8b8fb8';
    g.font = '12px ui-monospace, monospace';
    g.textAlign = 'left';
    g.fillText('NEXT', W * CELL + 16, 22);
    SHAPES[next].forEach((r, dy) => r.forEach((v, dx) => v && block(W + 0.7 + dx, 1.4 + dy, next + 1)));
    if (over || paused) {
      g.fillStyle = 'rgba(4, 4, 10, .7)';
      g.fillRect(0, 0, W * CELL, H * CELL);
      g.fillStyle = over ? '#ff2bd6' : '#22e6ff';
      g.font = 'bold 22px ui-monospace, monospace';
      g.textAlign = 'center';
      g.fillText(over ? 'GAME OVER' : 'PAUSED', (W * CELL) / 2, (H * CELL) / 2);
    }
    hud(`score ${score} · lines ${lines} · level ${levelOf(lines)} · best ${top}`);
  };
  const frame = (t) => {
    raf = requestAnimationFrame(frame);
    acc += Math.min(t - (last ?? t), 100); // a hidden tab pauses frames; back, don't fall the whole gap at once
    last = t;
    if (over || paused) acc = 0;
    while (acc >= stepMs(levelOf(lines))) {
      acc -= stepMs(levelOf(lines));
      if (!move(0, 1)) lock();
      if (over) break;
    }
    draw();
  };
  const restart = () => {
    well = emptyWell();
    bag = [];
    next = take();
    score = lines = acc = 0;
    over = paused = false;
    spawn();
  };
  const key = (e) => {
    const k = e.key;
    if (k === 'r' || k === 'R') return restart(), true;
    if (k === 'p' || k === 'P') return (paused = !over && !paused), true;
    if (over || paused) return false;
    if (k === 'ArrowLeft') move(-1, 0);
    else if (k === 'ArrowRight') move(1, 0);
    else if (k === 'ArrowUp' || k === 'x' || k === 'X') turn(1);
    else if (k === 'z' || k === 'Z') turn(-1);
    else if (k === 'ArrowDown') { if (move(0, 1)) score += 1; acc = 0; }
    else if (k === ' ') { while (move(0, 1)) score += 2; lock(); acc = 0; }
    else return false;
    return true;
  };
  restart();
  raf = requestAnimationFrame(frame);
  return {
    restart,
    help: '←→ move · ↑/X turn · Z turn back · ↓ soft drop · Space hard drop · P pause · R new game',
    extra: { label: 'Pause', act: () => key({ key: 'p' }) },
    key,
    close: () => cancelAnimationFrame(raf),
  };
}
