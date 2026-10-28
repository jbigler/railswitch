# Stage 6b project settings Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Edit a project's options, env, links and secrets from the dashboard, staged with data-impact labels and applied all or none, plus a raw `project.toml` editor.

**Architecture:** New text-level edit ops in `internal/config`; a new `internal/settings` package judges edits by rendering the current and the edited project in memory and comparing volumes, app env and unit hashes; CLI `settings preview|apply`, `project show|write`; `overview` gains `stale`; the dashboard gets a settings page whose jobs can carry stdin.

**Tech Stack:** Go stdlib + `github.com/BurntSushi/toml`; `html/template`, one vanilla JS file.

**Spec:** `docs/superpowers/specs/2026-10-28-railswitch-stage-6b-settings-design.md`

## Global Constraints

- `make check` before every commit: `gofmt -l` empty, `go vet ./...`, `staticcheck ./...`, `go test ./...`.
- Dependencies: stdlib + `github.com/BurntSushi/toml` only.
- Commands: argv only, through `sys.Runner`; unit tests need no podman, systemd, gh or network (use `internal/sys/systest` Fake).
- Secrets: values only on stdin, never argv, files railswitch writes, logs, job output, responses or SSE.
- Genericity: no package name (`postgres`, `redis`, …) in non-test core code (`TestGenericity`).
- The dashboard reads only through `railswitch --json`, changes only through `railswitch` jobs; no inline script or style; every POST passes the guard.
- Every behaviour change updates its doc (`docs/json.md`, `docs/dashboard.md`, `docs/packs.md`, `AGENTS.md`).
- `project.toml` edits keep every other byte; results must `Load` and `Validate` before a write; writes are temp + rename (`tomlfile.WriteAtomic`).

## Review Focus

- A `project.toml` with comments and blank lines around the edited key: comments survive, the edit lands in the right table (Task 1 fixture has comments on every table).
- A slot set to `"none"` that another option needs (sidekiq without cache): blocked with the resolve error, not a crash (Task 3).
- `project.toml` edited by hand between preview and apply: refused by `--if-hash`, nothing written (Task 4).
- A secret value containing newlines or shell metacharacters: reaches stdin byte-exact and appears nowhere else (Task 7).
- Env value or link URL with quotes, backslashes or `{placeholders}`: written as a valid TOML string and round-trips (Task 1).

---

### Task 1: Text-level edit ops in `internal/config`

**Files:**
- Modify: `internal/config/edit.go`
- Test: `internal/config/edit_test.go`

**Interfaces:**
- Produces (all in package `config`, pure functions on file text; they do not validate):
  - `func SetOption(data, key, ref string, slot bool) (string, error)` — slot: set or insert `key = "ref"` in `[options]` (ref may be `"none"`); non-slot with ref `"none"`: remove the key line; else set or insert.
  - `func SetEnv(data, name, value string) (string, error)`, `func UnsetEnv(data, name string) (string, error)` — project `[env]`; unset of an absent name is an error.
  - `func AddLink(data, label, url string) (string, error)` — appends a `[[links]]` table (`label`, `url`); duplicate project label is an error.
  - `func RemoveLink(data, label string) (string, error)` — removes that `[[links]]` table's statements (header through its last key) and nothing else.
  - `func HideLink(data, label string) (string, error)`, `func ShowLink(data, label string) (string, error)` — append to / remove from top-level `hide_links`; hide of a hidden label or show of a shown one is an error.
  - `type BlockedError struct{ Line int; Msg string }` with `Error()` = `"project.toml:<line>: <msg>: edit project.toml directly"`; returned for shapes the line editor cannot edit (inline table, dotted key, the target table absent but a parent inline table present).
- Internally add to `doc`: `setValue(s *stmt, value string) string`, `removeStmt(s *stmt) string`, `removeArrayElem(s *stmt, elem string) (string, error)`.

