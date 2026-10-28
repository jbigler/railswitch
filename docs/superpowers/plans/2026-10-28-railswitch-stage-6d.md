# Stage 6d Containerfiles and packages Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Edit the project's Containerfiles and its local packages from the dashboard, with the read-it acknowledgement, contract checks before any write, and an optional rebuild and restart.

**Architecture:** CLI domain commands (`containerfile show --json`, `containerfile write`, `pack file show|write|rm`, `pack new --option`) judge a change by rendering in memory with an overlay, then write through one shared commit step that restores on failure. Two dashboard areas (`/p/{prefix}/images…`, `/p/{prefix}/library…`) share one file editor and run those commands as jobs.

**Tech Stack:** Go stdlib + `github.com/BurntSushi/toml`; `html/template`, vanilla JS.

**Spec:** `docs/superpowers/specs/2026-10-28-railswitch-stage-6d-images-library-design.md`

## Global Constraints

- `make check` before every commit (`gofmt -l` on `cmd internal` empty, `go vet`, `staticcheck`, `go test ./...`).
- Dependencies: stdlib + `github.com/BurntSushi/toml` only.
- Commands: argv only, through `sys.Runner` with a timeout; unit tests need no podman, systemd, gh or network (`systest` Fake).
- Workspace files are read through `safefile.Read` (≤ 1 MiB); writes refuse > 1 MiB and symlinks and are atomic (`tomlfile.WriteAtomic`).
- Genericity: no framework or package name in non-test core code (`TestGenericity`).
- The dashboard reads only through `railswitch --json`, changes only through jobs; file text only as job stdin (`StartInput`), never argv; no inline script or style; every POST through the guard.
- Every behaviour change updates its doc (`docs/json.md`, `docs/dashboard.md`, `docs/packs.md`, `AGENTS.md`).

## Review Focus

- A Containerfile edited on disk between Check and Save: `--if-hash` refuses, nothing written (Task 1, test `TestContainerfileWriteHashMismatch`).
- A package write whose render fails after `stack.lock` was rewritten: the file, `project.toml` and `stack.lock` are byte-identical to before (Task 2, `TestPackFileWriteRestores`).
- `pack file write local:x@1 entrypoints/../../project.toml`, or `entrypoints` a symlink to `/tmp`: refused before any write (Task 2, `TestPackFilePathRefused`).
- An entrypoint edit changes no unit but a mounted asset: the restart list still names the running worktrees (Task 2, `TestPackFileRestartOnAsset`).
- A Save POST without the acknowledgement field: 400, no job (Task 3, `TestImagesSaveNeedsAck`).

---

### Task 1: Containerfile show/write and the shared commit

**Files:** Modify `internal/render/model.go`, `internal/render/images.go` (overlay), `internal/workspace/workspace.go`, `internal/settings/judge.go` (export the comparison), `internal/cli/containerfile.go`, `internal/cli/settings.go` (commit step), `internal/cli/completion.go`, `docs/json.md`, `AGENTS.md`. Tests `internal/cli/containerfile_test.go`, `internal/render/images_test.go`.

**Interfaces — Produces:**
- `render.Inputs.Overlay map[string][]byte` — workspace-relative path → bytes read instead of the disk file (Containerfiles; package assets go through the catalog, Task 2).
- `func (w *Workspace) WithFile(rel string, data []byte) *Workspace` — a copy whose renders see `data` at `rel`.
- `settings.Env.Compare(next *workspace.Workspace) (*Change, error)`, `type Change struct{ Steps []TagChange; Restart []string }`, `type TagChange struct{ Worktree, Name, OldTag, NewTag string }` (json `worktree,name,old_tag,new_tag`); `Restart` = running worktrees whose stamped units or mounted asset bytes differ (extend the existing `restart`). `Preview` uses it for its own `Restart`.
- `func (e *env) commit(ctx context.Context, root string, changes []fileChange, lock bool) error`, `type fileChange struct{ Rel string; Data []byte /* nil: remove */; Mode os.FileMode }`: writes each, then (if `lock`) locks, then renders; on any failure restores every changed file (removing new ones), and `stack.lock` (removing it when none existed). `commitProject` becomes a call to it.
- `parseEjectHeader(line string) (ref, sha string, ok bool)`.

- [ ] Tests: `TestContainerfileShowJSON` (addon: kind, default ref, diff ignores header; `default_changed` after rewriting the header sha; user addon: `default` null, kind `user`), `TestContainerfileWriteDryRun` (changed tag listed, file unchanged, `restart` only running worktrees via Fake `systemctl show`), `TestContainerfileWriteContractError` (no `FROM ${BASE_IMAGE}` → dry run `errors` exit 0; real run exit 1, file unchanged), `TestContainerfileWriteHashMismatch`, `TestContainerfileWriteNotInChain`, `TestContainerfileWriteTooBig`, `TestContainerfileWrite` (file replaced, units rendered, new tag printed), `TestContainerfileWriteRestart` (restart argv for each listed worktree, stops at first failure), `TestCommitRestoresNewLock` (the existing 6b case still passes through `commit`).
- [ ] Implement; `make check`; commit `containerfile: show --json and write`.

### Task 2: Local package files and `pack new --option`

**Files:** Modify `internal/pack/load.go` (catalog from an overlay), `internal/cli/pack.go` (new `pack file` dispatch; `pack show` `files`; `pack new --option`), `internal/cli/completion.go`, `docs/json.md`, `docs/packs.md`, `AGENTS.md`. Test `internal/cli/pack_test.go`.

