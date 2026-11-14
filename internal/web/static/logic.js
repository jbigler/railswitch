// The dashboard's logic that needs no page: app.js imports it, and
// internal/web/jstest tests it with `node --test` (make check).

// fuzzy scores query q against s (both lower case): q's letters in order,
// spaces ignored ("pgset" finds "page settings"); more for a letter that
// starts a word or follows the last one, less for a gap; -1 for no match.
export function fuzzy(q, s) {
  let score = 0;
  let from = 0;
  let last = -2;
  for (const c of q.replace(/\s+/g, "")) {
    const k = s.indexOf(c, from);
    if (k < 0) return -1;
    score += (k === last + 1 ? 2 : 0) + (k === 0 || /[\s_./:-]/.test(s[k - 1]) ? 3 : 0) - Math.min(k - from, 10) * 0.1;
    last = k;
    from = k + 1;
  }
  return score;
}

// target is what a staged settings edit changes; a later edit of the same
// target replaces the earlier one.
export function target(e) {
  if (e.op === "pack") return "pack";
  if (e.op === "firewall.mode") return "fwmode";
  if (e.op === "firewall.allow.add" || e.op === "firewall.allow.remove") return "fw " + e.key + " " + e.value;
  if (e.op === "option") return "option " + e.key;
  if (e.op === "env.set" || e.op === "env.unset") return "env " + e.name;
  if (e.op === "task.set" || e.op === "task.unset") return "task " + e.name;
  if (e.op === "task.fresh.add" || e.op === "task.fresh.remove") return "fresh " + e.name;
  if (e.op === "task.terminal.add" || e.op === "task.terminal.remove") return "terminal " + e.name;
  if (e.op === "link.hide" || e.op === "link.show") return "show " + e.label;
  if (e.op === "hooks.set") return "hooks " + e.key;
  if (e.op === "agent.setup") return "agent-setup " + e.key;
  if (e.op === "agent.default") return "agent-default";
  return e.op + " " + e.label;
}

// hookLines is a Hooks box's commands: one per line, blank lines left out.
export function hookLines(text) {
  return text.split("\n").map((l) => l.trim()).filter((l) => l !== "");
}

// addTaskEdits are the Tasks card's add form as staged edits: the task
// first, since preview judges edits in order and a fresh_tasks or
// terminal_tasks name with no task yet is a render error.
export function addTaskEdits(name, value, fresh, terminal) {
  const edits = [{ op: "task.set", name, value }];
  if (fresh) edits.push({ op: "task.fresh.add", name });
  if (terminal) edits.push({ op: "task.terminal.add", name });
  return edits;
}

// taskWay is how the palette runs a task: Shift, a desktop window; a
// terminal_tasks task or the ticked box, a terminal panel; else a job.
export function taskWay({ shift, terminal, panel }) {
  if (shift) return "window";
  return terminal || panel ? "panel" : "job";
}

// pageQuery is a project page's query for its sort and usage range
// (D187): "" for the defaults (name, 14 days), keys in Go's order.
export function pageQuery(sort, days) {
  const q = new URLSearchParams();
  if (days && String(days) !== "14") q.set("days", String(days));
  if (sort && sort !== "name") q.set("sort", sort);
  const s = q.toString();
  return s ? "?" + s : "";
}

// termShown (D191, D205): a terminal panel shows only while its worktree
// is the one shown: an agent's on the Agent tab, any other on Terminals.
export function termShown(panel, slug, tab) {
  return slug !== "" && panel.slug === slug && tab === (panel.agent ? "agent" : "terminals");
}

// termCloses (#224) says whether a terminal panel closes by itself when
// its program exits with code: a shell always; an agent or an editor that
// ended well; a task never, since its output is what you ran it for. A
// failed agent or editor stays, so what it printed last can be read.
export function termCloses(url, code) {
  const kind = new URL(url, "http://x").pathname.split("/").pop();
  return kind === "shell" || (kind !== "task" && code === 0);
}

// termFont (#167) is the terminal font a browser chose, safe to put in a
// CSS font-family list: letters, digits, spaces and - _ only; "" for none.
export function termFont(name) {
  name = String(name || "").trim();
  return /^[A-Za-z0-9 _-]{1,60}$/.test(name) ? name : "";
}

// termID names a terminal by its URL, however its query is written: the
// server's list and a button spell the same one differently.
export function termID(url) {
  const u = new URL(url, "http://x");
  u.searchParams.sort();
  return u.pathname + u.search;
}

// settingsTab (D201) is the tab a tabbed page (Settings, Library) shows: the one the address's
// #hash names (a card's old id counts), else the one kept for the session,
// else the first.
export function settingsTab(hash, kept, tabs) {
  const alias = { hooks: "commands", "agent-setup": "commands", import: "add" };
  for (const t of [alias[hash] || hash, kept]) {
    if (tabs.includes(t)) return t;
  }
  return tabs[0];
}

// noteFileName is the file name a note's typed title makes (Go's
// noteFileName, which decides; this one shows it while you type):
// "Agent queue design" is "agent_queue_design.md"; a file name stays.
export function noteFileName(title) {
  title = title.trim();
  if (/^[A-Za-z0-9][A-Za-z0-9._-]{0,99}\.md$/.test(title)) return title;
  const name = title.replace(/\.md$/, "").toLowerCase().split(/[^a-z0-9]+/).filter(Boolean).join("_");
  return name ? name.slice(0, 100).replace(/_+$/, "") + ".md" : "";
}