- [ ] **Step 1: Write failing tests** `TestSetOptionKeepsComments`, `TestSetOptionNoneSlotAndExtra`, `TestEnvSetUnset`, `TestAddRemoveLink`, `TestHideShowLink`, `TestEditBlockedShapes`, `TestEditQuotesValues`. Fixture: a project.toml with a comment line above and after each of `[options]`, `[env]`, two `[[links]]`, and `hide_links`. Assert: result `decode`s and `Validate`s; the bytes outside the edited statement are identical (compare with the fixture with the statement cut out); `SetEnv(d,"X","a\"b\\c{x}")` round-trips to the same Go string; `[env] X = 1` inline as `env = { X = "1" }` → `*BlockedError` naming line N.
- [ ] **Step 2:** `go test ./internal/config/ -run 'Option|Env|Link|Blocked|Quotes'` → FAIL (undefined).
- [ ] **Step 3:** Implement on the existing `parseDoc`/`find`/`insertKey`/`appendTable`/`appendToArray`; values via `QuoteString`.
- [ ] **Step 4:** Tests pass; `make check`.
- [ ] **Step 5:** Commit `config: line-level edits for options, env and links`.

### Task 2: `data = "cache"` on package volumes

**Files:**
- Modify: `internal/pack/format.go` (`Volume`), `internal/pack/load.go` (validate), `internal/render/model.go` (`render.Volume` carries it), `docs/packs.md`
- Test: `internal/pack/load_test.go`

**Interfaces:**
- Produces: `pack.Volume.Data string` (`toml:"data"`; "" or "persistent" = persistent, "cache"); `render.Volume.Cache bool`.

- [ ] **Step 1:** `TestVolumeData`: a local option with `data = "cache"` loads and its render volume has `Cache` true; `data = "temp"` fails to load with `data must be "persistent" or "cache"`.
- [ ] **Step 2:** Run → FAIL.
- [ ] **Step 3:** Implement; document in packs.md manifest tables.
- [ ] **Step 4:** Pass; `make check`.
- [ ] **Step 5:** Commit `pack: volumes declare data = "cache"`.

### Task 3: `internal/settings`: edits, preview judgement

**Files:**
- Create: `internal/settings/settings.go`, `internal/settings/judge.go`
- Modify: `internal/workspace/workspace.go`
- Test: `internal/settings/settings_test.go` (Filial fixture via the existing test helpers in `internal/cli`/`internal/workspace` testdata)

**Interfaces:**
- Consumes: Task 1 ops; Task 2 `render.Volume.Cache`.
- Produces:
  - `workspace.(*Workspace).WithProject(p *config.Project) (*Workspace, error)` — same Root/Catalog/Dirs/Slug, `Stack` resolved from p, `Lock` = in-memory lock from `lockEntries` keeping the current lock's digests.
  - `workspace.Hash(data []byte) string` → `"sha256:<hex>"`.
  - `type settings.Edit struct{ Op, Key, Ref, Name, Value, Label, URL string }`; `func settings.ParseEdits(r io.Reader) ([]Edit, error)` (strict: `DisallowUnknownFields`, unknown op error, required fields per op).
  - `func settings.ApplyText(data string, slots []string, e Edit) (string, error)` — dispatch to Task 1.
  - `type Impact string` = `"none" | "check" | "data" | "blocked"`.
  - `type EditResult struct{ Index int; Impact Impact; Reasons []string; Error string }` (json tags `index`, `impact`, `reasons`, `error`).
  - `type Preview struct{ Hash, Diff string; Edits []EditResult; Restart []string; NewText string }` (json: `hash`, `diff`, `edits`, `restart`; `NewText` `json:"-"`).
  - `type Env struct{ WS *workspace.Workspace; Reg *machine.Registry; Host render.Host; Running func(unit string) bool }`.
  - `func (e Env) Preview(data []byte, edits []Edit) (*Preview, error)`.
- Rules (spec "settings preview"): edit i is judged with edits 0…i-1 applied; the model for "current" is built once; per registered worktree compare (a) volume names from `VolumeSpecs` over every service, skipping `Cache`; (b) the app service's env names/values (`Instance.Env[app]`); restart = worktrees with `Running(<prefix>-<slug>-<app>.service)` whose stamped unit text from `Plan().Units` differs for any of its units. Reasons: `"<vol> is left behind; <vol> starts empty"`, `"run railswitch db dump first"` when a removed service's option has `[dump]`, `"<NAME> is no longer given to <app>"`, `"<NAME> changes for <app>"`. Diff: a minimal unified diff (write `unifiedDiff(a, b string) string` line-based, LCS; files are small).

