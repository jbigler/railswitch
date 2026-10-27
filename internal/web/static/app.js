// RailSwitch dashboard. The server renders every fragment; this script
// only swaps them in from the event stream, and drives keys, dialogs,
// notifications, the palette filter and the colour preview.
"use strict";

const body = document.body;
const prefix = body.dataset.prefix;
const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];
const sortStore = "railswitch.sort." + prefix;
const projectStore = "railswitch.project";

// Events.

let es = null;
let retry = null;

function connect() {
  if (es) es.close();
  clearTimeout(retry);
  const q = new URLSearchParams({ p: prefix, w: body.dataset.sel || "", sort: body.dataset.sort || "name" });
  es = new EventSource("/events?" + q);
  es.onmessage = (m) => handle(JSON.parse(m.data));
  es.onerror = () => {
    // The browser retries a dropped stream itself; a refused one is
    // closed, so try again later (after a restart, say).
    if (es.readyState === EventSource.CLOSED) retry = setTimeout(connect, 3000);
  };
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
  } else if (id === "detail") {
    body.dataset.sel = $("#detail").dataset.slug || "";
  }
}

// Notifications.

function notify(ev) {
  if (!("Notification" in window) || Notification.permission !== "granted") return;
  const n = new Notification(ev.title, { body: ev.body, tag: ev.tag });
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

// Start: the remembered project and sort, then the stream.

(function start() {
  if (!prefix) return;
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

