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

**Whiteboard**:
A worktree's one Excalidraw drawing, kept on the host with a dark and a light picture of it, shown by the Virtual Office's glass wall and the worktree page's Whiteboard tab, and deleted with the worktree.
_Avoid_: board (alone), canvas, sketch

### Notes

**Note**:
A Markdown file in the workspace's shared notes, which every agent reads and only the user writes. Its **title** is its file name's words ("Agent queue design" for `agent_queue_design.md`).
_Avoid_: memo, doc, memory

**Suggested note**:
A note an agent proposes from a worktree, which the user accepts into the notes or rejects.
_Avoid_: draft, inbox item