- [ ] **Step 1:** Tests `TestPreviewRedisNone` (check, `REDIS_URL`), `TestPreviewSidekiqBlocks` (cache none with sidekiq → blocked, error mentions the resolve message), `TestPreviewPinChangeData` (postgres pin bump → data, reasons name `db-data-pg16` and the dump hint), `TestPreviewCacheVolume` (local option with a cache volume dropped → none), `TestPreviewLinkNone` (link.add → none, Restart empty), `TestParseEditsStrict`.
- [ ] **Step 2:** Run → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Pass; `make check` (TestGenericity included).
- [ ] **Step 5:** Commit `settings: preview edits with data-impact labels`.

### Task 4: CLI `settings preview|apply`, `project show|write`

**Files:**
- Create: `internal/cli/settings.go`
- Modify: `internal/cli/cli.go` (dispatch `settings`), `internal/cli/project.go` (`show`, `write`), `internal/cli/completion.go`, `docs/json.md`, `AGENTS.md`
- Test: `internal/cli/settings_test.go`

**Interfaces:**
- Consumes: Task 3.
- Produces CLI:
  - `railswitch -C ws --json settings preview` < edits → Preview JSON.
  - `railswitch -C ws settings apply --if-hash <h> [--restart]` < edits.
  - `railswitch -C ws --json project show` → spec's document (`prefix, name, repo, base_branch, hash, options[{key,slot,ref,choices}], env[{name,value}], links[{label,url,from,hidden}]`).
  - `railswitch -C ws project write --if-hash <h>` < file.
  - Shared helper `func (e *env) commitProject(ctx, root string, old, new []byte) error`: write new, `workspace.Lock`, load + `e.render`; on error write old back and return the error.

