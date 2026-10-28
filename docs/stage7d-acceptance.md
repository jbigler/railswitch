# Stage 7d acceptance: templates

Host run on 2026-10-29 (podman 6.1.4, OpenSSH 10.6p1, Omarchy) with the
branch build installed, the New project page driven with Playwright
(headless Chromium). Plausible was stopped during the create to leave RAM
for it, and started again afterwards (200).

## Export

| Check | Result |
|---|---|
| `template export --name maybe-stack --version 1` in Maybe: `[project]` name, prefix, repo and base_branch dropped; questions for each slot (database, cache, storage, jobs, mail, browser) and each pin (postgres, redis, rustfs); the secrets `claude_token` and `github_token` declared with no value | PASS |
| `template ls`: `maybe-stack@1`, source machine, its packages | PASS |

## Start a project from it (the page)

| Check | Result |
|---|---|
| New project: "Start from a template" lists `maybe-stack@1`; choosing it shows its questions with their defaults | PASS |
| `versions.redis` answered `7`; Next: review shows "From the template" with every answer (redis 7, "default 8"), the routes and containers | PASS |
| Create (Start ticked): the project `maybetwo` is created and started; `/sessions/new` 200; the cache runs `redis:7-alpine` (the answer, not the default); `project.toml` records `[template] ref = "maybe-stack@1"` and its sha256 | PASS |
| `template check`: "unchanged since it was accepted"; `overview` `template_update: false` | PASS |

## A template from a signed library

| Check | Result |
|---|---|
| A library with `templates/railsy/` (a copy of the export), indexed and signed; `library add`, `library trust`: `template ls` lists `tpl:railsy@1`, source library tpl | PASS |
| `project new --template tpl:railsy@1 --dry-run`: `template` set, `install` lists `tpl:railsy@1` kind `template` status `added` | PASS |
| The real create without `--accept-install`: refused ("installing template tpl:railsy@1 needs review"); nothing cloned | PASS |

## Cleanup

`project rm maybetwo --purge --yes`, its workspace folder deleted,
`library rm tpl`, the exported template removed, the test entry removed
from `signers.toml` by hand.

## Known limits

- The machine store holds one version per template name (the latest
  export): after exporting `maybe-stack@2`, a project made from `@1` gets
  "its stack.toml declares maybe-stack@2" from `template check`, not an
  update. Template updates are a library feature.
