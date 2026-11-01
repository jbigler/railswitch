// The starship view (D144): the bridge is the project, each worktree a
// lab. State comes from /events?view=ship; actions are the dashboard's
// POSTs. Everything from the CLI is text: shown with textContent only.
import { World } from './world.js';
import { bridge, lab, svcState } from './rooms.js';

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
let here = null; // {kind: 'bridge'} | {kind: 'lab', slug}
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
  const n = ++building;
  closePanel();
  $('where').textContent = 'Energising…';
  const ui = { transport, say, toLab: (slug) => go({ kind: 'lab', slug }), prs: showPRs, issues: showIssues, notes: showNotes, config: showConfig,
    service: showService, desk: showDesk, agentDesk: showAgentDesk, agent: showAgent };
  const r = to.kind === 'lab' ? await lab(to.slug, ui) : await bridge(prefix, ui);
  if (n !== building) return;
  world.set(r.scene, r.view, r.walk);
  for (const t of r.ticks) world.tick(t);
  room = r;
  here = to;
  r.update(state);
  for (const t of r.ticks) world.tick(t); // racks added by update
  $('where').textContent = r.name;
  history.replaceState(null, '', to.kind === 'lab' ? `#lab/${encodeURIComponent(to.slug)}` : '#bridge');
}

function fromHash() {
  const m = location.hash.match(/^#lab\/(.+)$/);
  return m ? { kind: 'lab', slug: decodeURIComponent(m[1]) } : { kind: 'bridge' };
}

function transport() {
  openPanel('Transporter', (body) => {
    body.append(el('p', { class: 'muted' }, 'Choose a destination.'));
    const list = el('ul', { class: 'items' });
    list.append(el('li', {}, btn('⌂ Bridge', () => go({ kind: 'bridge' })), ' ', el('span', { class: 'muted' }, 'project overview')));
    for (const w of state.worktrees) {
      list.append(el('li', {}, btn(`⚗ ${w.slug}`, () => go({ kind: 'lab', slug: w.slug })), ' ',
        el('span', { class: `state-${w.stack}` }, w.stack), ' ', el('span', { class: 'muted' }, w.branch || '')));
    }
    body.append(list);
  });
}

// Panels.
let panelClose = null;
function openPanel(title, fill) {
  closePanel();
  $('panel-title').textContent = title;
  const body = $('panel-body');
  body.replaceChildren();
  $('panel').hidden = false;
  panelClose = fill(body) || null;
}
function closePanel() {
  panelClose?.();
  panelClose = null;
  $('panel').hidden = true;
}
$('panel-close').addEventListener('click', closePanel);
$('transport').addEventListener('click', transport);
function toggleView() {
  const m = world.setMode(world.mode === 'walk' ? 'orbit' : 'walk');
  $('view').textContent = m === 'walk' ? '◱ overview' : '🚶 walk';
  $('help').hidden = m !== 'walk';
}
$('view').addEventListener('click', toggleView);
addEventListener('keydown', (e) => {
  if (e.target.closest?.('input, textarea, select')) return;
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
      body.append(el('h3', {}, 'Whole lab'));
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
    body.append(el('div', { class: 'row' },
      btn('▣ Terminal', () => job(`${base}/${encodeURIComponent(slug)}/act/term`, body)),
      btn('✎ Neovim', () => job(`${base}/${encodeURIComponent(slug)}/act/nvim`, body))));
    body.append(el('p', { class: 'muted' }, 'Both open a window on this machine.'));
    if (w?.links.length) {
      body.append(el('h3', {}, 'Links'));
      body.append(el('div', { class: 'row' }, w.links.map((l) => ext(`${l.label} ↗`, safeURL(l.url)))));
    }
    if (w?.pr) {
      body.append(el('h3', {}, 'Pull request'));
      body.append(el('p', {}, ext(`#${w.pr.number} ↗`, safeURL(w.pr.url)), ' ', w.pr.title));
    }
  });
}

