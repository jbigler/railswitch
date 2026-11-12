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
