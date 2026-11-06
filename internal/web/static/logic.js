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