function showAgentDesk(slug) {
  openPanel(`Agent console · ${slug}`, (body) => {
    body.append(el('p', {}, 'Opens the agent in a terminal window (`railswitch agent --new-term`). A service agent keeps running when you close it.'));
    body.append(btn('▣ Open agent console', () => job(`${base}/${encodeURIComponent(slug)}/act/agent`, body)));
  });
}

// serviceAgents are a lab's agents that run as services (D142).
const serviceAgents = (w) => w.services.filter((s) => s.service.startsWith('agent-') && s.state !== 'on-demand').map((s) => s.service.slice(6));

function showAgent(slug) {
  openPanel(`Agent partner · ${slug}`, (body) => {
    const w = labOf(slug);
    const a = w?.agent;
    const status = el('p', {});
    const drawStatus = () => {
      const x = labOf(slug)?.agent;
      status.replaceChildren(x ? el('span', {}, el('strong', {}, x.name), ' · ', el('span', { class: `state-${x.state}` }, x.state), x.message ? ` · ${x.message}` : '') : el('span', { class: 'muted' }, 'No agent is running here.'));
    };
    drawStatus();
    body.append(status);
    const names = w ? serviceAgents(w) : [];
    if (!names.length) {
      body.append(el('p', { class: 'muted' }, 'Typing to the agent needs a service agent (`run = "service"`); this lab has none running. Use the agent console instead.'));
      body.append(btn('▣ Open agent console', () => job(`${base}/${encodeURIComponent(slug)}/act/agent`, body)));
      redraw = drawStatus;
      return () => { redraw = null; };
    }
    let name = a && names.includes(a.name) ? a.name : names[0];
    if (names.length > 1) {
      const sel = el('select', { on: { change: (e) => { name = e.target.value; poll(); } } }, names.map((n) => el('option', { value: n, selected: n === name }, n)));
      body.append(el('div', { class: 'row' }, 'Agent: ', sel));
    }
    body.append(el('h3', {}, 'Screen'));
    const pre = el('pre', { class: 'screen' }, '…');
    body.append(pre);
    const err = el('p', { class: 'err' });
    body.append(err);
    const box = el('textarea', { class: 'cmd', placeholder: 'Type to your agent. Enter sends; Shift+Enter is a new line.', maxlength: 4096 });
    const send = async () => {
      const text = box.value;
      if (!text.trim()) return;
      err.textContent = '';
      try {
        await post(`${base}/ship/agent/${encodeURIComponent(slug)}/send?agent=${encodeURIComponent(name)}`, text);
        box.value = '';
        setTimeout(poll, 300);
      } catch (e) {
        err.textContent = e.message;
      }
    };
    box.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        send();
      }
    });
    body.append(box, el('div', { class: 'row' }, btn('Send ⏎', send), btn('Open console ▣', () => job(`${base}/${encodeURIComponent(slug)}/act/agent`, body))));
    let stop = false;
    let timer = 0;
    async function poll() {
      clearTimeout(timer);
      try {
        const doc = await getJSON(`${base}/ship/agent/${encodeURIComponent(slug)}/screen?agent=${encodeURIComponent(name)}`);
        if (stop) return;
        if (!doc.running) pre.textContent = '(the agent container is not running: start the lab)';
        else if (!doc.session) pre.textContent = '(no session yet: send a message or open the console to start the agent)';
        else {
          const near = pre.scrollTop + pre.clientHeight >= pre.scrollHeight - 8;
          pre.textContent = doc.lines.join('\n');
          if (near) pre.scrollTop = pre.scrollHeight;
        }
      } catch (e) {
        if (!stop) pre.textContent = e.message;
      }
      if (!stop) timer = setTimeout(poll, document.hidden ? 5000 : 1500);
    }
    poll();
    box.focus();
    redraw = drawStatus;
    return () => { stop = true; clearTimeout(timer); redraw = null; };
  });
}

