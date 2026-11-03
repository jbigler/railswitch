// Minesweeper for the game room's first arcade (D154). The board logic is
// pure (newBoard, reveal, flag) and checked by check(); play() draws it.
export const SIZES = [
  { name: 'small', w: 9, h: 9, mines: 10 },
  { name: 'medium', w: 16, h: 16, mines: 40 },
  { name: 'large', w: 30, h: 16, mines: 99 },
];

export function newBoard(w, h, mines) {
  return { w, h, mines, laid: false, over: null, left: w * h - mines,
    cells: Array.from({ length: w * h }, () => ({ mine: false, n: 0, open: false, flag: false })) };
}

export function neighbours(b, i) {
  const x = i % b.w, y = (i - x) / b.w, out = [];
  for (let dy = -1; dy <= 1; dy++) {
    for (let dx = -1; dx <= 1; dx++) {
      const nx = x + dx, ny = y + dy;
      if ((dx || dy) && nx >= 0 && ny >= 0 && nx < b.w && ny < b.h) out.push(ny * b.w + nx);
    }
  }
  return out;
}

// lay places the mines away from the first click and its neighbours (when
// the board has room), so the first click always opens ground.
export function lay(b, safe, rand = Math.random) {
  const keep = new Set([safe, ...neighbours(b, safe)]);
  let spots = b.cells.map((_, i) => i).filter((i) => !keep.has(i));
  if (spots.length < b.mines) spots = b.cells.map((_, i) => i).filter((i) => i !== safe);
  for (let k = 0; k < b.mines; k++) {
    const j = k + Math.floor(rand() * (spots.length - k));
    [spots[k], spots[j]] = [spots[j], spots[k]];
    b.cells[spots[k]].mine = true;
  }
  b.cells.forEach((c, i) => { c.n = neighbours(b, i).filter((j) => b.cells[j].mine).length; });
  b.laid = true;
}

// reveal opens cell i; a zero opens its whole empty region and its rim.
export function reveal(b, i, rand) {
  if (b.over || b.cells[i].flag || b.cells[i].open) return;
  if (!b.laid) lay(b, i, rand);
  if (b.cells[i].mine) {
    b.cells[i].open = true;
    b.over = 'lost';
    return;
  }
  const todo = [i];
  while (todo.length) {
    const j = todo.pop(), c = b.cells[j];
    if (c.open || c.flag) continue;
    c.open = true;
    b.left--;
    if (c.n === 0) todo.push(...neighbours(b, j).filter((k) => !b.cells[k].open));
  }
  if (b.left === 0) b.over = 'won';
}

export function flag(b, i) {
  if (!b.over && !b.cells[i].open) b.cells[i].flag = !b.cells[i].flag;
}

// check asserts the logic on a fixed board; true when it holds.
export function check() {
  const b = newBoard(4, 3, 1);
  b.cells[3].mine = true; // top-right corner
  b.laid = true;
  b.cells.forEach((c, i) => { c.n = neighbours(b, i).filter((j) => b.cells[j].mine).length; });
  const ok = [
    neighbours(b, 0).length === 3 && neighbours(b, 5).length === 8,
    b.cells[2].n === 1 && b.cells[6].n === 1 && b.cells[7].n === 1 && b.cells[0].n === 0,
  ];
  reveal(b, 8); // a zero: floods everything but the mine
  ok.push(b.over === 'won' && b.cells.filter((c) => c.open).length === 11 && !b.cells[3].open);
  const f = newBoard(9, 9, 10);
  reveal(f, 40, () => 0.5);
  ok.push(f.over === null && f.cells[40].n === 0 && f.cells.filter((c) => c.mine).length === 10);
  return ok.every(Boolean);
}

const INK = ['', '#22e6ff', '#63e07c', '#ff2bd6', '#9b6bff', '#ffc857', '#5dfff0', '#ff8a3d', '#e6f2ff'];
const CELL = 32;

// play runs the game on canvas; hud(text) shows its status line.
export function play(canvas, hud) {
  let size = 0, b, t0, timer;
  const g = canvas.getContext('2d');
  const draw = () => {
    g.fillStyle = '#04040a';
    g.fillRect(0, 0, canvas.width, canvas.height);
    g.font = `bold ${CELL * 0.6}px ui-monospace, monospace`;
    g.textAlign = 'center';
    g.textBaseline = 'middle';
    b.cells.forEach((c, i) => {
      const x = (i % b.w) * CELL, y = Math.floor(i / b.w) * CELL;
      const shown = c.open || (b.over && c.mine);
      g.fillStyle = shown ? (c.mine ? '#3a0d1a' : '#0d1024') : '#1b1440';
      g.fillRect(x + 1, y + 1, CELL - 2, CELL - 2);
      if (!shown) {
        g.strokeStyle = '#3b2f6b';
        g.strokeRect(x + 1.5, y + 1.5, CELL - 3, CELL - 3);
      }
      const mark = shown && c.mine ? '✹' : c.flag ? '⚑' : shown && c.n ? String(c.n) : '';
      if (mark) {
        g.fillStyle = c.flag && !shown ? '#ff2bd6' : c.mine ? '#ff6b6b' : INK[c.n];
        g.shadowColor = g.fillStyle;
        g.shadowBlur = 6;
        g.fillText(mark, x + CELL / 2, y + CELL / 2 + 1);
        g.shadowBlur = 0;
      }
    });
  };
  const status = () => {
    const flags = b.cells.filter((c) => c.flag).length;
    const secs = t0 ? Math.floor(((b.end || Date.now()) - t0) / 1000) : 0;
    const end = b.over === 'won' ? ' · CLEARED' : b.over === 'lost' ? ' · BOOM' : '';
    hud(`${SIZES[size].name} · mines ${b.mines - flags} · ${secs}s${end}`);
  };
  const restart = () => {
    const s = SIZES[size];
    b = newBoard(s.w, s.h, s.mines);
    canvas.width = s.w * CELL;
    canvas.height = s.h * CELL;
    t0 = 0;
    draw();
    status();
  };
  const cellAt = (e) => {
    const r = canvas.getBoundingClientRect();
    const x = Math.floor(((e.clientX - r.left) / r.width) * b.w), y = Math.floor(((e.clientY - r.top) / r.height) * b.h);
    return x >= 0 && y >= 0 && x < b.w && y < b.h ? y * b.w + x : -1;
  };
  canvas.addEventListener('contextmenu', (e) => e.preventDefault());
  canvas.addEventListener('pointerdown', (e) => {
    const i = cellAt(e);
    if (i < 0 || b.over) return;
    if (e.button === 2) flag(b, i);
    else if (e.button === 0) {
      t0 ||= Date.now();
      reveal(b, i);
      if (b.over) b.end = Date.now();
    }
    draw();
    status();
  });
  timer = setInterval(() => { if (t0 && !b.over) status(); }, 500);
  restart();
  return {
    restart,
    help: 'click: open · right-click: flag · R: new game',
    extra: { label: 'Size', act: () => { size = (size + 1) % SIZES.length; restart(); } },
    key: (e) => (e.key === 'r' || e.key === 'R') && (restart(), true),
    close: () => clearInterval(timer),
  };
}
