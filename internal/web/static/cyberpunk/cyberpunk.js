// The cyberpunk office (D146): the starship's view (D144) as an office
// tower. The lobby is the project, each worktree a floor; the elevator goes
// between them. State comes from /events?view=cyberpunk; actions are the
// dashboard's POSTs. Everything from the CLI is text: shown with
// textContent only.
import { World } from './world.js';
import { lobby, lab, svcState, tokens } from './rooms.js';
import { work, setWho, follow, unfollow, issuesFor, prsFor } from './work.js';
import { openTerminal } from '../term/term.js';
import { editBoard, picture } from './whiteboard.js';

const prefix = document.body.dataset.prefix;
const base = `/p/${encodeURIComponent(prefix)}`;
const $ = (id) => document.getElementById(id);

// el builds an element: el('p', {class: 'muted'}, 'text', child…).
function el(tag, attrs = {}, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === 'on') for (const [ev, f] of Object.entries(v)) e.addEventListener(ev, f);
    else if (v !== undefined && v !== null && v !== false) e.setAttribute(k, v === true ? '' : v);
  }
  for (const k of kids.flat()) if (k !== null && k !== undefined && k !== false) e.append(k instanceof Node ? k : String(k));
  return e;
}
const btn = (text, onclick, attrs = {}) => el('button', { type: 'button', class: 'hud-btn', on: { click: onclick }, ...attrs }, text);
const ext = (text, href) => el('a', { class: 'hud-btn', href, target: '_blank', rel: 'noopener noreferrer' }, text);
const safeURL = (u) => (/^https?:\/\//i.test(u) ? u : '#');

let state = { worktrees: [], jobs: [], banner: [] };
let room = null;
let here = null; // {kind: 'lobby'} | {kind: 'floor', slug}
let building = 0;
const world = new World($('scene'));

world.onTip = (t) => {
  const tip = $('tip');
  if (!t) {
    tip.hidden = true;
    return;
  }
  tip.hidden = false;
  tip.textContent = t.text;
  tip.style.left = `${t.x}px`;
  tip.style.top = `${t.y}px`;
};

// Speech bubbles over the crew.
let bubble = null;
function say(text, obj) {
  bubble?.remove();
  const b = el('div', { class: 'hud-tip' }, text);
  document.body.append(b);
  bubble = b;
  const v = obj.getWorldPosition(obj.position.clone()).setY(1.2).project(world.camera);
  b.style.left = `${((v.x + 1) / 2) * innerWidth - 40}px`;
  b.style.top = `${((1 - v.y) / 2) * innerHeight - 60}px`;
  setTimeout(() => b.remove(), 2500);
}

// Navigation.
async function go(to) {
  const build = ++building;
  closePanel();
  $('where').textContent = 'Elevator…';
  const ui = { transport, say, toFloor: (slug) => go({ kind: 'floor', slug }), prs: showPRs, issues: showIssues, issueList: () => getJSON(`${base}/virtual-office/issues`), prList: () => getJSON(`${base}/virtual-office/prs`), notes: showNotes, config: showConfig,
    prPage: showPRPage, service: showService, desk: showDesk, agentDesk: showAgentDesk, agent: showAgent, whiteboard: showBoard, viewer: () => world.viewer() };
  const n = state.worktrees.findIndex((w) => w.slug === to.slug) + 1;
  if (to.kind === 'floor' && !n) to = { kind: 'lobby' }; // a removed worktree's old link
  let r;
  try {
    r = to.kind === 'floor' ? await lab(to.slug, n, ui) : await lobby(prefix, ui);
  } catch (e) {
    $('where').textContent = `Out of order: ${e.message}`;
    return;
  }
  if (build !== building) return;
  room?.leave?.();
  world.set(r.scene, r.view, r.walk);
  room = r;
  here = to;
  r.update(state);
  world.setTicks(r.ticks); // with the racks the update added
  $('where').textContent = r.name;
  history.replaceState(null, '', to.kind === 'floor' ? `#floor/${encodeURIComponent(to.slug)}` : '#lobby');
  if (to.kind === 'floor') loadBoard(r, to.slug);
}

// The floor's whiteboard (D151): its drawing on the board, and the editor.
const boardURL = (slug) => `${base}/virtual-office/whiteboard/${encodeURIComponent(slug)}`;
async function loadBoard(r, slug) {
  try {
    const doc = await getJSON(boardURL(slug));
    if (room === r) r.setBoard?.(await picture(doc.png));
  } catch { /* the board keeps its diagrams */ }
}
async function showBoard(slug) {
  closePanel();
  let doc;
  try {
    doc = await getJSON(boardURL(slug));
  } catch (e) {
    return openPanel('Whiteboard', (body) => body.append(el('p', { class: 'muted' }, `The board did not open: ${e.message}`)));
  }
  const r = room;
  await editBoard(`Whiteboard · ${slug}`, doc, async (body) => {
    const q = `?if-hash=${encodeURIComponent(doc.hash)}`;
    if (body === null) {
      if (!doc.hash) return '';
      return JSON.parse(await post(boardURL(slug) + q + '&rm=1', '')).hash;
    }
    return JSON.parse(await post(boardURL(slug) + q, body, 'application/json')).hash;
  }, async (png) => {
    if (room === r) r.setBoard?.(await picture(png));
  });
}


function fromHash() {
  const m = location.hash.match(/^#floor\/(.+)$/);
  if (!m) return { kind: 'lobby' };
  try {
    return { kind: 'floor', slug: decodeURIComponent(m[1]) };
  } catch {
    return { kind: 'lobby' }; // a broken %-escape (a truncated link): the lobby, not a page stuck arriving
  }
}

function transport() {
  openPanel('Elevator', (body) => {
    body.append(el('p', { class: 'muted' }, 'Choose a floor.'));
    const list = el('ul', { class: 'items' });
    list.append(el('li', {}, btn('L · Lobby', () => go({ kind: 'lobby' })), ' ', el('span', { class: 'muted' }, 'project overview')));
    state.worktrees.forEach((w, i) => {
      list.append(el('li', {}, btn(`${i + 1} · ${w.slug}`, () => go({ kind: 'floor', slug: w.slug })), ' ',
        el('span', { class: `state-${w.stack}` }, w.stack), ' ', el('span', { class: 'muted' }, w.branch || '')));
    });
    body.append(list);
  });
}

// Panels.
let panelClose = null;
function openPanel(title, fill) {
  closePanel();
  $('panel-title').textContent = title;
  // a fresh body per opening: a reply that comes back after this panel
  // closed (a slow gh call, a job's error) writes into a body no longer on
  // the page, never into the panel opened since
  const body = el('div', { class: 'panel-page' });
  $('panel-body').replaceChildren(body);
  $('panel').hidden = false;
  panelClose = fill(body) || null;
}
function closePanel() {
  panelClose?.();
  panelClose = null;
  $('panel').hidden = true;
  $('panel').classList.remove('wide');
}

// termIn adds a live terminal (D149) to a panel body: url is
// /p/{prefix}/tty/{slug}/{kind}[?agent=]. The panel's cleanup must call the
// returned close.
const officeTheme = { background: '#05080d', foreground: '#c8f7ff', cursor: '#29e7ff', cursorAccent: '#05080d', selectionBackground: '#1d4f63' };
function termIn(body, url) {
  $('panel').classList.add('wide');
  const box = el('div', { class: 'term' });
  body.append(box);
  return openTerminal(box, url, { theme: officeTheme });
}
const ttyURL = (slug, kind) => `${base}/tty/${encodeURIComponent(slug)}/${kind}`;
$('panel-close').addEventListener('click', closePanel);
$('transport').addEventListener('click', transport);
function toggleView() {
  const m = world.setMode(world.mode === 'walk' ? 'orbit' : 'walk');
  $('view').textContent = m === 'walk' ? 'overview' : 'walk';
  $('help').hidden = m !== 'walk';
  document.body.classList.toggle('walking', m === 'walk');
}
$('view').addEventListener('click', toggleView);
addEventListener('keydown', (e) => {
  if (e.target.closest?.('input, textarea, select, .term-host') || document.querySelector('.board-modal')) return; // a focused terminal, or the open whiteboard, gets every key, Escape included
  if (e.key === 'Escape') closePanel();
  if (e.key === 't' || e.key === 'T') transport();
  if (e.key === 'v' || e.key === 'V') toggleView();
});

async function post(url, body, type = 'text/plain') {
  const res = await fetch(url, { method: 'POST', headers: { Accept: 'application/json', 'Content-Type': type }, body });
  const text = await res.text();
  if (!res.ok) throw new Error(text.trim() || res.statusText);
  return text;
}
async function getJSON(url) {
  const res = await fetch(url, { headers: { Accept: 'application/json' } });
  const text = await res.text();
  if (!res.ok) throw new Error(text.trim() || res.statusText);
  return JSON.parse(text);
}
function errorTo(body, e) {
  body.append(el('p', { class: 'err' }, e.message));
}
// job starts a dashboard action; its progress shows in the job list.
async function job(url, body) {
  try {
    await post(url);
  } catch (e) {
    errorTo(body, e);
  }
}

const labOf = (slug) => state.worktrees.find((w) => w.slug === slug);

function showService(slug, name) {
  openPanel(`${name} · ${slug}`, (body) => {
    const draw = () => {
      body.replaceChildren();
      const w = labOf(slug);
      const s = w?.services.find((x) => x.service === name);
      if (!s) {
        body.append(el('p', { class: 'muted' }, 'This service is gone.'));
        return;
      }
      const st = svcState(s.state);
      body.append(el('p', {}, 'State: ', el('strong', { class: `state-${st}` }, st), el('span', { class: 'muted' }, ` (${s.state})`), s.role ? el('span', { class: 'muted' }, ` · ${s.role}`) : ''));
      const row = el('div', { class: 'row' });
      if (s.restartable) row.append(btn('↻ Restart this service', () => job(`${base}/${encodeURIComponent(slug)}/restart/${encodeURIComponent(name)}`, body)));
      if (s.container) row.append(ext('Logs ↗', `http://logs.localhost/show?name=${encodeURIComponent(s.container)}`));
      body.append(row);
      body.append(el('h3', {}, 'Whole floor'));
      body.append(el('p', { class: 'muted' }, 'Services start and stop together, as `up` and `stop` run them.'));
      body.append(el('div', { class: 'row' },
        btn('▶ Start', () => job(`${base}/${encodeURIComponent(slug)}/act/up`, body), { disabled: w.stack === 'up' }),
        btn('↻ Restart', () => job(`${base}/${encodeURIComponent(slug)}/act/restart`, body), { disabled: w.stack === 'stopped' }),
        btn('■ Stop', () => job(`${base}/${encodeURIComponent(slug)}/act/stop`, body), { disabled: w.stack === 'stopped', class: 'hud-btn danger' })));
    };
    draw();
    redraw = draw;
    return () => { redraw = null; };
  });
}

function showDesk(slug) {
  openPanel(`Your desk · ${slug}`, (body) => {
    const w = labOf(slug);
    body.append(el('p', {}, '⎇ ', el('strong', {}, w?.branch || slug), ' · ', el('span', { class: `state-${w?.stack}` }, w?.stack || '')));
    let t = null;
    const open = (kind) => { t?.close(); body.querySelector('.term')?.remove(); t = termIn(body, ttyURL(slug, kind)); };
    const hasNvim = w?.services.some((s) => s.service === 'nvim');
    body.append(el('div', { class: 'row' },
      btn('▣ Shell', () => open('shell'), { title: 'A shell in the app container, here; it ends when this panel closes' }),
      hasNvim ? btn('✎ Neovim', () => open('nvim'), { title: 'Neovim here; it keeps running when this panel closes' }) : null,
      btn('Terminal window', () => job(`${base}/${encodeURIComponent(slug)}/act/term`, body)),
      hasNvim ? btn('Neovim window', () => job(`${base}/${encodeURIComponent(slug)}/act/nvim`, body)) : null));
    body.append(el('p', { class: 'muted' }, 'The windows open on this machine.'));
    if (w?.links.length) {
      body.append(el('h3', {}, 'Links'));
      body.append(el('div', { class: 'row' }, w.links.map((l) => ext(`${l.label} ↗`, safeURL(l.url)))));
    }
    if (w?.pr) {
      body.append(el('h3', {}, 'Pull request'));
      body.append(el('p', {}, ext(`#${w.pr.number} ↗`, safeURL(w.pr.url)), ' ', w.pr.title));
      body.append(el('a', { class: 'hud-btn', href: `${base}/${encodeURIComponent(slug)}/pr` }, 'PR page: checks, reviews, merge'));
    }
    return () => t?.close();
  });
}

function showAgentDesk(slug) {
  openPanel(`Agent console · ${slug}`, (body) => {
    body.append(el('p', {}, 'Opens the agent in a terminal window (`railswitch agent --new-term`). A service agent keeps running when you close it.'));
    body.append(btn('▣ Open agent console', () => job(`${base}/${encodeURIComponent(slug)}/act/agent`, body)));
  });
}

// serviceAgents are a floor's agents that run as services (D142).
const serviceAgents = (w) => w.services.filter((s) => s.service.startsWith('agent-') && s.state !== 'on-demand').map((s) => s.service.slice(6));

function showAgent(slug) {
  openPanel(`Agent partner · ${slug}`, (body) => {
    const w = labOf(slug);
    const a = w?.agent;
    const status = el('p', {});
    const drawStatus = () => {
      const x = labOf(slug)?.agent;
      status.replaceChildren(x ? el('span', {}, el('strong', {}, x.name), ' · ', el('span', { class: `state-${x.state}` }, x.state), x.usage ? ` · ${tokens(x.usage)} tokens (${x.usage.input.toLocaleString()} in, ${x.usage.output.toLocaleString()} out, ${x.usage.cache_read.toLocaleString()} cache read, ${x.usage.cache_write.toLocaleString()} cache write)` : '', x.message ? ` · ${x.message}` : '') : el('span', { class: 'muted' }, 'No agent is running here.'));
    };
    drawStatus();
    body.append(status);
    const names = w ? serviceAgents(w) : [];
    let name = a && (names.includes(a.name) || !names.length) ? a.name : names[0];
    let t = null;
    const open = () => {
      t?.close();
      body.querySelector('.term')?.remove();
      t = termIn(body, ttyURL(slug, 'agent') + (name ? `?agent=${encodeURIComponent(name)}` : ''));
    };
    if (names.length > 1) {
      const sel = el('select', { on: { change: (e) => { name = e.target.value; open(); } } }, names.map((n) => el('option', { value: n, selected: n === name }, n)));
      body.append(el('div', { class: 'row' }, 'Agent: ', sel));
    }
    if (!names.length) body.append(el('p', { class: 'muted' }, 'This floor has no service agent: the agent runs for this panel and stops when it closes.'));
    body.append(el('div', { class: 'row' }, btn('Open console window ▣', () => job(`${base}/${encodeURIComponent(slug)}/act/agent`, body))));
    // Connecting runs `railswitch agent`, which starts the floor first: only
    // a running service agent is joined without asking.
    const live = name && w?.services.some((x) => x.service === `agent-${name}` && x.state.startsWith('active'));
    if (live) open();
    else {
      const go = btn('▣ Connect', () => { go.remove(); note.remove(); open(); });
      const note = el('p', { class: 'muted' }, 'Connecting starts the floor first if it is stopped.');
      body.append(el('div', { class: 'row' }, go), note);
    }
    redraw = drawStatus;
    return () => { t?.close(); redraw = null; };
  });
}

// whoBar is the work filter (Mine, Following, All) and, under Following,
// the people followed: changing it redraws the panel (draw) and the lobby's
// boards.
function whoBar(me, draw) {
  const pick = (who, text) => btn(text, () => { setWho(who); room?.redrawWork?.(); draw(); },
    { 'aria-pressed': String(work.who === who), class: work.who === who ? 'hud-btn on' : 'hud-btn' });
  const bar = el('div', { class: 'row' }, pick('mine', me ? `Mine (@${me})` : 'Mine'), pick('following', `Following (${work.follow.length})`), pick('all', 'All'));
  if (work.who !== 'following') return [bar];
  const input = el('input', { type: 'text', placeholder: 'GitHub login to follow', maxlength: 39, 'aria-label': 'GitHub login to follow' });
  const err = el('span', { class: 'err' });
  const add = () => {
    if (follow(input.value)) { room?.redrawWork?.(); draw(); } else err.textContent = 'Not a GitHub login.';
  };
  input.addEventListener('keydown', (e) => { if (e.key === 'Enter') add(); });
  const people = el('div', { class: 'row' }, work.follow.length ? work.follow.map((l) =>
    el('span', { class: 'tag' }, `@${l} `, el('button', { type: 'button', class: 'link', 'aria-label': `Unfollow ${l}`, on: { click: () => { unfollow(l); room?.redrawWork?.(); draw(); } } }, '✕')))
    : el('span', { class: 'muted' }, 'Follow teammates to see their issues and PRs.'));
  return [bar, el('div', { class: 'row' }, input, btn('Follow', add), err), people];
}

// showPRPage opens the floor's PR page (D152) in a new tab, or the PR
// list when the floor has no PR.
function showPRPage(slug) {
  const w = state.worktrees.find((x) => x.slug === slug);
  if (!w?.pr) return showPRs();
  window.open(`${base}/${encodeURIComponent(slug)}/pr`, '_blank', 'noopener');
}

function showPRs() {
  openPanel('Pull requests', (body) => {
    let doc = null;
    const draw = () => {
      body.replaceChildren();
      if (doc) {
        body.append(...whoBar(doc.me, draw));
        const mine = prsFor(doc);
        const list = el('ul', { class: 'items' });
        for (const p of mine) {
          const floor = state.worktrees.find((w) => w.branch === p.branch);
          const asked = p.reviewers.some((r) => r.toLowerCase() === doc.me.toLowerCase());
          list.append(el('li', {},
            el('div', {}, ext(`#${p.number} ↗`, safeURL(p.url)), ' ', p.draft ? el('span', { class: 'tag' }, 'draft') : '', p.title),
            el('div', { class: 'muted' }, `by ${p.author}`, p.assignees.length ? ` → ${p.assignees.join(', ')}` : '',
              p.reviewers.length ? ` · review asked of ${p.reviewers.join(', ')}` : '', ` · ${p.review.replace('_', ' ')}`),
            el('div', { class: 'row' }, asked ? el('strong', {}, 'Your review is asked') : '', floor ? btn('Go to the floor', () => go({ kind: 'floor', slug: floor.slug })) : '')));
        }
        if (!mine.length) list.append(el('li', { class: 'muted' }, 'No open pull request here.'));
        body.append(list);
      } else body.append(el('p', { class: 'muted' }, 'Reading the open pull requests…'));
      const open = state.worktrees.filter((w) => w.pr);
      body.append(el('h3', {}, 'This project’s floors'));
      if (state.pr_error) body.append(el('p', { class: 'err' }, state.pr_error));
      const list = el('ul', { class: 'items' });
      for (const w of open) {
        const p = w.pr;
        list.append(el('li', {},
          el('div', {}, ext(`#${p.number} ↗`, safeURL(p.url)), ' ', p.title),
          el('div', { class: 'muted' }, `${w.slug} · ${p.state.toLowerCase()}`, p.checks ? ` · checks ${p.checks}` : '', p.review ? ` · review ${p.review}` : ''),
          el('div', { class: 'row' }, btn('Go to the floor', () => go({ kind: 'floor', slug: w.slug })), p.review_app ? ext('Review app ↗', safeURL(p.review_app)) : '')));
      }
      if (!open.length) list.append(el('li', { class: 'muted' }, 'No worktree has an open PR.'));
      body.append(list, btn('↻ Refresh PRs', () => job(`${base}/prs/refresh`, body)));
    };
    draw();
    getJSON(`${base}/virtual-office/prs`).then((d) => { doc = d; draw(); }).catch((e) => errorTo(body, e));
    redraw = draw;
    return () => { redraw = null; };
  });
}

function showIssues() {
  openPanel('GitHub issues', (body) => {
    body.append(el('p', { class: 'muted' }, 'Scanning…'));
    let doc = null;
    const draw = () => {
      body.replaceChildren(...whoBar(doc.me, draw));
      const shown = issuesFor(doc);
      const list = el('ul', { class: 'items' });
      for (const i of shown) {
        const floor = state.worktrees.find((w) => w.branch.startsWith(`issue-${i.number}-`) || w.branch === `issue-${i.number}`);
        list.append(el('li', {},
          el('div', {}, ext(`#${i.number} ↗`, safeURL(i.url)), ' ', i.title),
          el('div', { class: 'muted' }, i.labels.map((l) => el('span', { class: 'tag' }, l)), i.assignees.length ? ` → ${i.assignees.join(', ')}` : ' unassigned', i.author ? ` · by ${i.author}` : ''),
          el('div', { class: 'row' }, floor
            ? btn(`Go to its floor (${floor.slug})`, () => go({ kind: 'floor', slug: floor.slug }))
            : btn('▶ Start working', () => job(`${base}/virtual-office/issues/${i.number}/start`, body),
              { title: 'A worktree on a branch named after the issue, its stack up, and the agent working on the issue' }))));
      }
      if (!shown.length) list.append(el('li', { class: 'muted' }, doc.issues.length ? 'Nothing open here: try Following or All.' : 'No open issues.'));
      body.append(list);
    };
    getJSON(`${base}/virtual-office/issues`).then((d) => { doc = d; draw(); }).catch((e) => { body.replaceChildren(); errorTo(body, e); });
  });
}

function showNotes() {
  openPanel('Shared notes', (body) => {
    const list = async () => {
      body.replaceChildren(el('p', { class: 'muted' }, "The agents' shared notes (`notes/` in the workspace). Every agent reads them; keep secrets out."));
      try {
        const doc = await getJSON(`${base}/virtual-office/notes`);
        const ul = el('ul', { class: 'items' });
        for (const n of doc.notes) ul.append(el('li', {}, el('button', { type: 'button', class: 'link', on: { click: () => edit(n.name) } }, n.name), ' ', el('span', { class: 'muted' }, `${n.size} B`)));
        if (!doc.notes.length) ul.append(el('li', { class: 'muted' }, 'No notes yet.'));
        body.append(ul);
      } catch (e) {
        errorTo(body, e);
      }
      const name = el('input', { type: 'text', placeholder: 'new_note_name.md', pattern: '[A-Za-z0-9][A-Za-z0-9._-]*\\.md' });
      body.append(el('h3', {}, 'New note'), name, btn('+ Create', () => {
        const n = name.value.trim();
        if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,99}\.md$/.test(n)) {
          errorTo(body, new Error('A name is letters, digits, . _ - and ends in .md'));
          return;
        }
        edit(n, true);
      }));
    };
    const edit = async (n, fresh) => {
      body.replaceChildren(btn('◂ All notes', list), el('h3', {}, n));
      let doc = { text: '', hash: '' };
      if (!fresh) {
        try {
          doc = await getJSON(`${base}/virtual-office/notes/${encodeURIComponent(n)}`);
        } catch (e) {
          errorTo(body, e);
          return;
        }
      }
      const ta = el('textarea', { spellcheck: 'false' });
      ta.value = doc.text;
      const msg = el('p', { class: 'muted' });
      const save = async () => {
        try {
          const out = await post(`${base}/virtual-office/notes/${encodeURIComponent(n)}?if-hash=${encodeURIComponent(doc.hash)}`, ta.value);
          // the saved note's hash, from the write itself: a second read could
          // pick up another writer's change and let the next save erase it
          doc.hash = JSON.parse(out).hash;
          msg.className = 'muted';
          msg.textContent = 'Saved.';
        } catch (e) {
          msg.className = 'err';
          msg.textContent = e.message;
        }
      };
      const rm = async () => {
        if (!doc.hash) return list();
        try {
          await post(`${base}/virtual-office/notes/${encodeURIComponent(n)}?rm=1&if-hash=${encodeURIComponent(doc.hash)}`, '');
          list();
        } catch (e) {
          msg.className = 'err';
          msg.textContent = e.message;
        }
      };
      body.append(ta, el('div', { class: 'row' }, btn('Save', save), btn('Delete', rm, { class: 'hud-btn danger' })), msg);
      ta.focus();
    };
    list();
  });
}

