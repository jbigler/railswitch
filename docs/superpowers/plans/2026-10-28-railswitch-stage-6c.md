# Stage 6c new project Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create a project from the dashboard: read the repository, suggest a pack and options with evidence, review the exact `project.toml`, create as a job.

**Architecture:** Detection rules in package manifests (`internal/pack`); a new `internal/detect` package fetches only the named files (shallow sparse git) and chooses; CLI `project detect`, `project new --dry-run --json`, `project new --up`; a `/new` page in `internal/web` that runs those commands.

**Tech Stack:** Go stdlib + `github.com/BurntSushi/toml`; `html/template`, vanilla JS.

**Spec:** `docs/superpowers/specs/2026-10-28-railswitch-stage-6c-new-project-design.md`

## Global Constraints

- `make check` before every commit (`gofmt -l` empty, `go vet`, `staticcheck` at `$(go env GOPATH)/bin`, `go test ./...`).
- Dependencies: stdlib + `github.com/BurntSushi/toml` only.
- Commands: argv only, through `sys.Runner` with a timeout; unit tests need no podman, systemd, gh or network (`git` allowed; use a local bare repo as a remote).
- Genericity: no framework or package name in non-test core code (`TestGenericity`); rules live in `internal/pack/builtin/**`.
- Files from a repository are read only through `safefile.Read`; printed text through `safefile.Printable`.
- The dashboard reads through `railswitch --json`, changes through `railswitch` jobs; no inline script or style; every POST through the guard.
- Every behaviour change updates its doc (`docs/packs.md`, `docs/json.md`, `docs/dashboard.md`, `AGENTS.md`).

## Review Focus

- A repository URL with spaces, `;`, `$(…)` or a leading `-`: one argv entry, refused if it starts with `-` (Task 2, 3, 4).
- A repository whose `Gemfile` is a symlink to `/etc/passwd` or a 2 MiB file: treated as absent, never read (Task 2).
- A detect run that fails mid-fetch (bad branch): the temp dir is still removed (Task 2).
- The dry run on a prefix in use: the real run's refusal text, nothing written (Task 3).
- Create that fails after the clone: the form keeps its values and the output shows what was left (Task 4).

---

### Task 1: Detection rules in package manifests

**Files:** Modify `internal/pack/format.go`, `internal/pack/load.go`, `internal/pack/builtin/packs/rails/pack.toml`, `internal/pack/builtin/options/{postgres,redis,rustfs,sidekiq,playwright}/option.toml`, `docs/packs.md`. Test `internal/pack/load_test.go` (or `local_test.go`).

**Interfaces — Produces:**
- `pack.Pack.Detect PackDetect` (`toml:"detect"`), `type PackDetect struct{ Files []string }`.
- `pack.Option.Detect []OptionDetect` (`toml:"detect"`), `type OptionDetect struct{ File, Match, Evidence string }`; `func (d OptionDetect) Regexp() *regexp.Regexp` (compiled at load, cached).
- Load errors: a path that is absolute, not clean, or has `..`; a `match` that doesn't compile.

