# Stage 7a Update review Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A package change (railswitch upgrade, a local edit outside the dashboard) is reviewed as per-file diffs with flagged items before it is locked; until then the project runs what it last accepted.

**Architecture:** A machine-wide content-addressed store of accepted packages (`internal/lockstore`); the workspace loader swaps a changed package for its accepted copy; a pure comparer with checks (`internal/review`); `lock --review|--accept` on top; a dashboard review page that runs them.

**Tech Stack:** Go stdlib + `github.com/BurntSushi/toml`; `html/template`, vanilla JS.

**Spec:** `docs/superpowers/specs/2026-10-28-railswitch-stage-7a-update-review-design.md`

## Global Constraints

- `make check` before every commit (`gofmt -l` empty, `go vet`, `staticcheck`, `go test ./...`).
- Dependencies: stdlib + `github.com/BurntSushi/toml` only.
- Unit tests need no podman, systemd, gh or network (`systest` Fake); store tests use `t.TempDir()` as `XDG_DATA_HOME`.
- Genericity: no framework, package or registry name in non-test core code (`TestGenericity`).
- The dashboard reads only through `railswitch --json`, changes only through jobs; no inline script or style; every POST through the guard; the page never sends file text.
- Every behaviour change updates its doc (`docs/json.md`, `docs/dashboard.md`, `docs/packs.md`, `AGENTS.md`).
- Commits end with the session's `Co-Authored-By` and `Claude-Session` lines; stage files by path.

## Review Focus

- A Settings save (or `agent add`, `project write`) while a built-in update waits: `stack.lock`'s entry for that package is unchanged afterwards (Task 2, `TestLockKeepsPending`).
- A store copy whose bytes were altered on disk: read as missing, never rendered (Task 1 `TestStoreTampered`, Task 2 `TestLoadMissingCopy`).
- `lock --accept` with the hash of an earlier review after the package changed again: refused, nothing written (Task 3, `TestLockAcceptStale`).
- `pack file write` of local package A while built-in B waits: A's entry advances, B's doesn't (Task 2, `TestLockAcceptOnly`).
- Accept POST without `ack` when flags > 0, or with a hash that no longer matches the review: 400, no job (Task 4, `TestReviewAcceptNeedsAck`, `TestReviewAcceptStaleHash`).

---

### Task 1: `internal/lockstore` and `internal/review`

**Files:** Create `internal/lockstore/store.go`, `internal/lockstore/store_test.go`, `internal/review/review.go`, `internal/review/checks.go`, `internal/review/review_test.go`. Modify `internal/machine` dirs if a `Locked` path helper belongs there.

**Interfaces — Produces:**
- `lockstore.Dir(dirs machine.Dirs) string` (`<data>/locked`); `type Store struct{ Dir string }`; `func (s Store) Put(files fs.FS) (string, error)`; `func (s Store) Get(sha string) (fs.FS, error)`; `var ErrMissing`. Limits: 1 MiB per file, 256 files. `sha` is `pack.ContentHash`'s hex.
- `review.Compare(old, new fs.FS) ([]File, error)` (nil `old`: all added); `type File struct{ Path, Status, Diff string; Flags []Flag }` (json `path,status,diff,flags`; `Flags` never null); `type Flag struct{ Check string; Line int; Text string }` (json `check,line,text`). Checks per the spec table, keyed by file name and manifest (`pack.toml`, `option.toml`, `tool.toml`, `agent.toml`); manifests decoded into `pack`'s format types and compared field by field. A unified diff helper exists in `internal/cli/containerfile.go` or `render/diff.go`: reuse it (move to a shared place if needed).

- [ ] Tests: `TestStoreRoundTrip`, `TestStoreTampered`, `TestStorePutIdempotent`, `TestStoreRefusesSymlink`, `TestStoreLimits`; review: one positive and one negative per check (`firewall` deps.allow host, agent `allow`, `firewall` profile; `runs-here` `curl … | sh`, `wget -O- … | bash`, `ADD https://`, `--network=host`; `image` `FROM`, `pull`; `host-mount` `/abs` and `{worktree.dir}` flagged, package file not; `port`; `privilege` capability and `.sh` of a capability package), `TestCompareNilOld`, `TestCompareReorderedManifest` (no flag), `TestCompareCommentedCurl` (flagged), `TestCompareBadManifest`.
- [ ] Implement; `make check`; commit `lockstore, review: accepted package store and update checks`.

### Task 2: Run what was accepted; locks store and keep pending

**Files:** Modify `internal/pack/load.go` (`Catalog.WithPackage`), `internal/workspace/workspace.go` (`load`, `Lock`, `CheckLock` hint, `Pending`), every `workspace.Lock` caller (`internal/cli/settings.go` `commit`, `new.go`, `agent_add.go`, `render.go`, `pack_file.go`), `internal/lifecycle/overview.go` + status (`review_pending`), `docs/json.md`. Tests `internal/workspace/*_test.go`, `internal/cli/*_test.go`.

