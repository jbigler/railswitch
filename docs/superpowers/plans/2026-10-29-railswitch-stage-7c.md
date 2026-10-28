# Stage 7c Library repositories Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add signed library repositories to a machine, fetch and verify them, and install and update their packages in projects only through 7a's review; lock the nvim tool and the agent base image too.

**Architecture:** `internal/libraries` (libraries.toml, the hardened fetch, staging → pending/current); `pack.Ref.Library` and a catalog that resolves `<alias>:<name>@N` from a verified checkout; 7a's review/lock extended with `added` library refs, `Accept.Install` and a re-verify; `settings preview`'s `install` section and `--accept-install`; the dashboard's Libraries section and Install review.

**Tech Stack:** Go stdlib + `github.com/BurntSushi/toml`; git; OpenSSH `ssh-keygen`.

**Spec:** `docs/superpowers/specs/2026-10-29-railswitch-stage-7c-libraries-design.md`

## Global Constraints

- `make check` before every commit; stdlib + toml only; `TestGenericity`.
- Git calls that touch a remote pass `sys.GitProtocols` and check user values with `sys.GitArg`; every command through `sys.Runner` with a timeout.
- Unit tests: git and ssh-keygen allowed, local bare repos and keys in `t.TempDir()`, XDG dirs in `t.TempDir()`; no network, podman, systemd.
- The dashboard reads through `--json`, changes through jobs; no inline script or style; guard on every POST.
- Docs with every behaviour change: `docs/json.md`, `docs/libraries.md`, `docs/packs.md`, `docs/dashboard.md`, `AGENTS.md`.

## Review Focus

- A library repo with a symlink `packs/x/entrypoints/e.sh -> /etc/passwd`, or a submodule, or a path outside the allowlist: fetch fails, `current` unchanged (Task 2, `TestFetchRefusesSymlink`, `TestFetchRefusesSubmodule`, `TestFetchRefusesPath`).
- A file under `current/` changed after the fetch (by anything): the library ref doesn't resolve (Task 1, `TestCatalogLibraryTampered`).
- `settings apply` adding a library option without `--accept-install`, or with a stale hash: refused, `project.toml` and `stack.lock` byte-identical (Task 3, `TestSettingsApplyInstallRefused`).
- Accepting an install or update after `signer rm` of the library's key: refused (Task 3, `TestAcceptReverifies`).
- A changed built-in nvim Containerfile: pending review, the accepted copy builds (Task 4, `TestToolPending`).

---

### Task 1: Library refs and the catalog

**Files:** `internal/pack/ref.go`, `internal/config/project.go` (ref check), `internal/pack/load.go` (library resolution), `internal/config/lock.go` (`LockEntry.Signer`, `toml:"signer,omitempty"`), `internal/signing` (`PackageHash(dir fs.FS, kind, name string) (string, error)` if not already exposed), `internal/workspace` (lock entries for library refs: source, commit, signer).

**Produces:** `pack.Ref{Name; Version; Local bool; Library string}` — `ParseRef("acme:django@1")`, `String()`; `config.LibraryRef(s) (alias, rest string, ok bool)`; `Catalog.Libraries func(alias string) (dir string, idx *signing.Index, commit string, signer string, err error)` (set by the workspace from `<data>/libraries/<alias>/current/` and its fetch record); a library ref resolves only when the index lists it with the sha its directory hashes to.

- [ ] Tests: `TestParseLibraryRef` (round trip; `local:` and `local` alias refused; uppercase refused), `TestConfigAcceptsLibraryRef`, `TestCatalogLibrary`, `TestCatalogLibraryTampered`, `TestCatalogLibraryUnlisted`, `TestLockLibraryEntry` (source, commit, signer).
- [ ] Implement; `make check`; commit `pack, config: library refs`.

### Task 2: Libraries on a machine and the fetch

**Files:** Create `internal/libraries/{config.go,fetch.go}` + tests; `internal/cli/library.go` (`add`, `fetch`, `rm`, `ls`; `trust` on an alias promotes pending), docs.