function showPRs() {
  openPanel('Pull requests', (body) => {
    const draw = () => {
      body.replaceChildren();
      if (state.pr_error) body.append(el('p', { class: 'err' }, state.pr_error));
      const open = state.worktrees.filter((w) => w.pr);
      const list = el('ul', { class: 'items' });
      for (const w of open) {
        const p = w.pr;
        list.append(el('li', {},
          el('div', {}, ext(`#${p.number} ↗`, safeURL(p.url)), ' ', p.title),
          el('div', { class: 'muted' }, `${w.slug} · ${p.state.toLowerCase()}`, p.checks ? ` · checks ${p.checks}` : '', p.review ? ` · review ${p.review}` : ''),
          el('div', { class: 'row' }, btn('⚗ Go to lab', () => go({ kind: 'lab', slug: w.slug })), p.review_app ? ext('Review app ↗', safeURL(p.review_app)) : '')));
      }
      if (!open.length) list.append(el('li', { class: 'muted' }, 'No worktree has an open PR.'));
      body.append(list, btn('↻ Refresh PRs', () => job(`${base}/prs/refresh`, body)));
    };
    draw();
    redraw = draw;
    return () => { redraw = null; };
  });
}

function showIssues() {
  openPanel('GitHub issues', (body) => {
    body.append(el('p', { class: 'muted' }, 'Scanning…'));
    getJSON(`${base}/ship/issues`).then((doc) => {
      body.replaceChildren();
      const list = el('ul', { class: 'items' });
      for (const i of doc.issues) {
        list.append(el('li', {},
          el('div', {}, ext(`#${i.number} ↗`, safeURL(i.url)), ' ', i.title),
          el('div', { class: 'muted' }, i.labels.map((l) => el('span', { class: 'tag' }, l)), i.assignees.length ? `→ ${i.assignees.join(', ')}` : '', i.author ? ` · by ${i.author}` : '')));
      }
      if (!doc.issues.length) list.append(el('li', { class: 'muted' }, 'No open issues.'));
      body.append(list);
    }).catch((e) => { body.replaceChildren(); errorTo(body, e); });
  });
}

function showNotes() {
  openPanel('Shared notes', (body) => {
    const list = async () => {
      body.replaceChildren(el('p', { class: 'muted' }, "The agents' shared notes (`notes/` in the workspace). Every agent reads them; keep secrets out."));
      try {
        const doc = await getJSON(`${base}/ship/notes`);
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
          doc = await getJSON(`${base}/ship/notes/${encodeURIComponent(n)}`);
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
          const out = await post(`${base}/ship/notes/${encodeURIComponent(n)}?if-hash=${encodeURIComponent(doc.hash)}`, ta.value);
          try {
            doc.hash = JSON.parse(out).hash || doc.hash;
          } catch {
            const again = await getJSON(`${base}/ship/notes/${encodeURIComponent(n)}`);
            doc.hash = again.hash;
          }
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
          await post(`${base}/ship/notes/${encodeURIComponent(n)}?rm=1&if-hash=${encodeURIComponent(doc.hash)}`, '');
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
    if (room && here.kind === 'lab') for (const t of room.ticks) world.tick(t);
    if (here?.kind === 'lab' && !labOf(here.slug)) go({ kind: 'bridge' });
  }
  redraw?.();
  const banner = $('banner');
  const lines = [state.error, ...state.banner].filter(Boolean);
  banner.hidden = !lines.length;
  banner.textContent = lines.join(' · ');
  const jobs = $('jobs');
  jobs.replaceChildren(...state.jobs.slice(0, 4).map((j) => el('li', { class: j.state }, `${j.slug || prefix}: ${j.label} · ${j.state}`)));
}

const es = new EventSource(`/events?p=${encodeURIComponent(prefix)}&view=ship`);
es.onmessage = (m) => {
  const ev = JSON.parse(m.data);
  if (ev.type === 'ship') apply(JSON.parse(ev.html));
};
addEventListener('hashchange', () => go(fromHash()));
