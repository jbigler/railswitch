// The whiteboard's editor page (D218): the worktree's board (D151) in
// Excalidraw, in the dashboard's light or dark theme. Save and close, and
// Discard changes, go back to the worktree page, which shows its Whiteboard
// tab again; leaving another way with changes asks first.
import { load, boardBody } from './office/whiteboard.js';

const page = document.body.dataset;
const status = document.getElementById('board-status');
const save = document.getElementById('board-save');
const discard = document.getElementById('board-discard');
let ed = null;
let leaving = false; // Save or Discard: no question on the way out

const back = () => {
  leaving = true;
  location.href = page.back;
};

addEventListener('beforeunload', (e) => {
  if (!leaving && ed?.dirty()) e.preventDefault();
});

discard.addEventListener('click', back);

async function post(url, body) {
  const r = await fetch(url, { method: 'POST', body: body ?? '', credentials: 'same-origin', headers: body ? { 'Content-Type': 'application/json' } : {} });
  if (!r.ok) throw new Error((await r.text()).trim() || r.statusText);
}

async function start() {
  let doc;
  try {
    const r = await fetch(page.board, { credentials: 'same-origin' });
    if (!r.ok) throw new Error((await r.text()).trim() || r.statusText);
    doc = await r.json();
    ed = (await load()).open(document.getElementById('board-host'), doc.scene, { dark: matchMedia('(prefers-color-scheme: dark)').matches });
  } catch (e) {
    status.textContent = `The board did not open: ${e.message}`;
    return;
  }
  status.textContent = '';
  save.disabled = false;
  save.addEventListener('click', async () => {
    save.disabled = discard.disabled = true;
    status.textContent = 'saving…';
    try {
      const { body } = await boardBody(ed);
      const q = `?if-hash=${encodeURIComponent(doc.hash)}`;
      // An empty board clears it, as in the office; there was nothing to clear without a hash.
      if (body !== null) await post(page.board + q, body);
      else if (doc.hash) await post(page.board + q + '&rm=1', null);
      back();
    } catch (e) {
      status.textContent = `Not saved: ${e.message}`;
      save.disabled = discard.disabled = false;
    }
  });
}

start();