**Interfaces — Consumes** Task 1 (`commit`, `settings.Env.Compare`). **Produces:**
- JSON: `pack show` `files: [{path,size,executable}]`; `pack file show --json` `{path,text,hash}`; `pack file write --dry-run --json` `{hash,used,steps,restart,errors}` (slices never null).
- `func packFilePath(rel string, manifest string) error` — the D93 allowlist (manifest; `Containerfile.<n>`, `deps.allow`, `README.md`, `<n>.sh` at top; `entrypoints/<f>`, `initdb/<f>`; names `[A-Za-z0-9][A-Za-z0-9._-]*`).
- An in-memory catalog with one local package's file replaced or removed, so the dry run and the "still loads" check write nothing.

- [ ] Tests: `TestPackShowFiles`, `TestPackFileShow` (local and built-in), `TestPackFilePathRefused` (`../x`, `entrypoints/../../project.toml`, `foo/bar`, `.hidden`, a symlinked `entrypoints/`, a symlinked file), `TestPackFileWriteBreaksLoad` (bad `pack.toml` refused, unchanged), `TestPackFileWriteUnused` (written, no lock change), `TestPackFileWriteUsed` (lock's sha changes, units rendered), `TestPackFileWriteRestores` (a render failure after lock: three files identical), `TestPackFileRestartOnAsset`, `TestPackFileShMode` (0755), `TestPackFileAdd` (`--if-hash ""`; refused when it exists), `TestPackFileRm` (manifest refused; entrypoint removed), `TestPackNewOption` (skeleton loads; `pack ls` lists it with its slot).
- [ ] Implement; `make check`; commit `pack: file show/write/rm and new --option`.

### Task 3: Images page and the file editor

**Files:** Create `internal/web/images.go`, `internal/web/images_test.go`, `internal/web/templates/images.tmpl`, `internal/web/templates/editor.tmpl`; modify `internal/web/server.go` (routes), `internal/web/cli.go` (types for Task 1 JSON), `internal/web/templates/header.tmpl` (Images, Library links beside Settings), `internal/web/static/app.js` (an `editor` section: Load from file, Check, ack/Check gating of Save, dirty-after-Check), `internal/web/static/app.css`, `docs/dashboard.md`.

**Interfaces — Consumes** (faked CLI): `--json containerfile list -w <slug>`, `--json containerfile show <name>`, `--json containerfile write <name> --if-hash <h> --dry-run` (stdin), jobs `containerfile write <name> --if-hash <h> [--restart]` (stdin), `containerfile eject <name> --force`, `containerfile new <name>`. **Produces:** `editor.tmpl` taking `{Title, Text, Hash, Diff, Notice, CheckURL, SaveURL, DeleteURL, AckRequired}` for Task 4.
- Routes: `GET /p/{prefix}/images[?wt=]`, `GET /p/{prefix}/images/{name}`, `POST …/images/{name}/check` (fragment), `POST …/images/{name}/save` (job; requires `ack=1`), `POST …/images/{name}/reset` (job), `POST /p/{prefix}/images/new` (job).

- [ ] Tests: `TestImagesPage` (steps, warnings, worktree picker), `TestImagesEditor` (text, diff, `default_changed` notice), `TestImagesCheck` (argv, stdin == text, errors shown), `TestImagesSave` (exact argv, `--restart` when ticked, stdin == text, project-level job), `TestImagesSaveNeedsAck`, `TestImagesHostileText` (`</textarea><script>` escaped), `TestImagesGuard` (every POST 403 without token or with a foreign Origin, no command run), `TestImagesNameRefused` (`../x` 404 without running).
- [ ] Implement; drive once with headless Chromium over the fake CLI; `make check`; commit `web: images page and Containerfile editor`.

### Task 4: Library page

**Files:** Create `internal/web/library.go`, `internal/web/library_test.go`, `internal/web/templates/library.tmpl`; modify `internal/web/server.go`, `internal/web/cli.go`, `internal/web/static/app.js`, `docs/dashboard.md`.

**Interfaces — Consumes** Task 2 JSON and Task 3's `editor.tmpl`; jobs `pack file write <ref> <path> --if-hash <h> [--restart]` (stdin), `pack file rm <ref> <path> --if-hash <h>`, `pack eject <ref> --as <name>`, `pack new <name>`, `pack new --option <slot> <name>`.
- Routes: `GET /p/{prefix}/library[?ref=]`, `GET /p/{prefix}/library/{ref}/{path...}`, `POST …/check|save|rm`, `POST /p/{prefix}/library/new`.

- [ ] Tests: `TestLibraryPage` (packs/options/agents, used marks, local files), `TestLibraryBuiltinReadOnly` (no Save), `TestLibrarySave` (argv, stdin, ack required except README.md), `TestLibraryNew` (copy and blank, pack and option argv), `TestLibraryPathRefused`, `TestLibraryGuard`.
- [ ] Implement; Chromium drive; `make check`; commit `web: library page`.

### Task 5: Host acceptance

- [ ] Build, install to `~/.local/bin`; through the live page: Mastodon `Containerfile.pg-client` (add a package line), Check, Save with restart → `stale: false`, app 200; Reset to default; Plausible `packs/phoenix/entrypoints/entrypoint-app.sh` (a comment line) → re-locked, restart, app 200; New option copied from `redis@1`.
- [ ] Record in `docs/stage6d-acceptance.md`; commit `docs: Stage 6d host run`.
