# Stage 8c acceptance: the pack in Settings

Host run on 2026-10-29 with the branch build installed, on Maybe (the
Settings page driven with Playwright, headless Chromium).

| Check | Result |
|---|---|
| `pack eject rails@1`: `project show --json` `pack` is `{ref: rails@1, choices: [local:rails@1, rails@1]}` | PASS |
| Settings: the pack row is first in Stack and options; Change → `local:rails@1` stages "Pack: rails@1 → local:rails@1", "No data affected" | PASS |
| Apply: `project.toml` `pack = "local:rails@1"`, `stack.lock` pack entry `source = "local"`, `render --check` 0 | PASS |
| Back to `rails@1` the same way: `project.toml` byte for byte the original; `packs/` removed; `render --check` 0, nothing pending | PASS |
