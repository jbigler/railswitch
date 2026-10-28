# Stage 8d acceptance: templates bundle their local packages

Host run on 2026-10-29 with the branch build installed, on Maybe; a
teammate's machine simulated with an empty `XDG_CONFIG_HOME` and
`XDG_DATA_HOME`.

| Check | Result |
|---|---|
| Maybe on `local:rails@1` (`pack eject rails@1`, `filial.example` added to its `deps.allow`, the pack picked with a Settings `pack` edit) | PASS |
| `template export --name maybe-own --to <dir>`: refused while an older `local:rails@1` remembered in `[[removed]]` waited for review; after `lock --accept`: "bundles local:rails@1 (packs/rails/)", the folder holds `stack.toml` and `packs/rails/` | PASS (the refusal is 7a working: a ref back from `[[removed]]` is locked at its old content) |
| Teammate: `template import <dir>` reviews `packs/rails/` with the pack checks (manifest image args, `FROM`, `deps.allow` hosts flagged) | PASS, after a new bundle was shown as its changes from its closest built-in: the first cut listed every file of the pack as added (16 flags for one changed line) |
| `--accept`; `template ls`: `maybe-own@1 machine local:rails@1 …`; `project new --template maybe-own@1 --dry-run`: `pack = "local:rails@1"`, no warnings | PASS |

The real create from a bundled template is covered by
`TestNewTemplateBundled` (the pack written as `packs/rails/`, scripts
0755, locked `source = "local"`, its host in the rendered firewall list).

## Cleanup

Maybe back on `rails@1` through a Settings `pack` edit (`project.toml`
byte for byte the original), `packs/` removed; `render --check` 0,
nothing pending, no `filial` host in the firewall list.
