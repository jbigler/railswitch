# Shared agent memory: progress log

(Summary at the top is written last.)

## M1: environment, repo, conventions (2026-10-31)

- VM: KVM guest (Omarchy/Arch, kernel 7.2.8), 6 CPUs, 7 GiB RAM, 13 GiB free
  on /home. Already installed: Go 1.27.3 (go.mod: 1.27.3), podman 6.1.4
  with Quadlet, nft, jq, git, curl, ssh-keygen. Nothing had to be installed
  for `railswitch doctor`.
- Repo: cloned https://github.com/jbigler/railswitch to `~/railswitch`
  (at 12e724b), branch `shared-memory`. `make build` builds `./railswitch`.
- `./railswitch doctor` before any change: every check `ok` except three
  warnings that don't fail it: dashboard not enabled, dashboard not
  answering (not started), linger off. VM-specific: none of these matter
  for the work; the dashboard was not started.
- VM state found: the registry (`~/.local/share/railswitch/registry.toml`)
  holds a project `rhdemo` whose workspace (under another session's /tmp
  scratchpad) no longer exists, with its unit files still in
  `~/.config/containers/systemd/` and no containers or volumes. Left alone.
- Conventions noted: decisions are rows `| Dnn | Topic | Decision | Why |` in
  `docs/superpowers/plans/2026-10-25-railswitch-roadmap.md` (last: D135);
  per-stage additions are a section in `AGENTS.md`; every `--json` read
  command is listed in AGENTS.md's D40 line and has a `## ` section in
  `docs/json.md` (example, then a field table); built-in packages under
  `internal/pack/builtin/` are locked and reviewed (D96, D107: the agent base
  is `[base.agent]` in `stack.lock`), so a change to them shows up as a
  pending review in every project that uses them; hostile files go through
  `safefile`; every command through `sys.Runner` with a timeout; unit tests
  use `systest.Fake` (no podman, systemd or network).