- [ ] **Step 1:** Tests: `TestSettingsPreviewJSON` (shape), `TestSettingsApplyWritesLocksRenders` (file changed, stack.lock rewritten, unit file for removed service gone), `TestSettingsApplyRestoresOnRenderFailure` (fake runner fails the render's host read; file bytes equal the original), `TestSettingsApplyIfHash` (outside edit → exit 1, "project.toml changed since the preview", file untouched), `TestSettingsApplyBlocked` (refused, untouched), `TestSettingsApplyRestart` (fake: app unit active for `main` only → one `restart` sequence for main), `TestProjectShowJSON` (choices for a slot list built-ins of that slot then `none`; links carry `from` and `hidden`), `TestProjectWrite` (round-trip; invalid TOML refused, untouched).
- [ ] **Step 2:** Run → FAIL.
- [ ] **Step 3:** Implement. `--restart` calls the lifecycle `Restart` for each listed slug in order, stopping at the first error.
- [ ] **Step 4:** Pass; `make check`.
- [ ] **Step 5:** Commit `cli: settings preview and apply, project show and write`.

### Task 5: `overview` reports `stale`

**Files:**
- Modify: overview code in `internal/lifecycle` (`PrintOverview`), `internal/podman` (one `podman ps -a --format json` reader returning name → labels), `docs/json.md`
- Test: the overview test file in `internal/lifecycle`

**Interfaces:**
- Produces: overview worktree field `"stale": bool`; `podman.(Podman).Labels(ctx, key string) (map[string]string, error)` (container name → that label's value, running containers only).

- [ ] **Step 1:** `TestOverviewStale`: fake `podman ps` returns the app container with an old hash → `stale: true`; matching hash → false; not running → false. Exactly one `podman ps` call per overview.
- [ ] **Step 2–4:** FAIL, implement, pass, `make check`.
- [ ] **Step 5:** Commit `overview: stale when a container runs an older unit`.

### Task 6: Job stdin and CLI input in the dashboard

**Files:**
- Modify: `internal/jobs/jobs.go`, `internal/web/cli.go` (+ its concrete runner in `internal/cli/dashboard_serve.go`)
- Test: `internal/jobs/jobs_test.go`, `internal/web/cli_test` coverage in `server_test.go`

**Interfaces:**
- Produces: `func (h *Hub) StartInput(ctx context.Context, s Spec, stdin []byte) (Snapshot, error)` (`Start` = `StartInput(ctx, s, nil)`); the payload lives on the unexported `job`, never in `Spec`/`Snapshot`/log. `web.CLI` gains `RunInput(ctx context.Context, stdin []byte, args ...string) ([]byte, error)`.

- [ ] **Step 1:** `TestStartInputStdin`: fake runner records `Cmd.Stdin` bytes == payload; snapshot (marshalled with `%+v` and `json`) and the log file do not contain the payload.
- [ ] **Step 2–4:** FAIL, implement, pass, `make check`.
- [ ] **Step 5:** Commit `jobs: a job may carry stdin`.

### Task 7: Settings page

**Files:**
- Create: `internal/web/settings.go`, `internal/web/templates/settings.tmpl`, `internal/web/templates/staged.tmpl`, `internal/web/templates/settingsfile.tmpl`
- Modify: `internal/web/server.go` (routes), `internal/web/cli.go` (types `projectShow`, `previewDoc`, `secretRow`), `internal/web/templates/header.tmpl` (Settings link), `internal/web/templates/list.tmpl`/`detail.tmpl` ("restart to apply" when `stale`), `internal/web/static/app.js`, `internal/web/static/app.css`, `docs/dashboard.md`
- Test: `internal/web/settings_test.go`

**Interfaces:**
- Routes: `GET /p/{prefix}/settings`; `POST /p/{prefix}/settings/preview` (body: JSON edit list; runs `RunInput(list, "-C", ws, "--json", "settings", "preview")`; returns the `staged.tmpl` fragment plus `X-Settings-Hash`); `POST /p/{prefix}/settings/apply` (form/JSON: list, hash, restart; job `settings apply --if-hash h [--restart]`, stdin = list, project-level); `POST /p/{prefix}/settings/secret` (`name`, `value`, optional `services`, `env`, `add`; job `secret set <name>` or `secret add <name> --service s… [--env V]`, stdin = value); `GET /p/{prefix}/settings/file`, `POST /p/{prefix}/settings/file` (`hash`, `text`; job `project write --if-hash h`, stdin = text).
- JS: staged list in `sessionStorage` key `rs-staged-<prefix>`; every change re-POSTs preview and swaps `#staged`; Apply disabled while a card is blocked; on the apply job's `ok` event clear the list and reload.

- [ ] **Step 1:** Tests: `TestSettingsPageRenders` (options rows, env, links with From, secrets rows show set/unset and no value), `TestSettingsPreviewPost` (argv and stdin recorded; fragment has the labels), `TestSettingsApplyJob` (argv with `--if-hash` and `--restart`; stdin is the list; project-level job), `TestSettingsSecretNoLeak` (value with `\n'$(x)` is the job's stdin byte-exact; absent from argv, log, `slog` buffer, response body, SSE events, and the GET page), `TestSettingsGuard` (no token / foreign Origin → 403, nothing run), `TestSettingsFileConflict` (job failure text "changed since" shows the reload offer, keeps text), `TestStaleMarker`.
- [ ] **Step 2–4:** FAIL, implement, pass, `make check`.
- [ ] **Step 5:** Commit `web: project settings page`.

### Task 8: Host acceptance and docs

**Files:**
- Create: `docs/stage6b-acceptance.md`
- Modify: `docs/superpowers/plans/2026-10-25-railswitch-roadmap.md` (6b status), `AGENTS.md` (Stage 6b additions)

- [ ] **Step 1:** On this host with the built binary and the dashboard: Mastodon redis pin 8 → 7 with restart now, app back (`curl` 200); Plausible `analytics` → none shows "Data not carried over", apply, restore; add a link and see it on the dashboard; set a secret and `grep -r` the value over the workspace, `~/.local/share/railswitch` and the job logs: no hit. Drive the page with Playwright as in 6a.
- [ ] **Step 2:** Record results, gaps and fixes in `docs/stage6b-acceptance.md`; `make check`.
- [ ] **Step 3:** Commit `docs: Stage 6b host run`.