**Interfaces — Consumes** Task 1. **Produces:**
- `func (c *Catalog) WithPackage(kind string, r Ref, files fs.FS) (*Catalog, error)` — kind `pack|option|agent`; the copy loads and checks `files` as that package at `r` (built-in or local).
- `Workspace.Pending []pack.Ref` (sorted); load replaces each same-ref, different-content package with `Store.Get(lock sha)` when usable, then resolves again; unusable → left as is, `CheckLock`'s error adds "an update is waiting for review: railswitch lock --review".
- `type Accept struct{ All bool; Refs []pack.Ref }`; `func Lock(ctx context.Context, root string, accept Accept) error` — resolves on the raw catalog; per package: accepted (All or in Refs) or new to the lock → the resolved entry; else the current entry kept when its store copy is usable, else resolved. Every entry written is `Put` first.
- `func Review(ctx context.Context, root string) (*Plan, error)` — the raw resolution vs `stack.lock`: `Plan{Lock []byte /* the bytes accept writes */; Packages []Change}`, `Change{Ref pack.Ref; Kind, Status string; From, To string; Old, New fs.FS}` (`Old` nil for `added`/`first`). Task 3 builds JSON on it.

- [ ] Tests: `TestLoadRunsAccepted` (a built-in changed under the lock — swap `LoadBuiltins` via a test catalog — renders units byte-identical to before; `Pending` names it), `TestLoadMissingCopy` (hint in the error), `TestLoadRefChange` (version changed in project.toml: stale, hint), `TestLockKeepsPending`, `TestLockAcceptOnly`, `TestLockStores` (`project new`, `settings apply`, `pack file write` leave the store holding every pinned sha), `TestOverviewReviewPending`.
- [ ] Implement; `make check`; commit `workspace: run accepted packages; locks store and keep waiting updates`.

### Task 3: `lock --review` and `lock --accept`

**Files:** Modify `internal/cli/render.go` (`cmdLock`), `internal/cli/completion.go`, `docs/json.md`, `docs/packs.md` (the update section), `AGENTS.md` (CLI Stage 7a additions; package map: lockstore, review). Test `internal/cli/lock_test.go`.

**Interfaces — Consumes** Tasks 1–2 (`workspace.Review`, `review.Compare`, `commit`, `settings.Env.Compare`). **Produces** the spec's `lock --review --json` document (`hash`, `flags`, `packages[]{ref,kind,status,from,to,also_locked_in,files}`, `restart`; slices never null), `lock --accept <hash> [--restart]` (exit 3 written-restart-failed), plain `lock` refusing a non-empty review (exit 1, text review, the accept command).

- [ ] Tests: `TestLockReviewJSON` (changed built-in: files, flags, hash == `workspace.Hash` of the plan's lock bytes; `also_locked_in` from a second registered project), `TestLockReviewFirst` (no store copy: status `first`, every file added), `TestLockPlainRefuses` (exit 1, lock unchanged, output names `--accept sha256:`), `TestLockPlainNoChange`, `TestLockAcceptStale`, `TestLockAccept` (lock written, store holds the new sha, units rendered, `Pending` empty), `TestLockAcceptRestart`.
- [ ] Implement; `make check`; commit `lock: --review and --accept`.

### Task 4: Dashboard review page

**Files:** Create `internal/web/review.go`, `internal/web/review_test.go`, `internal/web/templates/review.tmpl`; modify `internal/web/server.go`, `internal/web/cli.go` (types for the review JSON and `review_pending`), `internal/web/templates/header.tmpl` or the project page (banner), the switcher, `internal/web/static/app.js` (ack gating of Accept), `app.css`, `docs/dashboard.md`.

**Interfaces — Consumes** (faked CLI) `--json lock --review -C <ws>`, overview `review_pending`; job `lock --accept <hash> [--restart]`.
- Routes: `GET /p/{prefix}/review`, `POST /p/{prefix}/review/accept`.

- [ ] Tests: `TestReviewBanner`, `TestReviewPage` (packages, flagged files open, flag rows), `TestReviewHostileText` (`</pre><script>` escaped), `TestReviewAccept` (exact argv, `--restart` when ticked, no stdin), `TestReviewAcceptNeedsAck`, `TestReviewAcceptStaleHash`, `TestReviewAcceptNoFlags` (no ack needed), `TestReviewGuard`.
- [ ] Implement; drive once with headless Chromium over the fake CLI; `make check`; commit `web: review page`.

### Task 5: Host acceptance

- [ ] Install the branch build, then `railswitch lock` in Mastodon and Plausible (stores the accepted copies). Build a variant whose built-in rails pack differs (a test-only change, e.g. a comment line in `deps.allow` plus a host); install it; Mastodon `up` still runs (units unchanged), banner shows; review on the page (first: compared against the stored copy, the host flagged), Accept with restart, 200. Plausible: add a `curl … | sh` RUN line to `packs/phoenix/Containerfile.base` in a text editor; review shows it flagged; reject by restoring the file. Reinstall the real build; record the first-review flag count for a pre-7a lock.
- [ ] Record in `docs/stage7a-acceptance.md`; commit `docs: Stage 7a host run`.