- [ ] Tests `TestDetectRulesLoad` (built-ins carry the spec's rules), `TestDetectRulesRefused` (`../x`, `/etc/x`, `a//b`, bad regexp).
- [ ] Implement; built-in rules exactly as the spec lists; packs.md tables.
- [ ] `make check`; commit `pack: detection rules`.

### Task 2: `internal/detect` and `project detect`

**Files:** Create `internal/detect/detect.go`, `internal/detect/detect_test.go`; modify `internal/cli/project.go` (dispatch `detect`), `internal/cli/new.go` (export or reuse `remoteHead`, `gitArg`), `internal/cli/completion.go`, `docs/json.md`, `AGENTS.md`. Test `internal/cli/detect_test.go`.

**Interfaces — Consumes** Task 1. **Produces:**
- `func detect.Paths(cat *pack.Catalog) []string` (sorted, unique).
- `func detect.Fetch(ctx context.Context, r sys.Runner, repo, branch string, paths []string) (files map[string][]byte, err error)` — temp dir under `$XDG_RUNTIME_DIR` (else `os.TempDir()`), always removed; git per spec; absent/symlink/oversize files are omitted.
- `type detect.Result struct{ Branch string; Pack *PackMatch; Options []OptionMatch }` with `PackMatch{Ref string; Evidence, Others []string}`, `OptionMatch{Key, Ref, Source, Evidence string; Others []string}` (json tags per spec; slices never null).
- `func detect.Choose(cat *pack.Catalog, files map[string][]byte, packRef string) (Result, error)` — pure; rules per spec ("Choose").
- CLI `railswitch --json project detect <repo> [--branch b] [--pack ref]`; without `--json` a short text listing.

- [ ] Tests: `TestChoose*` (pure, from file maps: all-files pack, comment line not matched — rule regexps anchor `^\s*gem`, pack default wins, others, default source, `--pack`, works_with filter), `TestFetchBareRepo` (create a bare repo with git in a temp dir containing Gemfile, a symlinked `config/application.rb`, a 2 MiB `package.json`, an unrelated file; only named, regular, small files returned; temp dir gone; bad branch → error and temp dir gone), `TestProjectDetectCLI` (JSON shape, default branch read).
- [ ] Implement; `make check`; commit `detect: read a repository for its pack and options`.

### Task 3: `project new --dry-run --json` and `--up`

**Files:** Modify `internal/cli/new.go`, `docs/json.md`, `AGENTS.md`. Test `internal/cli/new_test.go`.

**Interfaces — Produces:** JSON `{project_toml, worktree{slug, routes[{host,service}], containers[{name,role}], env[]}, warnings[], steps[]}` (slices never null).

- [ ] Tests: `TestNewDryRunWritesNothing` (no dir, registry unchanged, no units, no git clone call — `git ls-remote` for the default branch is allowed), `TestNewDryRunTOMLMatchesReal` (same flags, real run's `project.toml` bytes == `project_toml`), `TestNewDryRunRefusals` (prefix in use; existing `project.toml`; same messages as real), `TestNewDryRunSummary` (routes/containers/env for the Filial-like fixture), `TestNewUp` (`up` runs after success, not after a failed create).
- [ ] Implement: refactor `cmdNew` so the dry run and the real run share every check and the `project.toml` builder (including `addNewSecrets` on text); render in memory through a `workspace.Workspace` built in memory (`WithProject`-style) and a `machine` entry not saved (next free ID/subnet from the registry, read-only).
- [ ] `make check`; commit `project new: --dry-run --json and --up`.

### Task 4: The `/new` page

**Files:** Create `internal/web/newproject.go`, `internal/web/newproject_test.go`, `internal/web/templates/new.tmpl`; modify `internal/web/server.go`, `internal/web/cli.go` (types), `internal/web/templates/header.tmpl` or the switcher ("New project…"), the root page (link when no project), `internal/web/static/app.js` (a `new` section), `internal/web/static/app.css`, `docs/dashboard.md`.

**Interfaces — Consumes** (via the web `CLI`, faked in tests): `--json project detect <repo> [--branch] [--pack]` (Task 2 JSON), `--json project new <dir> … --dry-run` (Task 3 JSON), `--json pack ls` or the catalog listing the page needs for the pack picker and slot choices — use `railswitch --json pack show`/existing read commands if they list packs and options; if none lists every pack and option with slots and `works_with`, add `railswitch --json pack ls` in this task (document in docs/json.md). Job: `project new <dir> --repo … [--up]` with Spec.Project = new prefix, Slug "".
- Routes: `GET /new`; `POST /new/detect` (form: repo, branch, pack → fragment for step 1); `POST /new/review` (form: all fields → step 2 fragment from the dry run); `POST /new/create` (job). All behind the guard.

- [ ] Tests: `TestNewPageRenders`, `TestNewDetectPost` (argv: repo as one entry; a repo starting with `-` refused 400 without running), `TestNewReviewPost` (argv from the form, dry-run errors shown, Create disabled), `TestNewCreateJob` (exact argv, `--up` when ticked, project-level job keyed by prefix), `TestNewGuard`, `TestNewSwitcherLink`.
- [ ] Implement; drive once with headless Chromium over the fake CLI; `make check`; commit `web: new project page`.

### Task 5: Host acceptance

- [ ] Build and install to `~/.local/bin`; through the live page create a public Rails app not on this host (small; e.g. a Rails 8 sample app), Start ticked; the app answers.
- [ ] `project detect` on Mastodon, Plausible (`--pack local:phoenix@1` is not possible before the workspace exists — record that local packs can't be detected at create), Metabase; record results.
- [ ] A used prefix; an existing folder; record; `docs/stage6c-acceptance.md`; commit `docs: Stage 6c host run`.
