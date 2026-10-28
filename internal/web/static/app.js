// RailSwitch dashboard. The server renders every fragment; this script
// only swaps them in from the event stream, and drives keys, dialogs,
// notifications, the palette filter and the colour preview.
"use strict";

const body = document.body;
const prefix = body.dataset.prefix;
const view = body.dataset.view || ""; // "", settings, settingsfile or new
const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];
const sortStore = "railswitch.sort." + prefix;
const projectStore = "railswitch.project";

// Events.

let es = null;
let retry = null;
let failures = 0;

function connect() {
  if (es) es.close();
  clearTimeout(retry);
  // The new page has a prefix only once Create starts its job.
  const q = new URLSearchParams({ p: body.dataset.prefix, w: body.dataset.sel || "", sort: body.dataset.sort || "name", view: view.startsWith("settings") ? "settings" : view });
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
  $$(".out", el).forEach((o) => {
    const job = o.closest(".job");
    if (job) scroll[job.id] = o.scrollTop + o.clientHeight >= o.scrollHeight - 4 ? null : o.scrollTop;
  });
  el.replaceWith(next);
  $$(".out", next).forEach((o) => {
    const job = o.closest(".job");
    const top = job ? scroll[job.id] : null;
    o.scrollTop = top == null ? o.scrollHeight : top;
  });
  afterSwap(id);
}

function afterSwap(id) {
  if (id === "header") {
    const n = Number($("#header").dataset.need || 0);
    document.title = n ? `(${n}) RailSwitch` : "RailSwitch";
    const btn = $("[data-notify]");
    if (btn) btn.hidden = !("Notification" in window) || Notification.permission !== "default";
  } else if (id === "projects") {
    filterSwitcher();
  } else if (id === "jobs") {
    [...watched.keys()].forEach(jobEnded);
  } else if (id === "detail") {
    const slug = $("#detail").dataset.slug || "";
    body.dataset.sel = slug;
    // The worktree shown may not be the URL's (it was removed): follow it.
    if (slug && location.pathname + location.search !== pageURL(slug)) history.replaceState(null, "", pageURL(slug));
  }
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
  const sort = body.dataset.sort;
  return "/p/" + encodeURIComponent(prefix) + "/" + encodeURIComponent(slug) + (sort && sort !== "name" ? "?sort=" + encodeURIComponent(sort) : "");
}

function select(slug, push) {
  $$("#list tbody tr").forEach((tr) => tr.classList.toggle("sel", tr.dataset.slug === slug));
  body.dataset.sel = slug;
  history[push ? "pushState" : "replaceState"](null, "", pageURL(slug));
  connect();
}

function move(delta) {
  const rows = $$("#list tbody tr");
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
    localStorage.setItem(sortStore, new URL(sortLink.href).searchParams.get("sort") || "name");
    return;
  }
  const row = t.closest("#list tbody tr");
  if (row && !t.closest("a.pr") && !e.ctrlKey && !e.metaKey && !e.shiftKey) {
    e.preventDefault();
    select(row.dataset.slug, true);
    return;
  }
  const opener = t.closest("[data-open]");
  if (opener) return openDialog(opener.dataset.open);
  if (t.closest("[data-palette]")) return openPalette();
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
  $("#toast").hidden = true;
});

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
  const remove = f.classList.contains("remove");
  if (remove && f.elements.confirm && f.elements.confirm.value !== f.dataset.slug) {
    return toast("Type " + f.dataset.slug + " to confirm.", true);
  }
  post(f, new URLSearchParams(new FormData(f, e.submitter)));
});

