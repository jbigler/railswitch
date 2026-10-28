# Stage 7d Templates Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Save a project as a template (`<name>.stack/`) and start new projects from one (on this machine or from a signed library), answering its questions; a project records its template and is told, never forced, when it changes.

**Architecture:** `internal/template` (format, decode, export from a workspace, apply to a new project's `project.toml`); `template ls|show|export|check` in `internal/cli`; `project new --template --answer` on 6c's `planNew`/`writeProject`; library templates through 7c's checkout and 7b's index (`kind = "template"`); dashboard Templates tab, Save as template, New project from a template.

**Tech Stack:** Go stdlib + `github.com/BurntSushi/toml`.

**Spec:** `docs/superpowers/specs/2026-10-29-railswitch-stage-7d-templates-design.md`

## Global Constraints

- `make check` before every commit; stdlib + toml only; `TestGenericity`.
- Template files read through `safefile.Read`; no symlinks; ≤ 1 MiB each; strict decode, `schema = 1`.
- Secret values never in a template, a job argv or output (D33).
- Unit tests: git and ssh-keygen allowed; temp XDG dirs; no podman, systemd, network.
- Dashboard: `--json` reads, jobs for changes, no inline script/style, guard on POST.
- Docs: `docs/json.md`, new `docs/templates.md`, `docs/libraries.md`, `docs/dashboard.md`, `AGENTS.md`.

## Review Focus

- An exported template contains no `[project]` line, no secret value and no Containerfile identical to its default (Task 1, `TestExportDropsProjectLines`, `TestExportOnlyEditedContainerfiles`).
- `--answer` for a key the template doesn't ask, or an option answer that doesn't work with the pack: refused before any clone (Task 2, `TestNewTemplateBadAnswer`).
- A library template's Containerfile reaching a build without the install review: refused without `--accept-install` (Task 2, `TestNewLibraryTemplateNeedsReview`).
- A template directory with a symlink or an off-list path: refused at ls/show/new (Task 1, `TestTemplateRefusesSymlink`).
- `template check` after a library fetch changed the template: `template_update` true, diff shown, nothing in `project.toml` changed (Task 3, `TestTemplateCheck`).

---

### Task 1: `internal/template` and `template ls|show|export`

**Files:** Create `internal/template/{format.go,export.go,apply.go}` + tests; `internal/cli/template.go` + tests; `internal/signing` index kind `template`; `internal/libraries` fetch allowlist `templates/<name>/` (stack.toml, README.md, containerfiles/Containerfile.<n>); docs.

**Produces:** `template.Template{Schema; Name; Version; Description; Lines *config.Project /* without [project] */; Ask []Ask; Secrets []string; Containerfiles map[string][]byte; Files fs.FS}`; `template.Decode(fs.FS) (*Template, error)`; `template.Export(w *workspace.Workspace, name string, version int) (*Template, []string /* dropped */, error)`; `(*Template).Write(dir string) error`; `template.Apply(t *Template, answers map[string]string) (*config.Project, error)`; `template.Resolve(ref string, d machine.Dirs, libs pack.Libraries) (*Template, string /* sha */, error)`.
- JSON `template ls --json`: `{"templates":[{"ref","name","version","description","source","packages":[…]}]}`; `template show <ref> --json`: `{"ref","sha256","stack_toml","ask":[{"key","prompt","default"}],"secrets":[…],"containerfiles":[…],"readme"}`.

- [ ] Tests: `TestDecodeStrict`, `TestTemplateRefusesSymlink`, `TestExportDropsProjectLines`, `TestExportQuestions`, `TestExportOnlyEditedContainerfiles`, `TestExportVersionExists`, `TestApplyAnswers`, `TestTemplateLsShow`, `TestLibraryTemplateIndexed` (BuildIndex lists it; Verify signs it).
- [ ] Implement; `make check`; commit `template: format, export, ls and show`.

### Task 2: `project new --template`

**Files:** `internal/cli/new.go`, `new_dryrun.go`, `internal/config/project.go` (`[template] ref, sha256`), `internal/workspace/accept.go` (a library template's content in the install review: kind `template`, stored, accepted with the packages), docs.

- [ ] Tests: `TestNewTemplateDryRun` (exact project_toml: template lines, answers, flags on top; `template` in the JSON), `TestNewTemplateContainerfiles` (real run: the template's file replaces the ejected one), `TestNewTemplateBadAnswer`, `TestNewLibraryTemplateNeedsReview`, `TestNewTemplateRecorded` (`[template]` in project.toml).
- [ ] Implement; `make check`; commit `project new: start from a template`.

### Task 3: `template check`

**Files:** `internal/cli/template.go`, `internal/lifecycle/overview.go` (`template_update`), docs.

- [ ] Tests: `TestTemplateCheck`, `TestTemplateCheckAccept`, `TestTemplateCheckNoTemplate`.
- [ ] Implement; `make check`; commit `template: check for updates`.

### Task 4: Dashboard

**Files:** `internal/web` library page (Templates tab), settings (Save as template), new project (Start from a template, questions, review), project header (template update), tests, `docs/dashboard.md`.

**Consumes** (faked CLI): `template ls --json`, `template show <ref> --json`, `project new --dry-run … --template <ref> --answer k=v --json` (with `template` and `install`), overview `template_update`; jobs `template export --name <n> --version <N>`, `project new … --template <ref> --answer k=v… [--accept-install <h>]`, `template check --accept <sha>`.

- [ ] Tests: `TestTemplatesTab`, `TestSaveAsTemplateJob`, `TestNewFromTemplate` (argv exact, each answer its own argv entry), `TestTemplateUpdateNotice`, `TestTemplatesGuard`, `TestTemplateHostile`.
- [ ] Implement; Chromium drive; `make check`; commit `web: templates`.

### Task 5: Host acceptance

- [ ] As the spec; `docs/stage7d-acceptance.md`; commit.
