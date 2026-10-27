# railswitch Stage 6a — Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A local web dashboard (`railswitch dashboard serve`, unit `railswitch-dashboard.service`, `http://railswitch.localhost:7700/`) showing each project's worktrees with stack state, PRs, agent state and links, and running up/stop/restart/add/remove/tasks as `railswitch` jobs.

**Architecture:** New read commands (`overview`, `pr ls`, `task ls`) give the page everything as JSON; the server (`internal/web`) polls them, renders `html/template` fragments and pushes them over SSE; every action is a child `railswitch` process managed by `internal/jobs`. Colours live in `dashboard.toml` behind `railswitch dashboard colors`.

**Tech Stack:** Go stdlib (`net/http`, `html/template`, `embed`, `crypto/subtle`), `github.com/BurntSushi/toml`, vanilla JS/CSS embedded.

**Spec:** `docs/superpowers/specs/2026-10-27-railswitch-stage-6a-dashboard-design.md` (read it first; this plan does not repeat its tables).

## Global Constraints

- Every external command goes through `sys.Runner` with a timeout or `sys.NoTimeout` (jobs only); argv only, never a shell on the host.
- Dependencies: stdlib + BurntSushi/toml. No third-party JS/CSS; no build step; assets via `go:embed` from `internal/web/static/` and `internal/web/templates/`.
- Listen `127.0.0.1:7700` only. Allowed Host values exactly `railswitch.localhost:7700`, `localhost:7700`, `127.0.0.1:7700`.
- Token file `<Dirs.Data>/dashboard/token` mode 0600 in dir 0700; cookie `railswitch_dashboard`; POST needs `Origin == "http://"+Host`.
- No secret, token or secret value in argv, job logs, SSE events or pages. The login URL is printed only by `railswitch dashboard`.
- Worktree files are read only via `safefile.Read`; `overview` runs no git.
- `dashboard.toml` carries `schema = 1`; a higher schema is refused ("written by a newer railswitch").
- Display name "RailSwitch"; identifiers stay `railswitch`. No framework names in core (`TestGenericity`).
- Every `--json` read command prints one JSON document; schemas added to `docs/json.md`.
- Unit tests need no podman, systemd, gh or network (`sh`, `bash`, `git` allowed; `internal/sys/systest` Fake).
- `make check` before every commit; stage files by path.

## Review Focus

- A POST from a foreign page (Origin `http://evil.example`, or none) must run nothing — Task 8 guard tests assert the fake CLI and job hub saw zero calls.
- A job whose output is a flood (`yes | head -c 20M`) must not grow memory or the log past the caps — Task 1 test with a 20 MiB writer.
- `overview` on a project where one worktree directory was deleted by hand must still list the others (that worktree: `stack` from systemd, `branch` "") — Task 5 test.
- `pr ls` when `gh` is not installed or not logged in must exit 1 with gh's message, and the page must show "PRs unavailable" without breaking polling — Task 3 test (runner error) and Task 8 test (CLI error → banner).
- An agent that stays in `permission` across polls notifies once; going `working` → `permission` again notifies again — Task 8 notification test.

---

### Task 1: Group kill with grace, and the job hub

**Files:**
- Modify: `internal/sys/runner.go` (Cmd gains `KillGrace`)
- Test: `internal/sys/runner_test.go`
- Create: `internal/jobs/jobs.go`, `internal/jobs/log.go`, `internal/jobs/jobs_test.go`

**Interfaces:**
- Produces:
  ```go
  // sys.Cmd: KillGrace > 0 runs the command in its own process group even
  // when untimed; cancellation sends SIGTERM to the group, then SIGKILL to
  // the group after KillGrace.
  KillGrace time.Duration

  package jobs
  var ErrBusy = errors.New("busy")
  type Spec struct {
      Project string   // prefix
      Slug    string   // "" = project-level job (blocks the whole project)
      Label   string   // shown in the page, e.g. "up", "task test"
      Argv    []string // full argv; argv[0] is the program
      Dir     string
  }
  type State string // "running", "ok", "failed", "cancelled"
  type Snapshot struct {
      ID string; Spec Spec; State State; Exit int
      Started, Ended time.Time
      Lines []string // kept output, oldest first
      Dropped bool   // output was dropped by a cap
  }
  type Event struct{ Job Snapshot } // published on start, output (≤ every 100 ms) and end
  type Hub struct{ /* unexported */ }
  func New(r sys.Runner, logDir string, env func(context.Context) []string) (*Hub, error) // creates logDir 0700, deletes old *.log
  func (h *Hub) Start(ctx context.Context, s Spec) (Snapshot, error) // ErrBusy wrapped with the running job's label
  func (h *Hub) Cancel(id string) error
  func (h *Hub) Jobs(project, slug string) []Snapshot // newest first; slug "" = all of the project
  func (h *Hub) Running() bool
  func (h *Hub) Subscribe() (<-chan Event, func())
  ```