async function post(f, data) {
  const r = await fetch(f.action, {
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

let toastTimer = null;
function toast(text, err) {
  const t = $("#toast");
  t.textContent = text;
  t.classList.toggle("err", !!err);
  t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (t.hidden = true), err ? 8000 : 3000);
}

// Keys.

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
  if (e.key === "Escape") $("#toast").hidden = true;
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
  else if (e.target.matches(".colors-form input[type=color]")) preview(e.target.form);
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

// Task palette: Enter runs the highlighted task as a job, Shift+Enter
// opens it in a terminal.

async function openPalette() {
  const url = $("#detail") && $("#detail").dataset.tasks;
  if (!url || !body.dataset.sel) return toast("No worktree selected.", true);
  const d = await loadDialog("palette", url);
  if (!d) return;
  $(".pal-filter", d).focus();
  filterPalette();
}

function paletteItems() {
  return $$("#palette .pal-list li[data-filter]").filter((li) => !li.hidden);
}

function filterPalette() {
  const q = $("#palette .pal-filter").value.trim().toLowerCase();
  $$("#palette .pal-list li[data-filter]").forEach((li) => (li.hidden = !li.dataset.filter.toLowerCase().includes(q)));
  const items = paletteItems();
  $$("#palette .pal-list li").forEach((li) => li.classList.remove("on"));
  if (items.length) items[0].classList.add("on");
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
  } else if (e.key === "Enter" && e.target.closest("input")) {
    e.preventDefault();
    if (i < 0) return;
    const f = $("#palette form");
    const data = new URLSearchParams(new FormData(f));
    data.set("task", $("button", items[i]).value);
    if (e.shiftKey) data.set("term", "1");
    post(f, data);
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

function preview(f) {
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
  syncLeft();
  if (staged.length) preview();
}

// target is what an edit changes; a later edit of the same target
// replaces the earlier one.
function target(e) {
  if (e.op === "option") return "option " + e.key;
  if (e.op === "env.set" || e.op === "env.unset") return "env " + e.name;
  if (e.op === "link.hide" || e.op === "link.show") return "show " + e.label;
  return e.op + " " + e.label;
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
  $$(".opt").forEach((row) => {
    const e = stagedFor("option " + row.dataset.key);
    const ref = e ? e.ref : row.dataset.ref;
    $$("input[type=radio]", row).forEach((r) => (r.checked = r.value === ref));
    row.classList.toggle("staged", !!e);
    const nw = $(".opt-new", row);
    nw.hidden = !e;
    nw.textContent = e ? "→ " + e.ref : "";
  });
  $$("tr.env").forEach((row) => {
    const e = stagedFor("env " + row.dataset.name);
    const input = $(".env-value", row);
    if (document.activeElement !== input) input.value = e && e.op === "env.set" ? e.value : row.dataset.value;
    input.disabled = !!e && e.op === "env.unset";
    row.classList.toggle("staged", !!e && e.op === "env.set");
    row.classList.toggle("removed", !!e && e.op === "env.unset");
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
  done(el.dataset.state, el.hasAttribute("data-conflict"));
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

// Add buttons wait for their required fields.
function addReady(f) {
  $("button", f).disabled = $$("input[required]", f).some((i) => !i.value.trim());
}

if (view.startsWith("settings")) {
  document.addEventListener("click", (e) => {
    const t = e.target;
    const change = t.closest("[data-change]");
    if (change) {
      const c = $(".choices", change.closest(".opt"));
      c.hidden = !c.hidden;
      return;
    }
    const undo = t.closest("[data-undo]");
    if (undo) {
      staged.splice(Number(undo.dataset.undo), 1);
      stagedChanged();
      return;
    }
    const envRm = t.closest("[data-env-remove]");
    if (envRm) return stage({ op: "env.unset", name: envRm.closest("tr").dataset.name }, false);
    const linkRm = t.closest("[data-link-remove]");
    if (linkRm) stage({ op: "link.remove", label: linkRm.closest("tr").dataset.label }, false);
  });

  document.addEventListener("change", (e) => {
    const t = e.target;
    const opt = t.closest(".opt");
    if (opt && t.type === "radio") return stage({ op: "option", key: opt.dataset.key, ref: t.value }, t.value === opt.dataset.ref);
    if (t.classList.contains("env-value")) {
      const row = t.closest("tr");
      return stage({ op: "env.set", name: row.dataset.name, value: t.value }, t.value === row.dataset.value);
    }
    if (t.classList.contains("link-show")) {
      const row = t.closest("tr");
      return stage({ op: t.checked ? "link.show" : "link.hide", label: row.dataset.label }, t.checked === (row.dataset.hidden !== "true"));
    }
    if (t.name === "restart" && t.closest("#staged")) restartWanted = t.checked;
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
      } else {
        f.reset(); // the value leaves the page at once
        startJob(f.action, data, secretDone);
      }
    }
  });
}

// New project: Read the repository posts repo and branch (with the
// picked pack, when one is picked) to /new/detect and swaps in the Stack
// card; Next: review posts the whole form to /new/review and swaps in
// step 2; Create starts project new as a job of the new prefix and
// follows it on that prefix's stream. Any change after a review marks it
// stale and turns Create off until the next review.

function startNew() {
  const form = $("#new-form");
  const el = form.elements;
  let extras = []; // Add service rows, {key, ref}, kept across reads
  let reviewed = false;
  let dirAuto = true; // the folder follows the prefix until edited
  const autoDir = () => (el.prefix.value.trim() ? body.dataset.home + "/workspace/" + el.prefix.value.trim() : "");

  function ready() {
    $("[data-detect]", form).disabled = !el.repo.value.trim();
    $("[data-review]", form).disabled = !($("#new-stack").dataset.pack && el.prefix.value.trim() && el.dir.value.trim());
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

  document.addEventListener("submit", async (e) => {
    const f = e.target;
    if (!f.matches("form[data-create]")) return;
    e.preventDefault();
    const btn = $("button", f);
    const data = new URLSearchParams(new FormData(form));
    if (f.elements.up && f.elements.up.checked) data.set("up", "1");
    const p = el.prefix.value.trim();
    body.dataset.prefix = p;
    connect();
    btn.disabled = true;
    const started = await startJob(f.action, data, (state) => {
      if (state === "ok") {
        location.href = "/p/" + encodeURIComponent(p);
        return;
      }
      toast("project new " + state + ". The form keeps your values; the job's output says what was left and how to remove it.", true);
      btn.disabled = false;
    });
    if (!started) btn.disabled = false;
  });

  ready();
}

// Start: the remembered project and sort, then the stream.

(function start() {
  if (view === "new") {
    afterSwap("header");
    startNew();
    return;
  }
  if (!prefix) return;
  if (view) {
    afterSwap("header");
    if (view === "settings") startSettings();
    connect();
    return;
  }
  const nav = performance.getEntriesByType("navigation")[0];
  const last = localStorage.getItem(projectStore);
  if (nav && nav.redirectCount > 0 && last && last !== prefix && $$("#projects .proj-item").some((li) => li.dataset.filter === last)) {
    location.replace("/p/" + encodeURIComponent(last));
    return;
  }
  localStorage.setItem(projectStore, prefix);
  const params = new URLSearchParams(location.search);
  const saved = localStorage.getItem(sortStore);
  if (!params.has("sort") && saved && saved !== "name") {
    params.set("sort", saved);
    location.replace(location.pathname + "?" + params);
    return;
  }
  afterSwap("header");
  connect();
})();

