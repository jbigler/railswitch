// RailSwitch dashboard. The server renders every fragment; this script
// only swaps them in from the event stream, and drives keys, dialogs,
// notifications, the palette filter and the colour preview. A module
// (strict); its page-free logic is logic.js, tested by `node --test`.
import { fuzzy, target, hookLines, addTaskEdits, taskWay, pageQuery, termShown, settingsTab } from "./logic.js";

const body = document.body;
const prefix = body.dataset.prefix;
const view = body.dataset.view || ""; // "", settings, settingsfile, images, library, editor, review, import, machine, notes or new
const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];
const sortStore = "railswitch.sort." + prefix;
const projectStore = "railswitch.project";
const tabStore = "railswitch.tab." + prefix + "."; // + the slug, in sessionStorage
const wtStore = "railswitch.wt." + prefix;
const daysStore = "railswitch.usagedays"; // the usage views' range (D187)

// recall and remember read and write this browser's local storage, which a
// private window or blocked site data makes throw: the page works without.
function recall(key) {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function remember(key, value) {
  try {
    localStorage.setItem(key, value);
  } catch {}
}
const noticeStore = "railswitch.notice";

// Events.

let es = null;
let retry = null;
let failures = 0;

function connect() {
  if (es) es.close();
  clearTimeout(retry);
  // The new page has a prefix only once Create starts its job.
  const q = new URLSearchParams({ p: body.dataset.prefix, w: body.dataset.sel || "", sort: body.dataset.sort || "name", days: body.dataset.days || "", view: view === "new" || view === "machine" || view === "import" ? "new" : view === "library" ? "library" : view ? "settings" : "" });
  es = new EventSource("/events?" + q);
  es.onopen = () => {
    failures = 0;
    connection("");
  };
  es.onmessage = (m) => handle(JSON.parse(m.data));
  es.onerror = () => {
    failures++;
    // The browser retries a dropped stream itself; a refused one is
    // closed, so try again later (after a restart, or a new login).
    if (es.readyState === EventSource.CLOSED) {
      fetch(location.pathname, { method: "HEAD", credentials: "same-origin" })
        .then((r) => connection(r.status === 401 ? "Logged out — run `railswitch dashboard` for a new link" : "Disconnected — retrying"))
        .catch(() => connection("Disconnected — retrying"));
      retry = setTimeout(connect, 3000);
    } else if (failures > 1) {
      connection("Disconnected — retrying");
    }
  };
}

// connection shows why the page is no longer live ("" hides it): what it
// shows may be stale.
function connection(text) {
  const c = $("#conn");
  c.textContent = text;
  c.hidden = !text;
}

function handle(ev) {
  if (ev.type === "html" || ev.type === "job") swap(ev.id, ev.html);
  else if (ev.type === "notify") notify(ev);
}

function swap(id, html) {
  const el = document.getElementById(id);
  if (!el) return;
  const t = document.createElement("template");
  t.innerHTML = html;
  const next = t.content.firstElementChild;
  if (!next) return;
  // Each job's output stays where the reader left it; one at its end
  // (or new) follows the new lines.
  const scroll = {};
  // Folds and menus the reader opened or closed stay so.
  const kept = {};
  $$("details[data-keep]", el).forEach((d) => (kept[d.dataset.keep] = d.open));
  $$(".out", el).forEach((o) => {
    const job = o.closest(".job");
    if (job) scroll[job.id] = o.scrollTop + o.clientHeight >= o.scrollHeight - 4 ? null : o.scrollTop;
  });
  el.replaceWith(next);
  $$("details[data-keep]", next).forEach((d) => {
    if (d.dataset.keep in kept) d.open = kept[d.dataset.keep];
  });
  $$(".out", next).forEach((o) => {
    const job = o.closest(".job");
    const top = job ? scroll[job.id] : null;
    o.scrollTop = top == null ? o.scrollHeight : top;
  });
  afterSwap(id);
}

// markCurrent marks the header link of the page shown: the longest one the
// path is or lies under (a project's worktrees link covers its worktrees).
function markCurrent() {
  let best = null;
  for (const a of document.querySelectorAll("#header a.hb[href^='/']")) {
    a.removeAttribute("aria-current");
    const h = a.getAttribute("href");
    if ((location.pathname === h || location.pathname.startsWith(h + "/")) && (!best || h.length > best.getAttribute("href").length)) best = a;
  }
  if (best) best.setAttribute("aria-current", "page");
}

function afterSwap(id) {
  if (id === "header") {
    markCurrent();
    // Worktrees returns to the worktree last shown (after markCurrent,
    // which matches the plain link).
    const last = prefix && recall(wtStore);
    for (const a of $$("#header a.hb")) if (last && a.getAttribute("href") === "/p/" + prefix) a.href = pageURL(last);
    const n = Number($("#header").dataset.need || 0);
    document.title = n ? `(${n}) RailSwitch` : "RailSwitch";
    const btn = $("[data-notify]");
    if (btn) btn.hidden = !("Notification" in window) || Notification.permission !== "default";
  } else if (id === "projects") {
    filterSwitcher();
  } else if (id === "list") {
    filterList();
    termsShown();
  } else if (id === "jobs") {
    [...watched.keys()].forEach(jobEnded);
  } else if (id === "detail") {
    const slug = $("#detail").dataset.slug || "";
    body.dataset.sel = slug;
    if (slug) remember(wtStore, slug);
    // The worktree shown may not be the URL's (it was removed): follow it.
    // A URL without one stays: a narrow window shows the list for it.
    if (slug && body.dataset.pane === "detail" && location.pathname + location.search !== pageURL(slug)) history.replaceState(null, "", pageURL(slug));
    showTab();
    termsShown(); // showTab returns early on a detail without tabs
  }
}

// The detail's tabs: Overview shows every card, another tab only its own
// (CSS, from body[data-tab]). Each worktree keeps its own for the browser
// tab's session (sessionStorage); one not yet shown opens on Overview.

function tabFor(slug) {
  try {
    return sessionStorage.getItem(tabStore + slug);
  } catch {
    return null;
  }
}
function keepTab(slug, tab) {
  try {
    sessionStorage.setItem(tabStore + slug, tab);
  } catch {}
}

function showTab(tab) {
  const tabs = $$("#detail [data-tab]");
  if (!tabs.length) return;
  const slug = $("#detail").dataset.slug || "";
  if (tab) keepTab(slug, tab);
  tab = tab || tabFor(slug) || "overview";
  if (!tabs.some((b) => b.dataset.tab === tab)) tab = "overview";
  body.dataset.tab = tab;
  tabs.forEach((b) => b.setAttribute("aria-pressed", String(b.dataset.tab === tab)));
  termsShown();
}

// The list's filter: a worktree shows while its name or branch holds the
// text; a group with none showing hides.

function filterList() {
  const f = $("#wt-filter");
  if (!f) return;
  const q = f.value.trim().toLowerCase();
  $$("#list tr[data-slug]").forEach((tr) => (tr.hidden = q !== "" && !tr.dataset.filter.toLowerCase().includes(q)));
  $$("#list tbody.grp").forEach((g) => (g.hidden = !$$("tr[data-slug]", g).some((tr) => !tr.hidden)));
}

// Notifications.

function notify(ev) {
  if (!("Notification" in window) || Notification.permission !== "granted") return;
  const n = new Notification(ev.title, { body: ev.body, tag: ev.tag, renotify: true });
  n.onclick = () => {
    window.focus();
    n.close();
    const [, , p, slug] = ev.url.split("/");
    if (p === prefix && slug) select(slug, true);
    else location.href = ev.url;
  };
}

// Selection: the URL follows; the stream restarts with the new worktree
// and sends its detail and jobs.

function pageURL(slug) {
  return "/p/" + encodeURIComponent(prefix) + "/" + encodeURIComponent(slug) + pageQuery(body.dataset.sort, body.dataset.days);
}

function select(slug, push) {
  $$("#list tr[data-slug]").forEach((tr) => tr.classList.toggle("sel", tr.dataset.slug === slug));
  body.dataset.sel = slug;
  // A click opens the detail (a narrow window shows one pane); ↑↓ doesn't.
  if (push) body.dataset.pane = "detail";
  // ↑↓ on the list keeps its URL, so Back returns to the list, not a detail.
  if (push || body.dataset.pane !== "list") history[push ? "pushState" : "replaceState"](null, "", pageURL(slug));
  connect();
}

function move(delta) {
  const rows = $$("#list tr[data-slug]:not([hidden])");
  if (!rows.length) return;
  let i = rows.findIndex((tr) => tr.classList.contains("sel"));
  i = Math.min(rows.length - 1, Math.max(0, i + delta));
  rows[i].scrollIntoView({ block: "nearest" });
  select(rows[i].dataset.slug, false);
}

window.addEventListener("popstate", () => location.reload());

// Clicks.

document.addEventListener("click", (e) => {
  const t = e.target;
  const sortLink = t.closest("a[data-sort]");
  if (sortLink) {
    remember(sortStore, new URL(sortLink.href).searchParams.get("sort") || "name");
    return;
  }
  // The usage range (D187): kept in the URL and this browser, and the
  // stream restarts to send the usage views for it.
  const daysLink = t.closest("a[data-days]");
  if (daysLink && !e.ctrlKey && !e.metaKey && !e.shiftKey && e.button === 0) {
    e.preventDefault();
    body.dataset.days = daysLink.dataset.days;
    remember(daysStore, daysLink.dataset.days);
    history.replaceState(null, "", location.pathname + pageQuery(body.dataset.sort, body.dataset.days));
    connect();
    return;
  }
  // A worktree's segment of the usage chart opens its Agent tab.
  const usageWT = t.closest("#usage a[data-usage-wt]");
  if (usageWT && !e.ctrlKey && !e.metaKey && !e.shiftKey) {
    e.preventDefault();
    keepTab(usageWT.dataset.usageWt, "agent");
    select(usageWT.dataset.usageWt, true);
    return;
  }
  // An open menu closes on a click outside it, or on one of its items.
  $$("details.menu[open]").forEach((d) => {
    if (!d.contains(t) || t.closest(".menu-pop a, .menu-pop button")) d.open = false;
  });
  const tabBtn = t.closest("#detail [data-tab]");
  if (tabBtn) return showTab(tabBtn.dataset.tab);
  const row = t.closest("#list tr[data-slug]");
  if (row && !t.closest("a.pr") && !e.ctrlKey && !e.metaKey && !e.shiftKey) {
    e.preventDefault();
    select(row.dataset.slug, true);
    return;
  }
  const palTask = t.closest("#palette button[name=task]");
  if (palTask) {
    e.preventDefault(); // not the form's submit: runPaletteTask picks the way
    return runPaletteTask(palTask, e.shiftKey);
  }
  const termBtn = t.closest("[data-term]");
  if (termBtn) return openTermPanel(termBtn);
  const sw = t.closest("[data-switch-url]");
  if (sw) return switchAgent(sw);
  const opener = t.closest("[data-open]");
  if (opener) return openDialog(opener.dataset.open);
  if (t.closest("[data-colors]")) return openColors();
  if (t.closest("[data-notify]")) {
    Notification.requestPermission().then(() => afterSwap("header"));
    return;
  }
  const dlgLink = t.closest("a[data-dialog]");
  if (dlgLink) {
    e.preventDefault();
    loadDialog(dlgLink.dataset.dialog, dlgLink.href);
    return;
  }
  if (t.closest("[data-cancel]")) t.closest("dialog").close();
  if (!t.closest("#toast")) return;
  hideToast();
});

// A menu opens to the left of its button, or to the right when the left
// has no room (a button near the window's left edge).
document.addEventListener(
  "toggle",
  (e) => {
    const d = e.target;
    if (!(d instanceof HTMLDetailsElement) || !d.classList.contains("menu") || !d.open) return;
    const pop = $(".menu-pop", d);
    pop.classList.remove("rightward");
    if (pop.getBoundingClientRect().left < 8) pop.classList.add("rightward");
  },
  true,
);

function openDialog(id) {
  const d = document.getElementById(id);
  if (!d || d.open) return;
  d.showModal();
  const first = $("input:not([type=hidden])", d);
  if (first) first.focus();
  if (id === "switcher") filterSwitcher();
}

async function loadDialog(id, url) {
  const r = await fetch(url, { credentials: "same-origin" });
  const text = await r.text();
  if (!r.ok) return toast(text, true);
  const d = document.getElementById(id);
  d.innerHTML = text;
  if (!d.open) d.showModal();
  const first = $("input:not([type=hidden]):not([type=checkbox]), button", d);
  if (first) first.focus();
  return d;
}

// Forms: every form[data-post] is posted with fetch; the answer is a
// toast (202: started; else the server's message).

document.addEventListener("submit", (e) => {
  const f = e.target;
  if (f.classList.contains("colors-form") || f.classList.contains("colors-reset")) {
    e.preventDefault();
    postColors(f, new URLSearchParams(new FormData(f)));
    return;
  }
  if (!f.matches("form[data-post]")) return;
  e.preventDefault();
  if (f.dataset.confirm && !confirm(f.dataset.confirm)) return;
  const remove = f.classList.contains("remove");
  if (remove && f.elements.confirm && f.elements.confirm.value !== f.dataset.slug) {
    return toast("Type " + f.dataset.slug + " to confirm.", true);
  }
  const by = e.submitter;
  post(f, new URLSearchParams(new FormData(f, by)), by && by.hasAttribute("formaction") ? by.formAction : f.action);
});

async function post(f, data, url = f.action) {
  const r = await fetch(url, {
    method: "POST", body: data, credentials: "same-origin", headers: { Accept: "application/json" },
  });
  if (r.ok) {
    const j = await r.json().catch(() => ({}));
    toast(j.label ? "Started: " + j.label : "Done", false);
    if (f.hasAttribute("data-close")) {
      const d = f.closest("dialog");
      if (d) d.close();
      f.reset();
    }
  } else {
    toast((await r.text()).trim(), true);
  }
}

// The toast is a popover: shown again each time, it goes on top of the
// top layer, above a modal dialog that is open (an error a dialog's form
// caused would otherwise sit behind it).
let toastTimer = null;
function toast(text, err) {
  const t = $("#toast");
  t.textContent = text;
  t.classList.toggle("err", !!err);
  hideToast();
  t.showPopover();
  clearTimeout(toastTimer);
  toastTimer = setTimeout(hideToast, err ? 8000 : 3000);
}

function hideToast() {
  const t = $("#toast");
  if (t.matches(":popover-open")) t.hidePopover();
}

// Keys.

// Terminal panels (D149, D191): docked in #terms, under the jobs and outside
// the detail that SSE replaces, so switching tabs or worktrees keeps them
// running; termsShown shows only the shown worktree's. Each closes its
// WebSocket when removed. A button with data-term-tab shows that tab first;
// a terminal already open is shown again, not opened twice.
async function openTermPanel(btn) {
  const dock = $("#terms");
  if (!dock) return;
  if (btn.dataset.termTab) showTab(btn.dataset.termTab);
  const open = $$(".term-panel", dock).find((p) => p.dataset.termUrl === btn.dataset.term);
  if (open) {
    open.scrollIntoView({ block: "nearest" });
    open.focusTerm?.();
    return;
  }
  const slug = $("#detail")?.dataset.slug || "";
  const panel = document.createElement("section");
  panel.className = "term-panel";
  // Not data-term: a click in the panel would then open it again.
  panel.dataset.termUrl = btn.dataset.term;
  panel.dataset.slug = slug;
  if ("termAgent" in btn.dataset) panel.dataset.agent = "";
  const head = document.createElement("header");
  const title = document.createElement("span");
  title.className = "mono";
  title.textContent = slug + " · " + (btn.dataset.termLabel || "terminal");
  const full = document.createElement("button");
  full.type = "button";
  full.className = "ln";
  full.textContent = "Maximize";
  full.title = "Fill the window; Esc stays in the terminal (F11 then fills the screen)";
  const x = document.createElement("button");
  x.type = "button";
  x.className = "ln";
  x.textContent = "✕";
  x.setAttribute("aria-label", "Close terminal");
  const host = document.createElement("div");
  head.append(title, full, x);
  panel.append(head, host);
  dock.prepend(panel);
  panel.scrollIntoView({ block: "nearest" }); // the dock sits below the jobs
  let t = null;
  try {
    const { openTerminal } = await import("/static/term/term.js");
    t = openTerminal(host, btn.dataset.term);
    panel.focusTerm = () => t.focus();
  } catch (err) {
    host.textContent = String(err.message || err);
  }
  // Fills the window, not the screen: the browser's fullscreen ends on Esc,
  // which vim needs. F11 (the browser's own) fills the screen and keeps Esc.
  full.addEventListener("click", () => {
    const on = panel.classList.toggle("max");
    full.textContent = on ? "Restore" : "Maximize";
    t?.focus();
  });
  panel.closeTerm = () => {
    t?.close();
    panel.remove();
    termsShown();
  };
  x.addEventListener("click", panel.closeTerm);
  termsShown();
}

// switchAgent stops the running agent (D176) and, once that job ends
// well, opens the other agent's terminal, which starts it: one agent runs
// per worktree.
function switchAgent(btn) {
  const { switchUrl, switchFrom, switchTerm, switchTo } = btn.dataset;
  if (!confirm("Stop " + switchFrom + " and start " + switchTo + "? " + switchFrom + "'s session ends; what it changed in the worktree stays.")) return;
  const slug = $("#detail")?.dataset.slug;
  startJob(switchUrl, new URLSearchParams({ agent: switchFrom }), (state) => {
    if (state !== "ok") return toast(switchFrom + " did not stop; " + switchTo + " was not started. Its output is under Jobs.", true);
    // Left for another worktree meanwhile: never start an agent unasked.
    if ($("#detail")?.dataset.slug !== slug) return toast(switchFrom + " stopped; start " + switchTo + " from its Agent card.", false);
    openTermPanel({ dataset: { term: switchTerm, termLabel: switchTo, termAgent: "", termTab: "agent" } });
  });
}

// termsShown (D191) shows the shown worktree's panels and hides the rest,
// which keep running: an agent's only on the Agent tab, where it takes the
// Agent card's place (CSS, from body[data-agent-term]). A worktree gone
// from the list has its panels closed; each row counts its open ones.
function termsShown() {
  const slug = $("#detail")?.dataset.slug || "";
  const rows = $$("#list tr[data-slug]");
  const panels = $$("#terms .term-panel");
  if ($("#list")) {
    const live = new Set(rows.map((r) => r.dataset.slug));
    for (const p of panels) if (!live.has(p.dataset.slug)) p.closeTerm?.();
  }
  const left = $$("#terms .term-panel");
  for (const p of left) p.hidden = !termShown({ slug: p.dataset.slug, agent: "agent" in p.dataset }, slug, body.dataset.tab);
  for (const r of rows) {
    const n = left.filter((p) => p.dataset.slug === r.dataset.slug).length;
    const a = $("a[data-row]", r); // CSS attr() reads the link's own attribute
    if (!a) continue;
    if (n) a.dataset.terms = n;
    else delete a.dataset.terms;
  }
  if (slug !== "" && left.some((p) => p.dataset.slug === slug && "agent" in p.dataset)) body.dataset.agentTerm = "";
  else delete body.dataset.agentTerm;
}

document.addEventListener("keydown", (e) => {
  const d = $("dialog[open]");
  if (d && d.id === "palette") return paletteKey(e);
  // 1–9 jump while the filter is empty; after that they filter too.
  if (d && d.id === "switcher" && /^[1-9]$/.test(e.key) && !$("#switcher-filter").value) {
    e.preventDefault();
    const a = $(`#projects a[data-n="${e.key}"]`);
    if (a) location.href = a.href;
    return;
  }
  if (e.key === "Escape") hideToast();
  if (d || e.ctrlKey || e.metaKey || e.altKey || e.target.closest("input, textarea, select")) return;
  if (e.key === "ArrowDown" || e.key === "ArrowUp") {
    e.preventDefault();
    move(e.key === "ArrowDown" ? 1 : -1);
  } else if (e.key === "P" || e.key === "p") {
    e.preventDefault();
    openDialog("switcher");
  } else if (e.key === ":") {
    e.preventDefault();
    openPalette();
  }
});

// Switcher: typing filters projects and the worktrees of every project.

document.addEventListener("input", (e) => {
  if (e.target.id === "switcher-filter") filterSwitcher();
  else if (e.target.classList.contains("pal-filter")) filterPalette();
  else if (e.target.matches(".colors-form input[type=color]")) previewColors(e.target.form);
});

function filterSwitcher() {
  const input = $("#switcher-filter");
  const q = input ? input.value.trim().toLowerCase() : "";
  $$("#projects .proj-item").forEach((li) => {
    const own = li.dataset.filter.toLowerCase().includes(q);
    let any = false;
    $$(".wt-item", li).forEach((w) => {
      const hit = q !== "" && w.dataset.filter.toLowerCase().includes(q);
      w.hidden = !hit;
      any = any || hit;
    });
    li.hidden = !(own || any);
  });
}

// Command palette: the selected worktree's tasks and actions, the
// project's worktrees and pages. Enter runs the highlighted task in a
// terminal panel or as a job, as "in a terminal panel" says (ticked unless
// this browser last unticked it; a terminal_tasks task always in a
// terminal, D175), Shift+Enter in a desktop window; it posts an action
// and follows a link.

const termKey = "railswitch.palette.panel";

async function openPalette() {
  if (!prefix) return toast("No project.", true);
  const url = ($("#detail") && body.dataset.sel && $("#detail").dataset.tasks) || "/p/" + encodeURIComponent(prefix) + "/palette";
  const d = await loadDialog("palette", url);
  if (!d) return;
  $(".pal-filter", d).focus();
  filterPalette();
}

document.addEventListener("change", (e) => {
  if (!e.target.matches("#palette .pal-term") || e.target.disabled) return;
  remember(termKey, e.target.checked ? "1" : "0");
});

// palTermSync shows the box for the highlighted line: ticked and fixed for
// a terminal_tasks task, else this browser's choice.
function palTermSync() {
  const box = $("#palette .pal-term");
  if (!box) return;
  const it = $("#palette li.on button[name=task]");
  const fixed = !!it && it.hasAttribute("data-terminal");
  box.disabled = fixed;
  box.checked = fixed || recall(termKey) !== "0";
  $("#palette .pal-term-why").textContent = fixed
    ? "(always for this task: Settings → Tasks · Shift+Enter: a desktop window)"
    : "(off: a job, its output under Jobs · Shift+Enter: a desktop window)";
}

// runPaletteTask runs task button it: shift, a desktop window (task
// --new-term); a terminal_tasks task or a ticked box, a terminal panel in
// the page; else a job.
function runPaletteTask(it, shift) {
  const f = it.form;
  const name = it.value;
  const args = f.elements.args ? f.elements.args.value.trim() : "";
  const way = taskWay({ shift, terminal: it.hasAttribute("data-terminal"), panel: $(".pal-term", f).checked });
  if (way === "panel") {
    it.closest("dialog").close();
    const url = f.dataset.tty + "task?task=" + encodeURIComponent(name) + (args ? "&args=" + encodeURIComponent(args) : "");
    return openTermPanel({ dataset: { term: url, termLabel: args ? name + " " + args : name } });
  }
  const data = new URLSearchParams(new FormData(f));
  data.set("task", name);
  if (way === "window") data.set("term", "1");
  post(f, data);
}

function paletteItems() {
  return $$("#palette .pal-list li[data-filter], #palette .pal-list li.pal-new").filter((li) => !li.hidden);
}

function filterPalette() {
  const q = $("#palette .pal-filter").value.trim().toLowerCase();
  const list = $("#palette .pal-list");
  const lis = $$("li[data-filter]", list);
  lis.forEach((li, i) => {
    li.dataset.at ??= i; // the template's order, for ties and an empty filter
    li.score = fuzzy(q, li.dataset.filter.toLowerCase());
    li.hidden = li.score < 0;
  });
  lis.sort((a, b) => b.score - a.score || a.dataset.at - b.dataset.at);
  list.prepend(...lis); // best first; the lines without data-filter stay last
  // A name no task has: offer a new task by it on the Settings page, last
  // in the list (other lines may match it too); only with a worktree.
  const nw = $("#palette .pal-new");
  const name = $("#palette .pal-filter").value.trim();
  if (nw) nw.hidden = !name || $$("#palette button[name=task]").some((b) => b.value === name);
  const items = paletteItems();
  $$("#palette .pal-list li").forEach((li) => li.classList.remove("on"));
  if (items.length) items[0].classList.add("on");
  palTermSync();
  if (!nw) return;
  $("b", nw).textContent = name;
  $("a", nw).href = $("a", nw).dataset.base + "?task=" + encodeURIComponent(name) + "#tasks";
  const none = $("#palette .pal-none");
  if (none) none.hidden = !nw.hidden;
}

function paletteKey(e) {
  const items = paletteItems();
  let i = items.findIndex((li) => li.classList.contains("on"));
  if (e.key === "ArrowDown" || e.key === "ArrowUp") {
    e.preventDefault();
    if (!items.length) return;
    items.forEach((li) => li.classList.remove("on"));
    i = Math.min(items.length - 1, Math.max(0, i + (e.key === "ArrowDown" ? 1 : -1)));
    items[i].classList.add("on");
    items[i].scrollIntoView({ block: "nearest" });
    palTermSync();
  } else if (e.key === "Enter" && e.target.closest("input")) {
    e.preventDefault();
    if (i < 0) return;
    const f = $("#palette form");
    const it = $(".pal-task", items[i]);
    if (it.tagName === "A") return void (location.href = it.href);
    if (it.hasAttribute("data-action")) return post(f, new URLSearchParams(), it.formAction);
    runPaletteTask(it, e.shiftKey);
  }
}

// Colours: the editor shows the current mode; a colour change previews
// at once; a preset change is saved at once (its colours come from it).

const dark = matchMedia("(prefers-color-scheme: dark)");

async function openColors() {
  const d = await loadDialog("colors", "/colors");
  if (d) showMode(d);
}

function showMode(d) {
  const mode = dark.matches ? "dark" : "light";
  $$(".colors-mode", d).forEach((m) => (m.hidden = m.dataset.mode !== mode));
}

document.addEventListener("change", (e) => {
  if (!e.target.matches(".colors-form select.preset")) return;
  const f = e.target.form;
  postColors(f, new URLSearchParams({ mode: f.elements.mode.value, base: e.target.value }));
});

async function postColors(f, data) {
  const r = await fetch(f.action, { method: "POST", body: data, credentials: "same-origin" });
  const text = await r.text();
  if (!r.ok) return toast(text.trim(), true);
  const d = $("#colors");
  d.innerHTML = text;
  showMode(d);
  clearPreview();
  const link = $("#theme");
  link.href = "/theme.css?t=" + Date.now();
  toast("Colours saved", false);
}

// previewColors shows form f's colours at once, with contrast warnings.
// (It was a second preview(), which the settings one silently replaced
// while app.js was a classic script: a module refuses the duplicate.)
function previewColors(f) {
  const mine = f.dataset.mode === (dark.matches ? "dark" : "light");
  const vals = {};
  $$("input[type=color]", f).forEach((i) => {
    vals[i.dataset.token] = i.value;
    if (mine) document.documentElement.style.setProperty("--" + i.dataset.token, i.value);
  });
  const warn = [];
  for (const tok of ["text", "muted", "accent", "blue", "green", "yellow"]) {
    const c = contrast(vals[tok], vals.bg);
    if (c < 4.5) warn.push(`${tok}: contrast ${c.toFixed(1)}:1 against bg (below 4.5:1)`);
  }
  $(".cwarn", f).textContent = warn.join("\n");
}

function clearPreview() {
  for (const tok of ["bg", "bg2", "text", "muted", "accent", "blue", "green", "yellow"]) {
    document.documentElement.style.removeProperty("--" + tok);
  }
}

document.addEventListener("close", (e) => {
  if (e.target.id === "colors") clearPreview();
}, true);

function luminance(hex) {
  const [r, g, b] = [1, 3, 5].map((i) => {
    const v = parseInt(hex.substr(i, 2), 16) / 255;
    return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function contrast(a, b) {
  const [x, y] = [luminance(a), luminance(b)].sort((p, q) => q - p);
  return (x + 0.05) / (y + 0.05);
}

// Settings: the staged edit list lives in sessionStorage (a reload keeps
// it). Every change posts the whole list to preview and swaps #staged for
// the server's answer; the left column shows each control as staged.

const stagedStore = "rs-staged-" + prefix;
let staged = [];
let previewSeq = 0;
let restartWanted = false;
const watched = new Map(); // job id → called with (state, conflict) when it ends

function startSettings() {
  try {
    const s = JSON.parse(sessionStorage.getItem(stagedStore) || "[]");
    staged = Array.isArray(s) ? s : [];
  } catch {
    staged = [];
  }
  let kept = "";
  try {
    kept = sessionStorage.getItem(stabStore) || "";
  } catch {}
  showStab(settingsTab(location.hash.slice(1), kept, $$("[data-stab]").map((b) => b.dataset.stab)));
  syncLeft();
  if (staged.length) preview();
  loadBlocked();
}

// showStab shows one Settings section, as the worktree's tabs do, and
// keeps it for the browser tab's session.
const stabStore = "rs-stab-" + prefix;
function showStab(tab) {
  try {
    sessionStorage.setItem(stabStore, tab);
  } catch {}
  $$("[data-stab]").forEach((b) => b.setAttribute("aria-pressed", String(b.dataset.stab === tab)));
  $$("[data-spanel]").forEach((p) => (p.hidden = p.dataset.spanel !== tab));
}

// loadBlocked swaps in the Firewall card's "Would be blocked", which the
// server reads after the page: it inspects every container.
async function loadBlocked() {
  const el = $("#fw-blocked[data-src]");
  if (!el) return;
  try {
    const r = await fetch(el.dataset.src, { credentials: "same-origin" });
    if (!r.ok) return;
    swap("fw-blocked", await r.text());
    const n = Number($("#fw-blocked").dataset.count || 0);
    const badge = $("#fw-count");
    badge.textContent = n;
    badge.hidden = !n;
  } catch {
    // the dashboard went away: the placeholder stays
  }
}

// stage puts e in the list; back (the control is at its current value
// again) only takes the earlier edit of its target out.
function stage(e, back) {
  const t = target(e);
  staged = staged.filter((x) => target(x) !== t);
  if (!back) staged.push(e);
  stagedChanged();
}

function stagedChanged() {
  try {
    sessionStorage.setItem(stagedStore, JSON.stringify(staged));
  } catch {
    // a private window: the list lives until the page goes
  }
  syncLeft();
  preview();
}

function stagedFor(t) {
  return staged.find((x) => target(x) === t);
}

function syncLeft() {
  const fw = $("#firewall");
  if (fw) {
    const e = stagedFor("fwmode");
    const mode = e ? e.value : fw.dataset.mode;
    $$("input[name=fw-mode]", fw).forEach((r) => (r.checked = r.value === mode));
    $$("tr.fwhost", fw).forEach((row) => row.classList.toggle("staged", !!stagedFor("fw " + row.dataset.prof + " " + row.dataset.host)));
  }
  $$(".sopt").forEach((row) => {
    const e = stagedFor(row.hasAttribute("data-pack") ? "pack" : "option " + row.dataset.key);
    $("select", row).value = e ? e.ref : row.dataset.ref;
    row.classList.toggle("staged", !!e);
  });
  $$("tr.env").forEach((row) => {
    const e = stagedFor("env " + row.dataset.name);
    const input = $(".env-value", row);
    if (document.activeElement !== input) input.value = e && e.op === "env.set" ? e.value : row.dataset.value;
    input.disabled = !!e && e.op === "env.unset";
    row.classList.toggle("staged", !!e && e.op === "env.set");
    row.classList.toggle("removed", !!e && e.op === "env.unset");
  });
  // A staged new variable shows in the table until Apply writes it.
  const envBody = $("#env-table tbody");
  if (envBody) {
    $$("tr.env-new", envBody).forEach((row) => row.remove());
    const known = new Set($$("tr.env", envBody).map((row) => row.dataset.name));
    for (const e of staged) {
      if (e.op !== "env.set" || known.has(e.name)) continue;
      const row = document.createElement("tr");
      row.className = "env-new staged";
      row.dataset.name = e.name;
      for (const text of [e.name, e.value]) {
        const td = document.createElement("td");
        td.className = "mono";
        td.textContent = text;
        row.append(td);
      }
      const td = document.createElement("td");
      const undo = document.createElement("button");
      undo.type = "button";
      undo.className = "ln rm";
      undo.dataset.envUndo = "";
      td.className = "act";
      undo.textContent = "Undo";
      td.append(undo);
      row.append(td);
      envBody.append(row);
    }
    const none = $("tr.env-none", envBody);
    if (none) none.hidden = !!$("tr.env-new", envBody);
  }
  // A task row shows its staged command; struck through when it goes
  // (removed, or hidden: set to ""), highlighted when it changes or comes back.
  $$("tr.task").forEach((row) => {
    const e = stagedFor("task " + row.dataset.name);
    const input = $(".task-cmd", row);
    const gone = !!e && ((e.op === "task.set" && e.value === "") || (e.op === "task.unset" && row.dataset.source !== "hidden"));
    if (input && document.activeElement !== input) input.value = e && e.op === "task.set" && e.value !== "" ? e.value : row.dataset.command;
    if (input) input.disabled = gone;
    const fe = stagedFor("fresh " + row.dataset.name);
    const box = $(".task-fresh", row);
    if (box) box.checked = fe ? fe.op === "task.fresh.add" : row.dataset.fresh === "true";
    const te = stagedFor("terminal " + row.dataset.name);
    const tbox = $(".task-terminal", row);
    if (tbox) tbox.checked = te ? te.op === "task.terminal.add" : row.dataset.terminal === "true";
    row.classList.toggle("staged", (!!e || !!fe || !!te) && !gone);
    row.classList.toggle("removed", gone);
  });
  $$("label.hook").forEach((box) => {
    const e = stagedFor("hooks " + box.dataset.list);
    const area = $(".hook-cmds", box);
    if (document.activeElement !== area) area.value = e ? (e.values || []).join("\n") : box.dataset.orig;
    box.classList.toggle("staged", !!e);
  });
  $$("label.agent-setup").forEach((box) => {
    const e = stagedFor("agent-setup " + box.dataset.agent);
    const area = $(".agent-setup-cmds", box);
    if (document.activeElement !== area) area.value = e ? (e.values || []).join("\n") : box.dataset.orig;
    box.classList.toggle("staged", !!e);
  });
  $$("tr.link").forEach((row) => {
    const e = stagedFor("show " + row.dataset.label);
    const box = $(".link-show", row);
    if (box) box.checked = e ? e.op === "link.show" : row.dataset.hidden !== "true";
    row.classList.toggle("staged", !!e);
    row.classList.toggle("removed", !!stagedFor("link.remove " + row.dataset.label));
  });
}

async function preview() {
  const seq = ++previewSeq;
  const apply = $("#staged form[data-apply] button");
  if (apply) apply.disabled = true; // its labels are for the list before this change
  let r;
  try {
    r = await fetch(body.dataset.preview, {
      method: "POST", body: JSON.stringify(staged), credentials: "same-origin", headers: { "Content-Type": "application/json" },
    });
  } catch {
    return toast("Preview failed: the dashboard is not reachable.", true);
  }
  const text = await r.text();
  if (seq !== previewSeq) return; // a newer preview is on its way
  if (!r.ok) return toast(text.trim(), true);
  swap("staged", text);
  const restart = $("#staged input[name=restart]");
  if (restart) restart.checked = restartWanted;
}

// startJob posts a form that starts a job and calls done when the job
// ends, as the jobs fragment shows it; false when it didn't start.
async function startJob(url, data, done) {
  const r = await fetch(url, { method: "POST", body: data, credentials: "same-origin", headers: { Accept: "application/json" } });
  if (!r.ok) {
    toast((await r.text()).trim(), true);
    return false;
  }
  const j = await r.json();
  toast("Started: " + j.label, false);
  watched.set(j.id, done);
  jobEnded(j.id); // it may have ended before this answer came
  return true;
}

function jobEnded(id) {
  const done = watched.get(id);
  const el = document.getElementById("job-" + id);
  if (!done || !el || el.dataset.state === "running") return;
  watched.delete(id);
  done(el.dataset.state, el.hasAttribute("data-conflict"), el.dataset.exit);
}

function applied(state, conflict) {
  if (state === "ok") {
    staged = [];
    try {
      sessionStorage.removeItem(stagedStore);
    } catch {
      // nothing kept
    }
    location.reload();
  } else if (conflict) {
    toast("project.toml changed since the preview. Look at the changes again before applying.", true);
    preview();
  } else {
    toast("Apply " + state + ": nothing was changed. Its output is below.", true);
  }
}

function secretDone(state) {
  if (state === "ok") location.reload();
  else toast("The secret was not stored. Its output is below.", true);
}

function fileSaved(state, conflict) {
  if (state === "ok") location.reload();
  else if (conflict) $("#file-conflict").hidden = false;
  else toast("project.toml was not written. The output is below.", true);
}

// The project page's unset secrets: each one set leaves the card; Start
// waits for the last, and the card goes once the base worktree is up.
// Secret jobs are the project's, one at a time: the Set buttons wait.
if (view === "") {
  const setButtons = (card, on) => $$("form[data-secret-needed] button", card).forEach((b) => (b.disabled = !on));
  document.addEventListener("submit", async (e) => {
    const f = e.target;
    const card = f.closest("#secrets-needed");
    if (!card) return;
    e.preventDefault();
    const data = new URLSearchParams(new FormData(f));
    if (f.matches("form[data-secret-needed]")) {
      setButtons(card, false);
      const started = await startJob(f.action, data, (state) => {
        setButtons(card, true);
        if (state !== "ok") return toast("The secret was not stored. Its output is below.", true);
        f.closest("tr").remove();
        if ($("form[data-blocks]", card)) return;
        $$("[data-todo]", card).forEach((x) => (x.hidden = true));
        $("[data-done]", card).hidden = false;
        const start = $("form[data-start-base] button", card);
        if (start) start.disabled = false;
      });
      if (started) f.reset(); // the value leaves the page once the job has it
      else setButtons(card, true);
    } else if (f.matches("form[data-start-base]")) {
      const btn = $("button", f);
      btn.disabled = true;
      // The job is the base worktree's: its jobs list shows when it ends.
      if (body.dataset.sel !== f.dataset.base) select(f.dataset.base, true);
      const started = await startJob(f.action, data, (state) => {
        if (state === "ok") card.remove();
        else {
          toast("up " + state + ". Its output is below.", true);
          btn.disabled = false;
          $("[data-starting]", card).hidden = true;
        }
      });
      if (started) $("[data-starting]", card).hidden = false;
      else btn.disabled = false;
    }
  });
}

// Add buttons wait for their required fields.
function addReady(f) {
  $("button", f).disabled = $$("input[required]", f).some((i) => !i.value.trim());
}

if (view.startsWith("settings")) {
  document.addEventListener("click", (e) => {
    const t = e.target;
    const stab = t.closest("[data-stab]");
    if (stab) return showStab(stab.dataset.stab);
    const secretSet = t.closest("[data-secret-set]");
    if (secretSet) {
      const f = $("form.secret-set");
      f.hidden = false;
      f.elements.name.value = secretSet.dataset.secretSet;
      $("[data-secret-name]", f).textContent = secretSet.dataset.secretSet;
      return f.elements.value.focus();
    }
    if (t.closest("[data-secret-cancel]")) {
      const f = $("form.secret-set");
      f.reset();
      f.hidden = true;
      return;
    }
    if (t.closest("[data-fw-allow-selected]")) {
      for (const box of $$(".fw-pick:checked")) stage({ op: "firewall.allow.add", key: box.dataset.prof, value: box.dataset.host }, false);
      return;
    }
    const undo = t.closest("[data-undo]");
    if (undo) {
      staged.splice(Number(undo.dataset.undo), 1);
      stagedChanged();
      return;
    }
    const envUndo = t.closest("[data-env-undo]");
    if (envUndo) {
      staged = staged.filter((x) => target(x) !== "env " + envUndo.closest("tr").dataset.name);
      stagedChanged();
      return $("form[data-stage=env] input[name=name]").focus(); // the row, and its button, are gone
    }
    const envRm = t.closest("[data-env-remove]");
    if (envRm) return stage({ op: "env.unset", name: envRm.closest("tr").dataset.name }, false);
    const fwRm = t.closest("[data-fw-remove]");
    if (fwRm) {
      const row = fwRm.closest("tr");
      return stage({ op: "firewall.allow.remove", key: row.dataset.prof, value: row.dataset.host }, false);
    }
    const fwAllow = t.closest("[data-fw-allow]");
    if (fwAllow) return stage({ op: "firewall.allow.add", key: fwAllow.dataset.prof, value: fwAllow.dataset.host }, false);
    // Hiding or removing a task takes it out of fresh_tasks and
    // terminal_tasks too: a name there with no task is a render error.
    const unfresh = (row) => {
      if (row.dataset.fresh === "true") stage({ op: "task.fresh.remove", name: row.dataset.name }, false);
      if (row.dataset.terminal === "true") stage({ op: "task.terminal.remove", name: row.dataset.name }, false);
    };
    const taskHide = t.closest("[data-task-hide]");
    if (taskHide) {
      unfresh(taskHide.closest("tr"));
      return stage({ op: "task.set", name: taskHide.closest("tr").dataset.name, value: "" }, false);
    }
    const taskRm = t.closest("[data-task-remove], [data-task-restore]");
    if (taskRm) {
      const row = taskRm.closest("tr");
      if (t.closest("[data-task-remove]") && !row.dataset.stock) unfresh(row); // back to the pack's keeps the task
      return stage({ op: "task.unset", name: row.dataset.name }, false);
    }
    const linkRm = t.closest("[data-link-remove]");
    if (linkRm) stage({ op: "link.remove", label: linkRm.closest("tr").dataset.label }, false);
  });

  document.addEventListener("change", (e) => {
    const t = e.target;
    const opt = t.closest(".sopt");
    if (t.name === "fw-mode") return stage({ op: "firewall.mode", value: t.value }, t.value === $("#firewall").dataset.mode);
    if (opt && opt.hasAttribute("data-pack")) return stage({ op: "pack", ref: t.value }, t.value === opt.dataset.ref);
    if (opt) return stage({ op: "option", key: opt.dataset.key, ref: t.value }, t.value === opt.dataset.ref);
    if (t.classList.contains("fw-pick")) {
      $("[data-fw-allow-selected]").disabled = !$(".fw-pick:checked");
      return;
    }
    if (t.classList.contains("env-value")) {
      const row = t.closest("tr");
      return stage({ op: "env.set", name: row.dataset.name, value: t.value }, t.value === row.dataset.value);
    }
    if (t.classList.contains("task-cmd")) {
      const row = t.closest("tr");
      // An emptied box is no edit: Hide or Remove says that.
      return stage({ op: "task.set", name: row.dataset.name, value: t.value.trim() }, t.value.trim() === row.dataset.command || !t.value.trim());
    }
    if (t.classList.contains("task-fresh")) {
      const row = t.closest("tr");
      return stage({ op: t.checked ? "task.fresh.add" : "task.fresh.remove", name: row.dataset.name }, t.checked === (row.dataset.fresh === "true"));
    }
    if (t.classList.contains("task-terminal")) {
      const row = t.closest("tr");
      return stage({ op: t.checked ? "task.terminal.add" : "task.terminal.remove", name: row.dataset.name }, t.checked === (row.dataset.terminal === "true"));
    }
    if (t.classList.contains("agent-setup-cmds")) {
      const box = t.closest("label.agent-setup");
      const values = hookLines(t.value);
      return stage({ op: "agent.setup", key: box.dataset.agent, values: values }, values.join("\n") === hookLines(box.dataset.orig).join("\n"));
    }
    if (t.classList.contains("hook-cmds")) {
      const box = t.closest("label.hook");
      const values = hookLines(t.value);
      return stage({ op: "hooks.set", key: box.dataset.list, values: values }, values.join("\n") === hookLines(box.dataset.orig).join("\n"));
    }
    if (t.classList.contains("link-show")) {
      const row = t.closest("tr");
      return stage({ op: t.checked ? "link.show" : "link.hide", label: row.dataset.label }, t.checked === (row.dataset.hidden !== "true"));
    }
    if (t.name === "restart" && t.closest("#staged")) restartWanted = t.checked;
    // An Install review with flags: Apply waits for the tick.
    if (t.name === "ack" && t.closest("#staged")) {
      $("#staged form[data-apply] button").disabled = $("#staged").hasAttribute("data-blocked") || !t.checked;
    }
  });

  document.addEventListener("input", (e) => {
    const f = e.target.closest("form[data-stage]");
    if (f) addReady(f);
  });

  document.addEventListener("submit", (e) => {
    const f = e.target;
    if (f.matches("form[data-stage]")) {
      e.preventDefault();
      const v = (n) => f.elements[n].value.trim();
      if (f.dataset.stage === "env") stage({ op: "env.set", name: v("name"), value: f.elements.value.value }, false);
      else if (f.dataset.stage === "service") stage({ op: "option", key: v("key"), ref: f.elements.ref.value }, false);
      else if (f.dataset.stage === "task") {
        for (const ed of addTaskEdits(v("name"), v("value"), f.elements.fresh.checked, f.elements.terminal.checked)) stage(ed, false);
      }
      else if (f.dataset.stage === "fw") stage({ op: "firewall.allow.add", key: f.elements.key.value, value: v("value") }, false);
      else stage({ op: "link.add", label: v("label"), url: v("url") }, false);
      f.reset();
      addReady(f);
    } else if (f.matches("form[data-apply]")) {
      e.preventDefault();
      const data = new URLSearchParams(new FormData(f));
      data.set("edits", JSON.stringify(staged));
      startJob(f.action, data, applied);
    } else if (f.matches("form[data-job]")) {
      e.preventDefault();
      const data = new URLSearchParams(new FormData(f));
      if (f.dataset.job === "file") {
        data.set("text", f.elements.text.value); // as typed: FormData would send CRLF
        $("#file-conflict").hidden = true;
        startJob(f.action, data, fileSaved);
      } else if (f.dataset.job.startsWith("template-")) {
        startJob(f.action, data, (state) => {
          if (state === "ok") location.reload();
          else toast("It did not finish: " + state + ". The output is below.", true);
        });
      } else {
        f.reset(); // the value leaves the page at once
        if (f.classList.contains("secret-set")) f.hidden = true;
        startJob(f.action, data, secretDone);
      }
    }
  });
}

// Editor (a Containerfile or a package file): Save waits for the
// acknowledgement and, where Check is required, a passing Check of the
// text as it is now; an edit after Check turns Save off again.

function startEditor() {
  const f = $("#editor");
  if (!f) return;
  const text = f.elements.text;
  let passed = null; // the text the last passing Check judged
  let checkSeq = 0;

  function ready() {
    const save = $("[data-save]", f);
    if (!save) return;
    const ack = !f.hasAttribute("data-ack-required") || f.elements.ack.checked;
    const checked = !f.hasAttribute("data-check-required") || passed === text.value;
    save.disabled = !(ack && checked);
  }

  function edited() {
    const c = $("#check");
    if (passed !== null && passed !== text.value && c && !$("[data-stale]", c)) {
      c.removeAttribute("data-ok");
      const p = document.createElement("p");
      p.className = "yellow";
      p.dataset.stale = "";
      p.textContent = "Edited since this Check: check again before saving.";
      c.prepend(p);
    }
    ready();
  }

  async function check() {
    const seq = ++checkSeq;
    const sent = text.value;
    const btn = $("[data-check-run]", f);
    btn.disabled = true;
    let r;
    try {
      r = await fetch(f.dataset.check, {
        method: "POST", body: new URLSearchParams({ hash: f.elements.hash.value, text: sent }), credentials: "same-origin",
      });
    } catch {
      btn.disabled = false;
      return toast("Check failed: the dashboard is not reachable.", true);
    }
    const html = await r.text();
    btn.disabled = false;
    if (seq !== checkSeq) return; // a newer Check is on its way
    if (!r.ok) return toast(html.trim(), true);
    swap("check", html);
    passed = $("#check").hasAttribute("data-ok") ? sent : null;
    edited(); // typed while it ran
  }

  function saved(state, conflict, exit) {
    if (state === "ok") location.assign(location.pathname); // drops ?restart=failed
    else if (exit === "3") location.assign(location.pathname + "?restart=failed"); // written; restart failed
    else if (conflict) $("#ed-conflict").hidden = false;
    else toast("The file was not written. The output is below.", true);
  }

  text.addEventListener("input", edited);
  f.addEventListener("change", (e) => {
    const t = e.target;
    if (t.name === "ack") return ready();
    if (!t.matches("[data-load]") || !t.files.length) return;
    const file = t.files[0];
    t.value = "";
    if (file.size > 1 << 20) return toast("The file is larger than 1 MiB.", true);
    file.text().then((s) => {
      text.value = s;
      edited();
    });
  });
  f.addEventListener("click", (e) => {
    if (e.target.closest("[data-check-run]")) check();
  });
  f.addEventListener("submit", (e) => {
    e.preventDefault();
    const data = new URLSearchParams(new FormData(f));
    data.set("text", text.value); // as typed: FormData would send CRLF
    $("#ed-conflict").hidden = true;
    startJob(f.action, data, saved);
  });
  ready();
}

// Job forms of the images, library, editor and machine pages: an optional
// confirm, then the job; when it ends ok the page goes on to the file it
// made (data-next) or reloads.

// copiedVersion is the version a new package gets: the copied built-in's
// (pack eject keeps it), 1 for a blank one.
function copiedVersion(from) {
  const m = /@(\d+)$/.exec(from);
  return m ? m[1] : "1";
}

function jobNext(f) {
  const v = (n) => (f.elements[n] ? f.elements[n].value.trim() : "");
  switch (f.dataset.job) {
    case "new-addon":
      return f.dataset.next + v("name");
    case "new-package":
      return f.dataset.next + "local:" + v("name") + "@" + copiedVersion(v("from")) + "/" + (v("kind") === "option" ? "option.toml" : "pack.toml");
    case "delete":
      return f.dataset.next;
  }
  return "";
}

if (view === "images" || view === "library" || view === "editor" || view === "machine" || view === "pr" || view === "tidy") {
  document.addEventListener("submit", (e) => {
    const f = e.target;
    if (f.matches("form[data-add-file]")) {
      e.preventDefault();
      location.href = f.dataset.base + f.elements.path.value.trim() + "?new=1";
      return;
    }
    if (!f.matches("form[data-job]")) return;
    e.preventDefault();
    if (f.dataset.confirm && !confirm(f.dataset.confirm)) return;
    const next = jobNext(f);
    startJob(f.action, new URLSearchParams(new FormData(f, e.submitter)), (state) => {
      if (state !== "ok") return toast("It did not finish: " + state + ". The output is below.", true);
      if (next) location.href = next;
      else location.reload();
    });
  });
}

// Review update: Accept waits for the acknowledgement of the flagged
// items. The job ending ok goes to the project page; so does a failed
// restart (exit 3: the update was accepted), with a notice. An import
// (view import) is the same form, going back to its data-next page.
if (view === "review" || view === "import") {
  const f = $("#accept");
  if (f) {
    const btn = $("[data-accept]", f);
    const ready = () => {
      btn.disabled = f.hasAttribute("data-ack-required") && !f.elements.ack.checked;
    };
    f.addEventListener("change", ready);
    f.addEventListener("submit", async (e) => {
      e.preventDefault();
      btn.disabled = true;
      const started = await startJob(f.action, new URLSearchParams(new FormData(f)), (state, _conflict, exit) => {
        if (state === "ok") return location.assign(f.dataset.next);
        if (exit === "3") {
          try {
            sessionStorage.setItem(noticeStore, "The update was accepted, but the restart failed. Its output is in the lock --accept job below.");
          } catch {
            // no notice kept
          }
          return location.assign(f.dataset.next);
        }
        toast("The update was not accepted: " + state + ". The output is below.", true);
        ready();
      });
      if (!started) ready();
    });
    ready();
  }
}

// New project: Read the repository posts repo and branch (with the
// picked pack, when one is picked) to /new/detect and swaps in the Stack
// card; Start from a template swaps in that template's questions instead
// (and Read waits until no template is picked); Next: review posts the
// whole form to /new/review and swaps in step 2; Create starts project
// new as a job of the new prefix and follows it on that prefix's stream.
// Any change after a review marks it stale and turns Create off until the
// next review.

function startNew() {
  const form = $("#new-form");
  const el = form.elements;
  let extras = []; // Add service rows, {key, ref}, kept across reads
  let reviewed = false;
  let dirAuto = true; // the folder follows the prefix until edited
  const autoDir = () => (el.prefix.value.trim() ? body.dataset.home + "/workspace/" + el.prefix.value.trim() : "");

  function ready() {
    const stack = $("#new-stack").dataset;
    $("[data-detect]", form).disabled = !el.repo.value.trim() || !!el.template.value;
    $("[data-review]", form).disabled = !((stack.pack || stack.template) && el.prefix.value.trim() && el.dir.value.trim());
    const add = $("[data-add-extra]", form);
    if (add) add.disabled = !$(".extra-key", form).value.trim();
  }

  function stale() {
    if (!reviewed) return;
    const note = $("#new-review [data-stale]");
    if (note) note.hidden = false;
    const create = $("#new-review form[data-create] button");
    if (create) create.disabled = true;
  }

  async function detect(withPack) {
    const data = new URLSearchParams({ repo: el.repo.value, branch: el.branch.value });
    if (withPack && el.pack) data.set("pack", el.pack.value);
    const err = $("#repo-error");
    const btn = $("[data-detect]", form);
    err.hidden = true;
    btn.disabled = true;
    btn.textContent = "Reading…";
    let r = null;
    try {
      r = await fetch("/new/detect", { method: "POST", body: data, credentials: "same-origin" });
    } catch {
      // shown below
    }
    btn.textContent = "Read the repository";
    const text = r ? (await r.text()).trim() : "The dashboard is not reachable.";
    if (r && r.ok) {
      swap("new-stack", text);
      if (!el.branch.value.trim()) el.branch.value = $("#new-stack").dataset.branch || "";
      renderExtras();
      warnings();
    } else {
      err.textContent = text;
      err.hidden = false;
    }
    stale();
    ready();
  }

  // The template's own tools and agents stand unless one is ticked here.
  async function pickTemplate() {
    const ref = el.template.value;
    let r = null;
    try {
      r = await fetch("/new/template", { method: "POST", body: new URLSearchParams({ template: ref }), credentials: "same-origin" });
    } catch {
      // shown below
    }
    const text = r ? (await r.text()).trim() : "The dashboard is not reachable.";
    if (r && r.ok) {
      swap("new-stack", text);
      extras = [];
      if (ref) $$("input[name=tool], input[name=agent]", form).forEach((c) => (c.checked = false));
      warnings();
    } else {
      toast(text, true);
    }
    stale();
    ready();
  }

  function slotChanged(row) {
    const checked = $("input[type=radio]:checked", row);
    const ref = checked ? checked.value : row.dataset.ref;
    const changed = ref !== row.dataset.ref;
    row.classList.toggle("staged", changed);
    const nw = $(".opt-new", row);
    nw.hidden = !changed;
    nw.textContent = changed ? "→ " + ref : "";
    $(".slot-label", row).textContent = changed
      ? "Changed by you · " + (row.dataset.source === "repo" ? "the repository suggests " : "the pack default is ") + row.dataset.ref
      : row.dataset.label;
    warnings();
  }

  function warnings() {
    const box = $("#new-warnings");
    box.replaceChildren();
    $$(".slot", form).forEach((row) => {
      const checked = $("input[type=radio]:checked", row);
      if (row.dataset.source !== "repo" || !checked || checked.value === row.dataset.ref) return;
      const p = document.createElement("p");
      p.className = "yellow";
      p.textContent = `${row.dataset.title}: you picked ${checked.value}, but the repository suggests ${row.dataset.ref}.`;
      box.append(p);
    });
    box.hidden = !box.children.length;
  }

  function renderExtras() {
    const box = $("#new-extras");
    if (!box) return;
    box.replaceChildren(...extras.map((x, i) => {
      const row = document.createElement("div");
      row.className = "opt";
      row.innerHTML = '<div class="opt-hd"><b></b> <span class="mono"></span> <span class="muted">extra</span><span class="sp"></span><button type="button" class="ln danger" data-extra-remove>Remove</button></div><input type="hidden" name="option">';
      $("b", row).textContent = x.key;
      $(".mono", row).textContent = x.ref;
      $("button", row).dataset.index = i;
      $("input", row).value = x.key + "=" + x.ref;
      return row;
    }));
  }

  function changed() {
    stale();
    ready();
  }

  form.addEventListener("input", (e) => {
    const t = e.target;
    if (t === el.prefix && dirAuto) el.dir.value = autoDir();
    if (t === el.dir) dirAuto = el.dir.value === "" || el.dir.value === autoDir();
    if (!t.classList.contains("extra-key")) return changed();
    ready();
  });

  form.addEventListener("change", (e) => {
    const t = e.target;
    if (t.matches("[data-repick]")) return detect(true);
    if (t.matches("[data-template]")) return pickTemplate();
    const row = t.closest(".slot");
    if (row && t.type === "radio") slotChanged(row);
    if (!t.classList.contains("extra-key") && !t.classList.contains("extra-ref")) changed();
  });

  form.addEventListener("click", (e) => {
    const t = e.target;
    if (t.closest("[data-detect]")) return detect(false);
    const change = t.closest("[data-change]");
    if (change) {
      const c = $(".choices", change.closest(".opt"));
      c.hidden = !c.hidden;
      return;
    }
    if (t.closest("[data-add-extra]")) {
      const key = $(".extra-key", form).value.trim();
      const ref = $(".extra-ref", form).value;
      if (!key || !ref) return;
      if (extras.some((x) => x.key === key) || $$(".slot", form).some((r) => r.dataset.key === key)) {
        return toast(key + " is already a service.", true);
      }
      extras.push({ key, ref });
      $(".extra-key", form).value = "";
      renderExtras();
      return changed();
    }
    const rm = t.closest("[data-extra-remove]");
    if (rm) {
      extras.splice(Number(rm.dataset.index), 1);
      renderExtras();
      changed();
    }
  });

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const btn = $("[data-review]", form);
    btn.disabled = true;
    let r = null;
    try {
      r = await fetch(form.action, { method: "POST", body: new URLSearchParams(new FormData(form)), credentials: "same-origin" });
    } catch {
      // shown below
    }
    const text = r ? (await r.text()).trim() : "The dashboard is not reachable.";
    if (r && r.ok) {
      swap("new-review", text);
      reviewed = true;
    } else {
      toast(text, true);
    }
    ready();
  });

  // An Install review with flags: Create waits for the tick (and for a
  // fresh review when step 1 changed).
  document.addEventListener("change", (e) => {
    const f = e.target.closest("form[data-create]");
    if (!f || e.target.name !== "ack") return;
    const note = $("#new-review [data-stale]");
    $("button", f).disabled = !e.target.checked || (note && !note.hidden);
  });

  document.addEventListener("submit", async (e) => {
    const f = e.target;
    if (!f.matches("form[data-create]")) return;
    e.preventDefault();
    const btn = $("button", f);
    const data = new URLSearchParams(new FormData(form));
    if (f.elements.up && f.elements.up.checked) data.set("up", "1");
    if (f.elements.accept_install) data.set("accept_install", f.elements.accept_install.value);
    if (f.elements.ack && f.elements.ack.checked) data.set("ack", "1");
    const p = el.prefix.value.trim();
    body.dataset.prefix = p;
    connect();
    btn.disabled = true;
    const started = await startJob(f.action, data, async (state, _conflict, exit) => {
      const page = "/p/" + encodeURIComponent(p);
      // Asking also reloads the dashboard's project list, so the page opens
      // even when the list was read moments before the project existed.
      // Exit 3: created, but up failed. The registry has the last word.
      let registered = exit === "3";
      try {
        const r = await fetch("/new/registered?prefix=" + encodeURIComponent(p), { credentials: "same-origin" });
        if (r.ok) registered = (await r.json()).registered;
      } catch {
        // the exit code decides
      }
      if (state === "ok") {
        location.href = page;
        return;
      }
      if (registered) {
        try {
          sessionStorage.setItem(noticeStore, "Project " + p + " was created, but up failed. Its output is in the project new job below.");
        } catch {
          // no notice kept
        }
        location.href = page;
        return;
      }
      toast("project new " + state + ". The form keeps your values; the job's output says what was left and how to remove it.", true);
      btn.disabled = false;
    });
    if (!started) btn.disabled = false;
  });

  ready();
}

// Notes (D155): Save posts the text with the hash it was opened at, and
// the CLI refuses it when the note changed since; the refusal stays shown
// and the text stays in the box.

function startNotes() {
  const f = $("#note");
  if (!f) return;
  const err = $("#note-err");
  const name = decodeURIComponent(f.action.split("/").pop());
  async function write(q, text) {
    const r = await fetch(f.action + "?" + new URLSearchParams(q), {
      method: "POST", body: text, credentials: "same-origin", headers: { "Content-Type": "text/plain; charset=utf-8" },
    });
    if (r.ok) return true;
    err.textContent = (await r.text()).trim();
    err.hidden = false;
    toast(err.textContent, true);
    return false;
  }
  f.addEventListener("submit", async (e) => {
    e.preventDefault();
    if (await write({ "if-hash": f.dataset.hash }, f.elements.text.value)) location.href = f.dataset.back + "?n=" + encodeURIComponent(name);
  });
  $("[data-note-rm]", f)?.addEventListener("click", async () => {
    if (!confirm("Delete " + name + "?")) return;
    if (await write({ rm: "1", "if-hash": f.dataset.hash }, "")) location.href = f.dataset.back;
  });
}

// Start: the remembered project and sort, then the stream.

(function start() {
  try {
    const notice = sessionStorage.getItem(noticeStore);
    if (notice) {
      sessionStorage.removeItem(noticeStore);
      toast(notice, true);
    }
  } catch {
    // no notice kept
  }
  if (view === "new") {
    afterSwap("header");
    startNew();
    return;
  }
  if (!prefix) return;
  if (view) {
    afterSwap("header");
    if (view === "settings") startSettings();
    if (view === "editor") startEditor();
    if (view === "notes") startNotes();
    connect();
    return;
  }
  const nav = performance.getEntriesByType("navigation")[0];
  const last = recall(projectStore);
  if (nav && nav.redirectCount > 0 && last && last !== prefix && $$("#projects .proj-item").some((li) => li.dataset.filter === last)) {
    location.replace("/p/" + encodeURIComponent(last));
    return;
  }
  remember(projectStore, prefix);
  const params = new URLSearchParams(location.search);
  const saved = recall(sortStore);
  // Only a key the sort bar still offers (an old "running" is gone, D170).
  const offered = (k) => $$("#list a[data-sort]").some((a) => (new URL(a.href).searchParams.get("sort") || "").replace(/^-/, "") === k.replace(/^-/, ""));
  const savedDays = recall(daysStore);
  let moved = false;
  if (!params.has("sort") && saved && saved !== "name" && offered(saved)) {
    params.set("sort", saved);
    moved = true;
  }
  if (!params.has("days") && (savedDays === "7" || savedDays === "30")) {
    params.set("days", savedDays);
    moved = true;
  }
  if (moved) {
    location.replace(location.pathname + "?" + params);
    return;
  }
  afterSwap("header");
  showTab();
  $("#wt-filter")?.addEventListener("input", filterList);
  connect();
})();


// Markdown from GitHub (D152): each [data-md] element's text, formatted
// into elements built with textContent only, so nobody's Markdown can add
// markup: headings, lists, quotes, fenced code, inline code, bold, italics
// and links (http and https only). Anything else stays text.
function mdInline(parent, text) {
  const re = /(`[^`]+`)|(\*\*[^*]+\*\*)|(\*[^*\s][^*]*\*)|\[([^\]]+)\]\((https?:\/\/[^)\s]+)\)|(https?:\/\/[^\s<>()]+)/g;
  let at = 0;
  for (const m of text.matchAll(re)) {
    parent.append(text.slice(at, m.index));
    let el;
    if (m[1]) { el = document.createElement("code"); el.textContent = m[1].slice(1, -1); }
    else if (m[2]) { el = document.createElement("strong"); el.textContent = m[2].slice(2, -2); }
    else if (m[3]) { el = document.createElement("em"); el.textContent = m[3].slice(1, -1); }
    else {
      // a bare URL leaves the sentence's punctuation after it as text
      const bare = m[6] ? m[6].replace(/[.,;:!?'"]+$/, "") : "";
      el = document.createElement("a");
      el.href = m[5] || bare;
      el.textContent = m[4] || bare;
      el.target = "_blank";
      el.rel = "noopener noreferrer";
      if (m[6]) {
        parent.append(el);
        at = m.index + bare.length;
        continue;
      }
    }
    parent.append(el);
    at = m.index + m[0].length;
  }
  parent.append(text.slice(at));
}

function mdFormat(box) {
  const lines = box.textContent.replace(/\r\n?/g, "\n").split("\n");
  box.textContent = "";
  let list = null, para = null;
  const flush = () => { list = null; para = null; };
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (/^```/.test(line)) {
      flush();
      const pre = document.createElement("pre");
      const code = [];
      for (i++; i < lines.length && !/^```/.test(lines[i]); i++) code.push(lines[i]);
      pre.textContent = code.join("\n");
      box.append(pre);
      continue;
    }
    let m;
    if (!line.trim()) { flush(); continue; }
    if ((m = line.match(/^(#{1,6})\s+(.*)$/))) {
      flush();
      const h = document.createElement("h" + Math.min(6, m[1].length + 2));
      mdInline(h, m[2]);
      box.append(h);
    } else if ((m = line.match(/^\s*([-*+]|\d+[.)])\s+(?:\[([ xX])\]\s+)?(.*)$/))) {
      para = null;
      const ordered = /\d/.test(m[1]);
      if (!list || (list.tagName === "OL") !== ordered) {
        list = document.createElement(ordered ? "ol" : "ul");
        box.append(list);
      }
      const li = document.createElement("li");
      if (m[2]) li.append(m[2] === " " ? "☐ " : "☑ ");
      mdInline(li, m[3]);
      list.append(li);
    } else if ((m = line.match(/^>\s?(.*)$/))) {
      flush();
      const q = document.createElement("blockquote");
      mdInline(q, m[1]);
      box.append(q);
    } else {
      list = null;
      if (!para) { para = document.createElement("p"); box.append(para); } else para.append(document.createElement("br"));
      mdInline(para, line);
    }
  }
}
document.querySelectorAll("[data-md]").forEach(mdFormat);