- Constants (spec "Jobs"): `KeepLines = 5000`, `KeepBytes = 4 << 20`, `MaxLine = 64 << 10`, `LogLimit = 8 << 20`, `KeepJobs = 20`, `Grace = 3 * time.Second`, scope prefix `systemd-run --user --scope --collect --quiet --`.

- [ ] **Step 1:** Tests in `runner_test.go`: `TestKillGraceTERMThenKILL` — run `sh -c 'trap "" TERM; sleep 30'` with `Timeout: sys.NoTimeout, KillGrace: 200ms`, cancel ctx → returns within 2 s; `TestKillGraceTERMHonoured` — `sh -c 'trap "exit 3" TERM; sleep 30 & wait'` → exit code 3 after cancel.
- [ ] **Step 2:** Run `go test ./internal/sys -run KillGrace` → FAIL (no field).
- [ ] **Step 3:** Implement: when `KillGrace > 0`, set `Setpgid`, `cmd.Cancel` = SIGTERM to `-pid` and start a timer that SIGKILLs `-pid` after `KillGrace`; `WaitDelay = KillGrace + 2s`. Timed calls without KillGrace keep today's behaviour.
- [ ] **Step 4:** Tests in `jobs_test.go` with the real `sys.Exec` and `sh` (the hub's runner is injected; tests pass a runner wrapper that strips the `systemd-run …--` prefix and records the full argv): `TestStartWrapsInScope` (recorded argv starts with the scope prefix, then `Spec.Argv`); `TestLogFileMode` (0600, dir 0700); `TestOutputCaps` (a job printing 6000 lines keeps the last 5000; a 100 KiB line is cut to 64 KiB; `printf '\033[31mred\033[0m'` is kept as `red`; invalid UTF-8 becomes `?`); `TestFloodCapped` (a job writing 20 MiB: log file ≤ 16 MiB at all times, `Dropped` true, kept bytes ≤ 4 MiB); `TestBusy` (second job for the same slug → `errors.Is(err, ErrBusy)`; a project-level job blocks every slug and vice versa); `TestRetention` (25 finished jobs → 20 kept, oldest logs deleted); `TestCancel` (state `cancelled`); `TestStates` (exit 0 → ok, exit 4 → failed with Exit 4); `TestEnv` (child sees `NO_COLOR=1` and the injected env).
- [ ] **Step 5:** Run `go test ./internal/jobs` → FAIL.
- [ ] **Step 6:** Implement `jobs.go` (hub, locking, retention, events) and `log.go` (log file `O_CREATE|O_WRONLY|O_APPEND|O_NOFOLLOW` 0600 handed to the child as Stdout and Stderr; a tail goroutine reads it, splits lines, strips ANSI CSI/OSC, replaces invalid UTF-8, applies caps, truncates the file when the reader has caught up past `LogLimit` or when it exceeds `2*LogLimit`, appending "… output dropped (log over 8 MiB)"). Child: `sys.Cmd{Name: argv[0], Args: argv[1:], Dir, Env: env(ctx)+"NO_COLOR=1", Stdin: empty reader, Stdout: f, Stderr: f, Timeout: sys.NoTimeout, KillGrace: Grace}`.
- [ ] **Step 7:** `go test ./internal/sys ./internal/jobs` → PASS; `make check`.
- [ ] **Step 8:** Commit `jobs: job hub with scope, caps, cancel; sys: KillGrace`.

### Task 2: Links, `hide_links` and project tasks in the model

**Files:**
- Modify: `internal/config/project.go` (fields + validation), `internal/config/project_test.go`
- Modify: `internal/pack/load.go:391` (`links` checks placeholders), `internal/pack/load_test.go`
- Create: `internal/render/links.go`, `internal/render/links_test.go`
- Modify: `internal/render/model.go` (hide_links check while building)
- Docs: `docs/packs.md` (link placeholders), `docs/project.md` or the doc that describes `project.toml` keys (find it with grep `\[\[routes\]\]` in `docs/`)

**Interfaces:**
- Produces:
  ```go
  // config.Project
  HideLinks []string          `toml:"hide_links"` // labels of stack links not shown
  Tasks     map[string]string `toml:"tasks"`      // project tasks; "" removes a pack task

  // render
  type Link struct {
      Label string `json:"label"`
      URL   string `json:"url"`
      Group string `json:"group"` // "app", "project", "stack"
  }
  func (m *Model) Links(in *Instance) ([]Link, error)
  type Task struct {
      Name    string `json:"name"`
      Command string `json:"command"`
      Source  string `json:"source"` // "pack" or "project"
  }
  func (m *Model) Tasks() []Task // sorted by name; project overrides pack; "" removes
  ```
- [ ] **Step 1:** Tests: config `TestHideLinksAndTasksDecode` (both keys decode; a task name with a newline or an empty name is a validation error at `tasks."…"`; a `hide_links` entry that is empty is an error); pack `TestLinkPlaceholders` (an option link `{worktree.hots}/x` is a load error naming the file and placeholder); render `TestLinksOrder` (rails pack + sidekiq + playwright fixture: App `http://<slug>.<prefix>.localhost/` first, then project links in file order, then pack links, then option links in `config.Slots` order, then tools; `vnc.{worktree.host}` → `http://vnc.<host>`; a URL that already has `https://` is unchanged); `TestHideLinks` (hiding "Routes" removes it; `hide_links = ["Nope"]` fails model build with `hide_links: no stack link labelled "Nope"`); `TestTasksMerge` (pack `test` overridden by project, project `console = ""` removes it, project `lint` added with source `project`).
- [ ] **Step 2:** Run the three packages' tests → FAIL.
- [ ] **Step 3:** Implement. URL rule: expand with the instance's values (`{worktree.host}`, `{worktree.slug}`, `{branch}`); prefix `http://` when the result has no `scheme://`. Group "stack" collects pack, then `config.Slots` options, then `m.Inputs.Stack.Tools`. The hide check runs once per model build against all stack link labels.
- [ ] **Step 4:** Run tests → PASS; update goldens only if a golden test fails because of the new validation (it shouldn't); `make check`.
- [ ] **Step 5:** Docs: `hide_links`, `[tasks]` in the project.toml reference; link placeholders in `docs/packs.md`.
- [ ] **Step 6:** Commit `render: resolved worktree links, hide_links, project tasks`.

### Task 3: `internal/prs` (GitHub PRs via gh)

**Files:**
- Create: `internal/prs/prs.go`, `internal/prs/prs_test.go`, `internal/prs/testdata/gh-pr-list.json`

**Interfaces:**
- Produces:
  ```go
  package prs
  type PR struct {
      Number int    `json:"number"`
      Title  string `json:"title"`
      URL    string `json:"url"`
      Branch string `json:"-"`
      State  string `json:"state"`  // "draft", "open", "merged", "closed"
      Checks string `json:"checks"` // "pass", "fail", "pending", "none"
      Review string `json:"review"` // "approved", "changes_requested", "pending", "none"
  }
  func Repo(url string) (string, error) // "owner/name" from https://github.com/o/n(.git), git@github.com:o/n(.git), ssh://git@github.com/o/n; else error "<url> is not a GitHub repo"
  func List(ctx context.Context, r sys.Runner, repo, dir string) ([]PR, error)
  func ForBranch(prs []PR, branch string) *PR // open/draft first, then highest number; nil when none
  ```
- [ ] **Step 1:** Tests: `TestRepo` (the three URL forms, `.git` stripped, gitlab URL error); `TestListArgv` (Fake records `gh pr list --repo o/n --state all --limit 200 --json number,title,url,headRefName,isDraft,state,reviewDecision,statusCheckRollup`, `Dir` = dir, env contains `GH_PROMPT_DISABLED=1`, `Timeout` 30 s); `TestParse` with the fixture: one PR each of draft, open+approved+all checks success, open+changes requested+a failed check, open+REVIEW_REQUIRED+an IN_PROGRESS check run, open with a StatusContext `PENDING`, merged, closed, and one with `statusCheckRollup: []` → the expected `State/Checks/Review`; neutral and skipped conclusions count as pass; `TestForBranch` (merged #10 and open #12 on one branch → #12; two closed → higher); `TestListError` (runner returns `ErrExit` with stderr "gh: not logged in" → error contains "not logged in").
- [ ] **Step 2:** `go test ./internal/prs` → FAIL.
- [ ] **Step 3:** Implement; env = `os.Environ()` + `GH_PROMPT_DISABLED=1`.
- [ ] **Step 4:** PASS; `make check`; commit `prs: GitHub PR state via gh`.

### Task 4: `internal/dashconf` (dashboard colours)

**Files:**
- Create: `internal/dashconf/dashconf.go`, `internal/dashconf/presets.go`, `internal/dashconf/dashconf_test.go`

**Interfaces:**
- Produces:
  ```go
  package dashconf
  const SchemaVersion = 1
  var Tokens = []string{"bg", "bg2", "text", "muted", "accent", "blue", "green", "yellow"} // editable, in editor order
  var Fixed = []string{"sel", "border", "purple", "red"}
  var PresetNames = []string{"flexoki", "paper", "solar", "contrast"}
  type Mode struct {
      Base      string            `toml:"base" json:"base"`
      Overrides map[string]string `toml:"-" json:"overrides"` // token → #RRGGBB; flattened into the mode table in the file
  }
  type File struct { Light, Dark Mode }
  func Path(d machine.Dirs) string // <d.Config>/dashboard.toml
  func Load(d machine.Dirs) (*File, error) // missing file = flexoki/flexoki, no overrides
  func (f *File) Save(d machine.Dirs) error // atomic write, schema = 1, only changed tokens
  func (f *File) Set(dark bool, base string, overrides map[string]string) error // base "" keeps; validates names and #RRGGBB (upper-cased)
  func (f *File) Reset(dark bool)
  func (f *File) Colors(dark bool) map[string]string // preset + overrides, all 12 tokens
  func Contrast(a, b string) float64 // WCAG ratio
  func Warnings(c map[string]string) []string // "text: contrast 3.2:1 against bg (below 4.5:1)" for tokens other than bg/bg2
  ```
  File format exactly as the spec's "Colours" block (`[colors.light] base = "flexoki"`, then one key per override).
- [ ] **Step 1:** Tests: `TestPresetsComplete` (every preset × mode has all 12 tokens, values equal the spec table — copy the table into a test map); `TestRoundTrip` (Set dark accent, Save, Load → same; file contains `schema = 1`, `[colors.dark]`, `accent = "#DA702D"` and no unchanged tokens); `TestSetRejects` (unknown token `link`, `#12345`, `red` (fixed token), unknown base → errors naming them); `TestNewerSchema` (`schema = 2` → error containing "newer railswitch"); `TestUnknownKey` (`[colors.light] foo = "#000000"` → error); `TestContrast` (`#000000` vs `#FFFFFF` = 21; flexoki light text/bg ≥ 4.5; `Warnings` flags `#AAAAAA` text on white).
- [ ] **Step 2:** FAIL. **Step 3:** Implement (decode with `toml.Decode` into `map[string]any` per mode or a custom `UnmarshalTOML`; reject unknown keys; reuse `tomlfile` schema handling if it fits). **Step 4:** PASS; `make check`; commit `dashconf: dashboard colour presets and file`.

### Task 5: `railswitch overview`

**Files:**
- Modify: `internal/podman/systemd.go` (`ShowMany`), its test
- Create: `internal/lifecycle/overview.go`, `internal/lifecycle/overview_test.go`
- Modify: `internal/lifecycle/status.go` (share the row-building with overview where natural)
- Create: `internal/cli/overview.go`; Modify: `internal/cli/cli.go` (command map), `internal/cli/completion.go` + `internal/cli/completion/*` (new command)
- Docs: `docs/json.md` (`overview` section), command reference doc where `status` is described

**Interfaces:**
- Consumes: Task 2 `Model.Links`.
- Produces:
  ```go
  // podman.Systemd
  func (s Systemd) ShowMany(ctx context.Context, units []string, props ...string) (map[string]map[string]string, error) // one `systemctl --user show -p Id,<props> u1 u2 …`; keyed by Id
  // lifecycle
  type OverviewService struct{ Service, Role, State string } // json service, role, state
  type OverviewWorktree struct {
      Slug    string            `json:"slug"`
      ID      int               `json:"id"`
      Branch  string            `json:"branch"`
      Base    bool              `json:"base"`
      Stack   string            `json:"stack"` // "up", "partial", "stopped", "failed"
      Services []OverviewService `json:"services"`
      Agent   *AgentRow         `json:"agent"`
      Links   []render.Link     `json:"links"`
  }
  type Overview struct {
      Prefix    string             `json:"prefix"`
      Workspace string             `json:"workspace"`
      Worktrees []OverviewWorktree `json:"worktrees"` // registry order (ID)
  }
  func (l *L) Overview(ctx context.Context) (Overview, error)
  func StackState(services []OverviewService) string
  ```
- [ ] **Step 1:** Tests: `TestShowManyParses` (Fake output of two blocks separated by a blank line → two maps keyed by Id; one call); `TestStackState` table (all app/worker/data active → up; one activating → partial; none active → stopped; any failed → failed; deps/tool/agent rows ignored; no stack rows → stopped); `TestOverview` with `testfixture.Workspace` (two worktrees; Fake systemctl answers; one agent status file `permission` in worktree b → b.Agent.State "permission"; links equal `Model.Links`; base worktree has `Base: true`; Fake saw exactly one `systemctl` call and at most one `podman` call); `TestOverviewMissingDir` (worktree b's directory deleted → still listed, `Branch` ""); `TestOverviewAgentChoice` (running agent wins; else first non-off; else nil).
- [ ] **Step 2:** FAIL. **Step 3:** Implement; on-demand agents' "running" from one `podman ps --format {{.Names}}` per overview (exact-name set membership); `agent` per spec. CLI: `overview [--json]`, text form a table `WORKTREE BRANCH STACK AGENT` plus links indented. **Step 4:** PASS; docs; `make check`; commit `overview: one-project status for the dashboard`.

### Task 6: `pr ls`, `task`, `wt add --up`

**Files:**
- Create: `internal/cli/pr.go`, `internal/cli/task.go`, tests `internal/cli/pr_test.go`, `internal/cli/task_test.go`
- Create: `internal/lifecycle/task.go`, `internal/lifecycle/task_test.go`
- Modify: `internal/cli/wt.go` (`--up`), `internal/cli/cli.go`, `internal/cli/completion.go` + scripts
- Docs: `docs/json.md` (`pr ls`, `task ls`), command docs

**Interfaces:**
- Consumes: Task 2 `Model.Tasks`, Task 3 `prs.Repo/List/ForBranch`.
- Produces:
  - `railswitch pr ls [--json]` → `[{"slug","branch","pr":{…}|null}]` in registry order.
  - `railswitch task ls [--json]` → `[]render.Task`; `railswitch task [--new-term] <name> [args…]` (args after the name are passed through; `--` optional).
  - `func (l *L) Task(ctx context.Context, slug, name string, args []string) error` → `l.Exec(ctx, slug, false, append([]string{"sh", "-c", cmd + ` "$@"`, name}, args...))`; unknown task → `UsageError{"unknown task <name>; railswitch task ls"}`.
  - `railswitch wt add [--name n] [--up] <target>`: after a successful add, runs `Up` for the new slug with the same output.
- [ ] **Step 1:** Tests: `TestPrLsJSON` (Fake gh output fixture, two worktrees, one matching branch → its `pr`, the other `null`); `TestPrLsNotGitHub` (repo gitlab → exit 1 "is not a GitHub repo"); `TestTaskArgv` (Fake: running app container → recorded `podman exec -i -w /app <ctr> sh -c 'bin/rails test "$@"' test a b`); `TestTaskUnknown` (exit 2); `TestTaskLsJSON`; `TestTaskNewTerm` (replaced `launchTerm` gets `railswitch -C <root> -w <slug> task test a`); `TestWtAddUp` (after add, `systemctl --user start` of the new worktree's app unit is in the Fake calls).
- [ ] **Step 2:** FAIL. **Step 3:** Implement; completion: `pr ls`, `task ls`, task names for `task` (from `task ls`), `--up`, `--new-term`. **Step 4:** PASS; docs; `make check`; commit `cli: pr ls, task, wt add --up`.

### Task 7: The dashboard unit and `railswitch dashboard`

**Files:**
- Create: `internal/machine/dashboard.go`, `internal/machine/dashboard_test.go`
- Modify: `internal/machine/dirs.go` (`SystemdUser`), `internal/workspace/workspace.go:439` area (write the unit on render), `internal/render/quadlet.go:208-213` (`Wants=` for the app), goldens under `internal/render/testdata/golden/**` (regenerate with the repo's update flag; check only the `Wants=` line changed)
- Modify: `internal/lifecycle/up.go:195-244` (restart a stale dashboard), `internal/cli/machine.go` (text + JSON), `docs/json.md` (`machine.dashboard`)
- Create: `internal/cli/dashboard.go`, `internal/cli/dashboard_test.go`; Modify `cli.go`, completion

**Interfaces:**
- Produces:
  ```go
  // machine
  const DashboardService = "railswitch-dashboard.service"
  const DashboardAddr = "127.0.0.1:7700"
  const DashboardURL = "http://railswitch.localhost:7700/"
  // Dirs.SystemdUser = $XDG_CONFIG_HOME/systemd/user (default ~/.config/systemd/user)
  func DashboardUnit(exe string) []byte
  func WriteDashboardUnit(d Dirs, exe string) (changed bool, err error) // atomic; 0644
  func DashboardToken(d Dirs) (string, error) // reads <Data>/dashboard/token; creates it (32 random bytes hex, 0600, dir 0700) when missing
  ```
  - Unit text exactly as the spec's ini block; first line `# railswitch: machine unit, rewritten by railswitch`.
  - `railswitch dashboard` → prints `<DashboardURL>login?token=<token>` and, when the unit isn't active, a second line `start it: systemctl --user start railswitch-dashboard.service`.
  - `railswitch dashboard colors [--json]`, `dashboard colors set [--dark] [--base p] [tok=#hex…]`, `dashboard colors reset [--dark]` (Task 4 API); `--json` prints `{"light":{"base","overrides","colors"},"dark":{…},"warnings":{"light":[],"dark":[]}}`.
  - `railswitch dashboard serve` is added in Task 8.
- [ ] **Step 1:** Tests: `TestDashboardUnit` (exact text for `/usr/bin/railswitch`); `TestWriteDashboardUnit` (first write changed, second unchanged, file 0644 under `SystemdUser`); `TestDashboardToken` (created 0600/0700, stable on second call, 64 hex chars); quadlet golden: app units list `railswitch-dashboard.service` in `Wants=`, non-app units don't; `TestUpRestartsStaleDashboard` (Fake: unit active, `ActiveEnterTimestampMonotonic`/`ActiveEnterTimestamp` older than the unit file's mtime → `systemctl --user restart railswitch-dashboard.service`; newer → no restart; inactive → no restart); `TestMachineJSONDashboard`; `TestDashboardPrintsLogin`; `TestColorsCLI` (set, then `--json` shows the override and a warning for a low-contrast text).
- [ ] **Step 2:** FAIL. **Step 3:** Implement; `workspace.Render` writes the unit with `os.Executable()` + `filepath.EvalSymlinks` (tests inject the path through a package variable). **Step 4:** PASS; regenerate goldens; `make check`; docs; commit `dashboard: unit, login link, colours CLI`.

### Task 8: `internal/web` and `railswitch dashboard serve`

**Files:**
- Create: `internal/web/server.go` (Server, routes), `guard.go`, `state.go` (pollers, diff, notifications), `view.go` (view models, sorting, glyphs, PR/agent labels), `args.go` (shell-style split), `templates/*.tmpl` (layout, header, strip, list, detail, jobs, dialogs, palette, colours, login), `static/app.css`, `static/app.js`
- Tests: `internal/web/guard_test.go`, `server_test.go`, `state_test.go`, `view_test.go`, `args_test.go`
- Modify: `internal/cli/dashboard.go` (`serve`)

**Interfaces:**
- Consumes: Task 1 `jobs.Hub`, Task 4 `dashconf`, Task 5/6/7 JSON shapes (decode into local structs mirroring `docs/json.md`; don't import `lifecycle`).
- Produces:
  ```go
  package web
  // CLI runs `railswitch <args>` (argv[0] is the dashboard's own executable) and returns stdout; non-zero exit → error with stderr tail.
  type CLI interface{ Run(ctx context.Context, args ...string) ([]byte, error) }
  type Config struct {
      Token   string
      CLI     CLI
      Jobs    *jobs.Hub
      Exe     string            // for job argv[0]
      Dirs    machine.Dirs      // colours file
      Version string
      Now     func() time.Time
  }
  type Server struct{ /* … */ }
  func New(c Config) *Server
  func (s *Server) Handler() http.Handler
  func (s *Server) Run(ctx context.Context) error // pollers; returns when ctx ends
  func SplitArgs(s string) ([]string, error)      // quotes ' and ", backslash; unterminated quote → error
  ```
  Routes (all behind the guard; spec "Access"):
  `GET /login?token=`, `GET /healthz`, `GET /` (→ 303 `/p/<first prefix>`; JS swaps to the remembered one), `GET /p/{prefix}`, `GET /p/{prefix}/{slug}`, `GET /events?p=`, `GET /p/{prefix}/{slug}/remove` (dialog fragment; runs `-C ws wt ls --json`), `GET /p/{prefix}/{slug}/tasks` (`task ls --json`), `GET /colors` (fragment), `GET /static/…`;
  `POST /p/{prefix}/{slug}/act/{up|stop|restart}`, `POST /p/{prefix}/{slug}/remove` (form `force`, `confirm`), `POST /p/{prefix}/worktrees` (form `branch`, `name`, `up`), `POST /p/{prefix}/{slug}/task` (form `task`, `args`, `term`), `POST /p/{prefix}/prs/refresh`, `POST /jobs/{id}/cancel`, `POST /colors` (form `mode`, `base`, tokens), `POST /colors/reset` (form `mode`). POST answers 202 with the job id, 409 busy, 422 bad input, 404 unknown project/worktree/task. Prefix and slug must exist in the latest `project ls`/`overview`; never passed through unchecked.
  Job argv: `[Exe, "-C", workspace, "-w", slug, …]`; add: `[Exe, "-C", ws, "wt", "add", ("--name", n)?, ("--up")?, branch]`; remove: `[…, "wt", "rm", ("--force")?, slug]` (force only when `confirm == slug`); task: `[…, "task", (--new-term)?, name, args…]`; colours: `[Exe, "dashboard", "colors", "set", ("--dark")?, "--base", b, "tok=#hex"…]` run synchronously through `CLI`, not as a job.
  - `serve`: `DashboardToken`, `jobs.New(sys.Exec{}, <Data>/dashboard/jobs, sessionEnv)`, `http.Server{Addr: DashboardAddr, ReadHeaderTimeout: 10s}`; exits 0 when its executable's size/mtime changes and `!Jobs.Running()` (checked every 5 s). `sessionEnv` = `os.Environ()` overlaid with `DISPLAY WAYLAND_DISPLAY XDG_CURRENT_DESKTOP XDG_RUNTIME_DIR SSH_AUTH_SOCK TERMINAL TMUX KITTY_PID WEZTERM_PANE KONSOLE_VERSION TERMINATOR_UUID ALACRITTY_WINDOW_ID GHOSTTY_RESOURCES_DIR GHOSTTY_BIN_DIR` from `systemctl --user show-environment` (10 s timeout; `$'…'` values decoded).
- [ ] **Step 1: guard tests** (`guard_test.go`, fake CLI recording calls, real `jobs.Hub` with a recording runner): Host `evil.example:7700` → 403 for GET and POST; no cookie → 401 on `/p/x` and `/events`; `/login?token=bad` → 403, no cookie; `/login?token=<good>` → 303 to `/`, `Set-Cookie` with `HttpOnly`, `SameSite=Strict`, `Path=/`; POST with cookie and no Origin → 403; with `Origin: http://evil.example` → 403; in every refusal the fake CLI and the job runner saw zero calls; good POST `act/up` → 202 and the job argv `[exe -C <ws> -w b up]`; every response (403 included) has `X-Frame-Options: DENY` and the CSP; `/healthz` without cookie → 200 `ok <version>`.
- [ ] **Step 2: view tests** (`view_test.go`): sorting (name: base first then slug; running: failed, up, partial, stopped, then name; PR: highest number first, none last; agent: permission, question, reply, your turn, working, idle, off; `reverse` flips); labels (agent done+reply → "waiting for reply", done → "your turn"; PR `#2141 ✓ ready · approved`, draft/merged/closed words; CSS class per state per the spec's colour table); the detail fragment for a fixture worktree contains the Links section before Services, App link first with class `main`, a stopped stack's links carry class `dim`; no remove button for the base worktree; a merged PR shows "Remove worktree".
- [ ] **Step 3: state tests** (`state_test.go`, fake clock + fake CLI with scripted `overview`/`pr ls` outputs): a change in one worktree's row produces one `html` event for `row-<slug>` (and `detail` when selected), none when unchanged; notifications fire on transition into permission/question/reply/your turn only, once, with title "ftest / feat-login needs permission" and the message cut at 200 chars; repeated `permission` polls → no new notify; `working` → `permission` again → notify; the need-input count across two projects feeds the header and title; `pr ls` error → one banner event "PRs unavailable: <msg>", polling continues; polling cadence 2 s for a project with a subscriber, 10 s otherwise, PRs 60 s.
- [ ] **Step 4: server tests** (`server_test.go`): `POST /p/ftest/worktrees` branch `jb/x` name `x` up `on` → argv `[exe -C ws wt add --name x --up jb/x]`; remove without force → `wt rm b`; remove with `force=1&confirm=wrong` → 422, nothing run; with `confirm=b` → `wt rm --force b`; remove for the base → 422; unknown slug → 404; task with args `a "b c"` → `task test a "b c"` split into `a`, `b c`; `term=1` adds `--new-term`; busy → 409; `POST /colors` runs `dashboard colors set --base paper accent=#7C3AED` through CLI; `args_test.go` cases (`a 'b c' "d\"e" f\ g` → `a`, `b c`, `d"e`, `f g`; `"x` → error).
- [ ] **Step 5:** Run `go test ./internal/web` → FAIL.
- [ ] **Step 6:** Implement server, guard, state, view, args, templates. Pages render complete HTML server-side (usable without JS except live updates and dialogs). SSE event JSON per spec; `: ping` every 15 s.
- [ ] **Step 7:** Implement `static/app.js`: EventSource with reconnect; `html` events replace `#id` outerHTML; `notify` → `new Notification(title, {body, tag})` when granted, click → `window.focus()` + navigate; title `(N) RailSwitch`; keys ↑/↓, `P`, `1`–`9`, `:`, Esc; sort bar → `localStorage["railswitch.sort.<prefix>"]` and `?sort=`; last project → `localStorage["railswitch.project"]`; dialogs (new worktree, remove, palette with filter, Enter/Shift+Enter, colours with live preview via CSS variables and client-side contrast warning); fetch POSTs with `credentials: "same-origin"`. `static/app.css`: the layout of `.superpowers/brainstorm/*/content/dashboard-a-dark.html` with CSS variables `--bg --bg2 --sel --border --text --muted --accent --blue --green --yellow --purple --red`, set from `dashconf` in a `<style>` block with `@media (prefers-color-scheme: dark)`; fonts `Nunito, system-ui, sans-serif` and `"JetBrains Mono", ui-monospace, monospace`.
- [ ] **Step 8:** `serve` in `internal/cli/dashboard.go`; `go test ./internal/web ./internal/cli` → PASS; `make check`.
- [ ] **Step 9:** Commit `web: the dashboard`.

### Task 9: Docs, decisions, acceptance, smoke

**Files:**
- Create: `docs/dashboard.md` (what it shows, login, links and `hide_links`, sorting, keys, actions, task palette, notifications and the open-tab requirement, colours, the unit and how to stop it), `docs/stage6a-acceptance.md` (spec "Host acceptance" checks as commands + expected results; Run log; Done checklist; Gap log)
- Modify: `docs/superpowers/plans/2026-10-25-railswitch-roadmap.md` (D72–D80 rows, Stage 6 row split into 6a–6d, Install review → Stage 7), `AGENTS.md` (package map: `internal/jobs`, `internal/prs`, `internal/dashconf`, `internal/web`; `--json` list gains `overview`, `pr ls`, `task ls`, `dashboard colors`; D1–D80; dashboard address/unit constraint line), `docs/json.md` (check all new sections present)
- [ ] **Step 1:** Write the docs.
- [ ] **Step 2:** Smoke: a throwaway `internal/web/smoke_test.go` (build tag `smoke`, deleted after) that serves `web.New` with a fake CLI returning three worktrees (permission / working / stopped, PRs draft / approved / merged) on a free port; open it with the browser tool in light and dark (`prefers-color-scheme` emulation), screenshot both, exercise sort, ↑/↓, the palette and the remove dialog; check the console for errors. Delete the file.
- [ ] **Step 3:** `make check`; commit `docs: Stage 6a dashboard, decisions, acceptance`.
