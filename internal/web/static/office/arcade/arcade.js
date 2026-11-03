// The game room's arcades (D154): a cabinet opens its game over the office,
// like the whiteboard; the overlay carries .board-modal, so the office's
// own keys (T, V, walking) stand down while it is open.
import * as minesweeper from './minesweeper.js';
import * as blocks from './blocks.js';

const GAMES = [
  { title: 'Minesweeper', mod: minesweeper },
  { title: 'Stackfall', mod: blocks },
];
let checked = false;

export const arcadeTitle = (n) => GAMES[n % GAMES.length].title;

// openArcade opens cabinet n's game; Esc or Close ends it.
export function openArcade(n) {
  const game = GAMES[n % GAMES.length];
  if (!checked) {
    checked = true;
    for (const x of GAMES) if (!x.mod.check()) console.error(`arcade: ${x.title}'s self-check failed`);
  }
  document.exitPointerLock?.();
  const mk = (text, act, cls) => {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'hud-btn' + (cls ? ' ' + cls : '');
    b.textContent = text;
    b.addEventListener('click', () => { b.blur(); act(); }); // a focused button would take Space
    return b;
  };
  const name = document.createElement('span');
  name.textContent = game.title;
  const status = document.createElement('span');
  status.className = 'board-status';
  const canvas = document.createElement('canvas');
  const help = document.createElement('p');
  help.className = 'arcade-help';
  const screen = document.createElement('div');
  screen.className = 'arcade-screen';
  screen.append(canvas, help);
  const head = document.createElement('header');
  const modal = document.createElement('div');
  modal.className = 'board-modal arcade';
  modal.append(head, screen);
  document.body.append(modal);

  const g = game.mod.play(canvas, (text) => { status.textContent = text; });
  help.textContent = `${g.help} · Esc: close`;
  const onKey = (e) => {
    if (e.key === 'Escape') close();
    else if (!g.key(e)) return;
    e.preventDefault();
    e.stopImmediatePropagation();
  };
  const close = () => {
    g.close();
    removeEventListener('keydown', onKey, true);
    modal.remove();
  };
  head.append(name, status, mk(g.extra.label, g.extra.act), mk('New game', g.restart), mk('Close', close, 'primary'));
  addEventListener('keydown', onKey, true);
}
