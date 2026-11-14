// Tests of the dashboard's page-free logic (static/logic.js), run by
// `node --test internal/web/jstest/*.test.js` in make check: no packages, no browser.
// Outside static/ so the binary doesn't carry them.
import { test } from "node:test";
import assert from "node:assert/strict";
import { fuzzy, target, hookLines, addTaskEdits, taskWay, pageQuery, termShown, termCloses, termFont, termID, settingsTab, noteFileName } from "../static/logic.js";

test("settingsTab: the hash wins, then the kept tab, then the first", () => {
  const tabs = ["stack", "firewall", "commands", "tasks"];
  assert.equal(settingsTab("tasks", "firewall", tabs), "tasks");
  assert.equal(settingsTab("agent-setup", "", tabs), "commands", "a card's old id");
  assert.equal(settingsTab("template", "firewall", tabs), "firewall", "a hash that is no tab");
  assert.equal(settingsTab("", "gone", tabs), "stack");
  assert.equal(settingsTab("import", "", ["packages", "add"]), "add", "the Library's Import link");
});

test("fuzzy matches letters in order, spaces ignored", () => {
  assert.ok(fuzzy("pgset", "page settings") > 0);
  assert.equal(fuzzy("xyz", "page settings"), -1);
  assert.equal(fuzzy("sp", "page settings"), -1, "out of order");
  assert.equal(fuzzy("", "anything"), 0);
});

test("fuzzy ranks word starts and runs above scattered letters", () => {
  assert.ok(fuzzy("watch", "test:watcher:rails") > fuzzy("watch", "wxaxtxcxh"), "a run beats scattered letters");
  assert.ok(fuzzy("set", "page settings") > fuzzy("set", "pageasxexxt"), "a word start beats mid-word letters");
});

test("target: a later edit of the same thing replaces the earlier", () => {
  assert.equal(target({ op: "task.set", name: "x" }), target({ op: "task.unset", name: "x" }));
  assert.notEqual(target({ op: "task.set", name: "x" }), target({ op: "task.fresh.add", name: "x" }));
  assert.equal(target({ op: "task.fresh.add", name: "x" }), target({ op: "task.fresh.remove", name: "x" }));
  assert.equal(target({ op: "task.terminal.add", name: "x" }), target({ op: "task.terminal.remove", name: "x" }));
  assert.notEqual(target({ op: "env.set", name: "A" }), target({ op: "env.set", name: "B" }));
  assert.equal(target({ op: "firewall.allow.add", key: "app", value: "h" }), "fw app h");
});

test("hookLines: one command per line, blanks dropped", () => {
  assert.deepEqual(hookLines("  a \n\n b\n  \n"), ["a", "b"]);
  assert.deepEqual(hookLines(""), []);
});

// The bug of 2026-11-05: New container staged before its task was blocked.
test("addTaskEdits stages the task before its fresh and terminal settings", () => {
  assert.deepEqual(addTaskEdits("w", "retest --rails", true, true).map((e) => e.op), ["task.set", "task.fresh.add", "task.terminal.add"]);
  assert.deepEqual(addTaskEdits("w", "x", false, false), [{ op: "task.set", name: "w", value: "x" }]);
  assert.deepEqual(addTaskEdits("w", "x", false, true).map((e) => e.op), ["task.set", "task.terminal.add"]);
});

test("taskWay: Shift is a window; a terminal task or the box a panel; else a job", () => {
  assert.equal(taskWay({ shift: true, terminal: true, panel: true }), "window");
  assert.equal(taskWay({ shift: false, terminal: true, panel: false }), "panel", "a terminal task ignores an unticked box");
  assert.equal(taskWay({ shift: false, terminal: false, panel: true }), "panel");
  assert.equal(taskWay({ shift: false, terminal: false, panel: false }), "job");
});

test("pageQuery: the defaults leave the URL bare, keys in Go's order", () => {
  assert.equal(pageQuery("name", "14"), "");
  assert.equal(pageQuery("", ""), "");
  assert.equal(pageQuery("-pr", "14"), "?sort=-pr");
  assert.equal(pageQuery("name", "30"), "?days=30");
  assert.equal(pageQuery("pr", 7), "?days=7&sort=pr");
});

test("termShown: a panel shows only in its worktree, an agent's on the Agent tab, another on Terminals", () => {
  assert.ok(termShown({ slug: "a", agent: false }, "a", "terminals"));
  assert.ok(!termShown({ slug: "a", agent: false }, "a", "overview"), "a shell off the Terminals tab hides");
  assert.ok(!termShown({ slug: "a", agent: false }, "b", "terminals"), "another worktree's panel hides");
  assert.ok(termShown({ slug: "a", agent: true }, "a", "agent"));
  assert.ok(!termShown({ slug: "a", agent: true }, "a", "terminals"), "an agent panel off the Agent tab hides");
  assert.ok(!termShown({ slug: "a", agent: true }, "b", "agent"), "another worktree's agent panel hides");
  assert.ok(!termShown({ slug: "", agent: false }, "", "terminals"), "no worktree shown");
});

test("termID: one terminal, however its query is written", () => {
  assert.equal(termID("/p/a/tty/b/task?task=t&args=a%20b"), termID("/p/a/tty/b/task?args=a+b&task=t"));
  assert.notEqual(termID("/p/a/tty/b/task?task=t&args=a"), termID("/p/a/tty/b/task?task=t"));
  assert.equal(termID("/p/a/tty/b/shell"), "/p/a/tty/b/shell");
});

test("noteFileName: a title is a snake-case file name, a file name stays", () => {
  assert.equal(noteFileName("Agent queue design"), "agent_queue_design.md");
  assert.equal(noteFileName("  what's next?\n"), "what_s_next.md");
  assert.equal(noteFileName("Keep_This-Name.md"), "Keep_This-Name.md");
  assert.equal(noteFileName("!!"), "");
  assert.equal(noteFileName("a".repeat(99) + " b c"), "a".repeat(99) + ".md");
});

test("termCloses: a shell typed into, anything else that ended well, a task never", () => {
  assert.ok(termCloses("/p/a/tty/b/shell", 130, true), "exit after a failed command");
  assert.ok(termCloses("/p/a/tty/b/shell", 0, false));
  assert.ok(!termCloses("/p/a/tty/b/shell", 1, false), "a shell that could not start stays to be read");
  assert.ok(termCloses("/p/a/tty/b/agent?agent=x", 0));
  assert.ok(!termCloses("/p/a/tty/b/agent?agent=x", 1), "a failed agent stays to be read");
  assert.ok(termCloses("/p/a/tty/b/nvim", 0));
  assert.ok(!termCloses("/p/a/tty/b/task?task=t", 0), "a task's output stays");
});

test("termFont: a plain font name, or none", () => {
  assert.equal(termFont("  Fira Code "), "Fira Code");
  assert.equal(termFont('x", serif; }'), "");
  assert.equal(termFont(""), "");
});