function showConfig() {
  openPanel('Engineering', (body) => {
    body.append(el('p', { class: 'muted' }, 'The project’s configuration opens in the dashboard.'));
    body.append(el('div', { class: 'row' },
      ext('Settings ↗', `${base}/settings`), ext('Images ↗', `${base}/images`), ext('Library ↗', `${base}/library`), ext('Machine ↗', '/machine')));
    if (state.banner.length) {
      body.append(el('h3', {}, 'Alerts'));
      for (const b of state.banner) body.append(el('p', {}, b));
    }
  });
}

// Live state.
let redraw = null;
function apply(next) {
  const first = !state.prefix;
  state = next;
  if (first) {
    go(fromHash());
  } else {
    room?.update(state);
    if (room && here.kind === 'floor') world.setTicks(room.ticks);
    if (here?.kind === 'floor' && !labOf(here.slug)) go({ kind: 'lobby' });
  }
  redraw?.();
  const banner = $('banner');
  const lines = [state.error, ...state.banner].filter(Boolean);
  banner.hidden = !lines.length;
  banner.textContent = lines.join(' · ');
  const jobs = $('jobs');
  jobs.replaceChildren(...state.jobs.slice(0, 4).map((j) => el('li', { class: j.state }, `${j.slug || prefix}: ${j.label} · ${j.state}`)));
}

const es = new EventSource(`/events?p=${encodeURIComponent(prefix)}&view=cyberpunk`);
es.onmessage = (m) => {
  const ev = JSON.parse(m.data);
  if (ev.type === 'cyberpunk') apply(JSON.parse(ev.html));
};
addEventListener('hashchange', () => go(fromHash()));
