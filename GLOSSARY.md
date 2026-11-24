# railswitch

railswitch runs Rails development environments on rootless podman. This glossary fixes the words the project uses for its own concepts.

## Language

### Dashboard

**Read command**:
A railswitch command that changes nothing and can print its result as one JSON document.
_Avoid_: query, getter

**Action**:
A railswitch command the dashboard starts on the user's behalf that changes something, such as starting a worktree.
_Avoid_: mutation, operation

**Dashboard contract**:
The set of read commands and actions the dashboard depends on, with the exact arguments it passes and the JSON fields it reads from each.
_Avoid_: dashboard API, JSON schema

**Welcome page**:
The dashboard's page at `/` while no project is registered: the machine checks and the two ways to start a first project.
_Avoid_: onboarding, wizard, setup page

**Machine checks**:
`doctor`'s host checks as the dashboard shows them, each failure or warning with its fix.
_Avoid_: health check, diagnostics

**Whiteboard**:
A worktree's one Excalidraw drawing, kept on the host with a dark and a light picture of it, shown by the Virtual Office's glass wall and the worktree page's Whiteboard tab, and deleted with the worktree.
_Avoid_: board (alone), canvas, sketch

### Worktrees

**Services**:
The containers a worktree runs, such as its app, database and agents. The worktree has their combined state: a **running** (`up`), **stopped**, **partial** or **failed** worktree.
_Avoid_: stack, environment, runtime

**Lock file**:
`stack.lock`, the project's record of the packages it accepted and their exact content. Its filename is fixed, like `project.toml`'s `[stack]` table and a template's `stack.toml`; the packages themselves are never called a stack.
_Avoid_: the stack (for the chosen packages)

### Branches

**Stack**:
A chain of branches, each built on the one below, that gh stack tracks and GitHub shows as one stack of pull requests. "Stack" means only this.
_Avoid_: stack for a worktree's services, branch stack, PR chain

**Upstream gone**:
A local branch whose remote branch on origin no longer exists, usually because its pull request was merged and GitHub deleted the branch.
_Avoid_: orphan branch, stale branch

### Notes

**Note**:
A Markdown file in the workspace's shared notes, which every agent reads and only the user writes. Its **title** is its file name's words ("Agent queue design" for `agent_queue_design.md`).
_Avoid_: memo, doc, memory

**Suggested note**:
A note an agent proposes from a worktree, which the user accepts into the notes or rejects.
_Avoid_: draft, inbox item
