# Stage 8b acceptance: sharing packages

Host run on 2026-10-29 (podman 6.1.4, Omarchy) with the branch build
installed, on Maybe (the Library page driven with Playwright, headless
Chromium).

## Export and a new package

| Check | Result |
|---|---|
| `pack export rails@1 --to <dir>`; `filial.example` added to the folder's `deps.allow` | PASS |
| `pack import <dir> --as filial-rails`: new, shown as its changes from the built-in `rails@1`: `deps.allow` (1 firewall flag) and the manifest's name line | PASS, after the first cut showed every file as added (15 flags burying the one change) |
| Switching Maybe to `local:filial-rails@1` | Refused: `sidekiq@1 does not work with pack filial-rails`. Options name the packs they work with, so a shared pack keeps its name; documented |
| `pack import <dir>` (as `local:rails@1`), accepted; `project write` with `pack = "local:rails@1"`: locked `source = "local"`, `filial.example` in the rendered firewall list, nothing pending | PASS (Settings has no pack control: the project.toml editor does it; documented) |

## An update of a used package (the Library page)

| Check | Result |
|---|---|
| The folder gains `api.filial.example`; Import a package: "local:rails@1 replaces local:rails@1", the diff from the workspace's copy, 1 flagged; Import off until ticked | PASS |
| Import: the job writes, locks and renders; the page returns to Library; `stack.lock` at the reviewed hash, nothing pending, both hosts in the firewall list | PASS |
| Export on the package's pane writes the folder with both hosts | PASS |

## Cleanup

Maybe's `project.toml` written back with `project write` (byte for byte
the original), `packs/` removed; `render --check` 0, no `filial` host in
the firewall list. `stack.lock` keeps the same package hashes; it now also
locks the agent base (adopted on any lock since 7c) and remembers the
removed local pack in `[[removed]]`.
