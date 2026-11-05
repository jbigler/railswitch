// Tests of the dashboard's page-free logic (static/logic.js), run by
// `node --test internal/web/jstest/` in make check: no packages, no browser.
// Outside static/ so the binary doesn't carry them.
import { test } from "node:test";
import assert from "node:assert/strict";
import { fuzzy, target, hookLines, addTaskEdits, taskWay } from "../static/logic.js";

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
