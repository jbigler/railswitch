# Stage 8a acceptance: sharing templates

Host run on 2026-10-29 (podman 6.1.4, Omarchy) with the branch build
installed. The Machine page was driven with Playwright (headless
Chromium). A teammate's machine was simulated with an empty
`XDG_CONFIG_HOME` and `XDG_DATA_HOME`.

## Export without personal choices (D112)

| Check | Result |
|---|---|
| `template export --name maybe-stack --to <dir>` in Maybe: `[agents.claude]` dropped "(each person's own)"; `claude_token` and `github_token` dropped (only the agent received them); `[project]` lines "(project-only)"; `stack.toml` has no `[tools]`, `[agents]` or agent service | PASS, after the labels were fixed (the first cut called every dropped line "project-only") |

## Import as a teammate (CLI, D113)

| Check | Result |
|---|---|
| `template import <dir>`: "new on this machine", every file shown as added, 0 flagged, the accept command printed; nothing saved (`template ls` empty) | PASS |
| `--accept <hash>`: saved into the store; `template ls` lists `maybe-stack@1`; a second review: "already on this machine" | PASS |
| `project new --template maybe-stack@1 --tool nvim --dry-run`: the teammate's `[tools.nvim]` and the default `[agents.claude]`, nothing of the author's | PASS |

## Import on the Machine page

| Check | Result |
|---|---|
| New project's template picker links to the Machine page; the Templates section lists the store and has the folder field | PASS |
| Review of a new folder; Import runs the job, the page returns to Machine, the template is listed | PASS, after the import page got its own view (`import`): as a `review` view it subscribed to a project fragment for `~machine` (404s), so the job's end never arrived and the page didn't move on |
| A version 2 folder with `[firewall] mode = "off"`: "replaces maybe-stack@1", the diff from the stored version, 1 flagged; Import off until "I've read the 1 flagged item" is ticked | PASS |
| A version 3 (README added): imported; Machine lists `maybe-stack@3` | PASS |

## Cleanup

The test template was removed from the store
(`~/.config/railswitch/templates/maybe-stack.stack`). All four projects:
`render --check` 0.