**Produces:** `libraries.Load(path) (*Config, error)`, `(*Config).Save`, `Add`, `Remove`; `Fetch(ctx, run sys.Runner, d machine.Dirs, lib *Library, t *signing.Trust) (*FetchResult, error)` (`FetchResult{Status signing.Status; Commit; Serial; Packages int; Pending bool; Reason}`); `Promote(d, alias) error`; on-disk record `<data>/libraries/<alias>/fetch.toml` beside `current/` and `pending/`, exactly: `schema = 1`, `id` (string), `commit` (hex), `serial` (int), `status` (a signing.Status string), `fetched` (RFC 3339), `signer` (fingerprint of the key that signed `current/`'s index; "" when no current), `reason` (string). Task 1 reads it through `libraries.ReadRecord(d machine.Dirs, alias string) (*Record, error)`, which Task 2 owns; until Task 2 lands, Task 1 defines the same struct and reader in `internal/libraries/record.go` and Task 2 keeps it.
- JSON `library ls --json`: `{"libraries":[{"alias","url","branch","id","status","serial","commit","fetched","packages","pending":{"fingerprint","new_fingerprint","status"}|null}]}` (slices never null).
- `library trust <alias> --fingerprint <fp> [--rotation]` (an alias, not a dir): verifies `pending/`, trusts, promotes.

- [ ] Tests: `TestFetchTrusted`, `TestFetchUnknownPending`, `TestFetchTrustPromotes`, `TestFetchRolledBackKeepsCurrent`, `TestFetchRefusesSymlink`, `TestFetchRefusesSubmodule`, `TestFetchRefusesPath`, `TestFetchRefusesSize`, `TestFetchTempRemoved`, `TestFetchIDFixed`, `TestLibraryAddLsRm`, `TestLibraryRmInUse`.
- [ ] Implement; `make check`; commit `libraries: add, fetch and verify library repositories`.

### Task 3: Install and update through the review

**Files:** `internal/workspace/accept.go` (library refs new to the lock are `added` and never locked unaccepted; `Accept.Install []pack.Ref`; re-verify before accepting a library ref), `internal/cli/settings.go` (`preview` `install`, `apply --accept-install`), `internal/cli/new.go` + `new_dryrun.go` (`install`, `--accept-install`), `internal/cli/lock.go`, `internal/lifecycle/overview.go` (`signer_warnings`), docs.

**Consumes** Tasks 1–2. **Produces:** `settings preview --json` gains `install` (the `lock --review` document of the edited project, or `null` when it installs nothing); `settings apply --if-hash <h> --accept-install <review-hash>`; `project new --dry-run --json` gains `install`; `project new --accept-install <hash>`; `overview --json` `signer_warnings[]{ref,library,fingerprint}`.

- [ ] Tests: `TestReviewLibraryAdded`, `TestSettingsApplyInstallRefused`, `TestSettingsApplyInstall`, `TestSettingsApplyInstallStale`, `TestProjectNewInstall`, `TestAcceptReverifies`, `TestLibraryUpdatePending` (a refetch with changed content: project keeps the accepted copy, review `changed`), `TestSignerWarnings`.
- [ ] Implement; `make check`; commit `lock, settings: install and update library packages through the review`.

### Task 4: Tools and the agent base in the lock

**Files:** `internal/workspace` (`stackPackages` with kinds `tool`, `base`), `internal/pack` (`Catalog.WithPackage` for those kinds), `internal/config/lock.go` (`Tools map`, `Base map`), `internal/cli/containerfile.go` (`stackBuiltins` from the accepted catalog), `internal/review` (kinds), docs.

- [ ] Tests: `TestLockTools`, `TestToolPending`, `TestBasePending`, `TestEjectAgentBaseAccepted`.
- [ ] Implement; `make check`; commit `lock: the nvim tool and the agent base`.

### Task 5: Dashboard

**Files:** `internal/web` library page (Libraries section, Add/Fetch/Trust/Confirm rotation jobs, library packages read-only with alias and signer), settings and new-project pages (Install review component reused from `review.tmpl`, ack, `accept_install`), project header (`signer_warnings`), tests, `docs/dashboard.md`.

**Consumes** (faked CLI) `library ls --json`, `settings preview --json` `install`, `project new --dry-run --json` `install`, overview `signer_warnings`; jobs `library add <url> --alias <a> [--branch <b>]`, `library fetch <alias>`, `library trust <alias> --fingerprint <fp> [--rotation]`, `settings apply --if-hash <h> --accept-install <r>`, `project new … --accept-install <r>`.

- [ ] Tests: `TestLibrariesSection`, `TestLibraryAddJob`, `TestLibraryFetchJob`, `TestLibraryTrustJob` (fingerprint from the page must match the current `library ls` pending, else 400), `TestSettingsInstallReview` (ack required when flags > 0), `TestNewInstallReview`, `TestSignerWarningsHeader`, `TestLibrariesGuard`.
- [ ] Implement; Chromium drive over the fake CLI; `make check`; commit `web: libraries and the install review`.

### Task 6: Host acceptance

- [ ] As the spec's host acceptance; record `docs/stage7c-acceptance.md`; commit.
