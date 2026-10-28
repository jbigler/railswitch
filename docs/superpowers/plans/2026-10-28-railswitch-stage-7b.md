# Stage 7b Signers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Verify a library's signed index against per-library trusted SSH keys, with vouched rotation, revocation and rollback protection; manage trust from the CLI and the machine page.

**Architecture:** `internal/signing` (index format, trust store, `Verify` calling `ssh-keygen -Y` through `sys.Runner`, fingerprints and SSHSIG parsing in Go); `signer` and `library` command groups in `internal/cli`; a Signers section on the dashboard's machine page.

**Tech Stack:** Go stdlib + `github.com/BurntSushi/toml`; OpenSSH `ssh-keygen` (≥ 8.2 for `-Y`).

**Spec:** `docs/superpowers/specs/2026-10-28-railswitch-stage-7b-signers-design.md`

## Global Constraints

- `make check` before every commit; stdlib + toml only; genericity test.
- Commands argv only through `sys.Runner` with a timeout (`library sign`: `sys.NoTimeout`, terminal attached).
- Unit tests may run real `ssh-keygen` (like `git`); keys generated in `t.TempDir()` with `-N ''`; no network.
- Files read through `safefile.Read`; `signers.toml` 0600, `schema = 1` (D39), atomic.
- The dashboard reads through `--json`, changes through jobs; the public key only as job stdin.
- Every behaviour change updates `docs/json.md`, `AGENTS.md`, a new `docs/libraries.md` (publisher guide), `docs/dashboard.md`.

## Review Focus

- A key trusted for library A signing library B's index: never `trusted` (Task 1, `TestVerifyOtherLibraryKey`).
- An index re-signed by a revoked key after `[[revoke]]` was applied: `revoked` (Task 1, `TestVerifyRevokedKey`).
- An older, validly signed index (lower serial): `rolled-back`, `last_serial` unchanged (Task 1, `TestVerifyRollback`).
- `library trust --fingerprint` with a fingerprint other than the one just reported: refused, `signers.toml` unchanged (Task 2, `TestLibraryTrustWrongFingerprint`).
- A `library.toml` whose `library` names a different id than the trust entry it verified against: never `trusted` (Task 1, `TestVerifyLibraryIDBound`).

---

### Task 1: `internal/signing`

**Files:** Create `internal/signing/{index.go,trust.go,verify.go,sshsig.go}` and tests.

**Produces:** `ParseIndex([]byte) (*Index, error)`; `Index{Schema int; Library string; Serial int64; Packages []IndexPackage; Rotate []Rotation; Revoke []Revocation}`; `Fingerprint(authorizedKey string) (string, error)`; `SignerOf(armored []byte) (key string, err error)`; `LoadTrust(path) (*Trust, error)`, `(*Trust).Save(path)`, `Add(lib, key)`, `Remove(lib, fp)`; `Verify(ctx, run sys.Runner, dir string, t *Trust) (*Result, error)` (statuses `trusted|unknown-signer|rotation|rolled-back|revoked|bad-signature`; on `trusted` it updates `t` in memory: serial, revocations — the caller saves); `BuildIndex(dir, lib string, serial int64, keep *Index) (*Index, error)` and `(*Index).Encode() []byte`.

- [ ] Tests: `TestFingerprintMatchesSSHKeygen`, `TestSignerOf`, `TestVerifyTrusted`, `TestVerifyUnknownSigner`, `TestVerifyOtherLibraryKey`, `TestVerifyLibraryIDBound`, `TestVerifyTampered`, `TestVerifyPackageChanged`, `TestVerifyUnlistedPackage`, `TestVerifyRollback`, `TestVerifyRevokedKey`, `TestVerifyRotation`, `TestVerifyRotationUntrusted`, `TestVerifyAppliesRevoke`, `TestParseIndexStrict` (bad id, `local:` ref, duplicate ref, schema 2), `TestTrustFileMode`.
- [ ] Implement; `make check`; commit `signing: library index, trust and verify`.

### Task 2: `signer` and `library` commands

**Files:** Create `internal/cli/signer.go`, `internal/cli/library.go` and tests; modify `cli.go` dispatch, `completion.go`, `internal/doctor` (ssh-keygen -Y check), `internal/lifecycle/overview.go` (`signer_warnings: []`), docs.

- [ ] Tests: `TestSignerAddLsRm`, `TestSignerAddRevokedRefused`, `TestSignerAddStdin`, `TestLibraryIndexSignVerify` (round trip with a temp key), `TestLibraryVerifyJSON`, `TestLibraryTrustUnknown`, `TestLibraryTrustWrongFingerprint`, `TestLibraryTrustRotation`, `TestLibraryRotate`, `TestDoctorSSHKeygen`.
- [ ] Implement; `make check`; commit `signer, library: trust and verify commands`.

### Task 3: Signers on the machine page

**Files:** `internal/web` machine page handler and template, `cli.go` types, tests, `docs/dashboard.md`.

- [ ] Tests: `TestSignersSection`, `TestSignerAddJob` (argv `signer add <lib> -`, stdin == key), `TestSignerRmJob`, `TestSignersGuard`, `TestSignersHostile` (escaped).
- [ ] Implement; `make check`; commit `web: signers on the machine page`.

### Task 4: Host acceptance

- [ ] Branch build installed; a library folder in the scratchpad with `packs/demo/` (an eject of `rails@1` renamed); `library index`, `sign` (a fresh key), `verify` → unknown-signer; `library trust`; verify → trusted; a byte changed → not signed; `library rotate` + re-sign with the new key → rotation → trust → trusted; `[[revoke]]` the old key, sign, verify → revoke applied; an older copy of the index → rolled-back; the machine page shows, adds and distrusts. Record `docs/stage7b-acceptance.md`; commit.
