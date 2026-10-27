# Stage 4 acceptance

This is the run procedure for the Stage 4 acceptance: agents, the nvim config
repo, git-guard and `db dump`. Run it on a real host, not in a sandbox. The
reference host is CachyOS/Arch.

## Prerequisites

- Everything in docs/stage3-acceptance.md "Prerequisites" and "From a clean
  machine": podman ≥ 5 with netavark ≥ 1.7.0, a systemd user session,
  `podman.socket` enabled, `curl`, `railswitch doctor` all OK (a `git-guard`
  warning is fine; it never changes the exit code).
- Build and install from the `stage-4` branch:
  `git switch stage-4 && git pull && make build && install -m755 railswitch ~/.local/bin/`.
- A generated app at `~/tmp/rs-demo/demo` (docs/stage2-acceptance.md Step 1).
- No project with prefix `s4a` registered (`railswitch project ls`).
- Network access to `example.com` and `api.anthropic.com` from the host (the
  firewall check expects the first allowed and the second refused).
- Upgrading from Stage 3: a `project.toml` with `[tools] nvim = true` is
  refused; replace it with an empty `[tools.nvim]` table (D54), or one with
  `config_repo`/`config_ref`. Then `railswitch lock` if `up` says
  `stack.lock` is stale.

## Two agents in one project (`TestE2EAgents`)

```sh
RAILSWITCH_E2E_REPO=$HOME/tmp/rs-demo/demo RAILSWITCH_E2E_PREFIX=s4a \
  go test -tags integration ./internal/lifecycle/ -run E2EAgents -timeout 60m -v
```

The prefix defaults to `s4a` and must match `^[a-z][a-z0-9]{1,15}$`. The test
builds railswitch from source, runs `project new` (default agent `claude@1`),
copies `internal/lifecycle/testdata/agents/probe` to `<ws>/agents/probe`,
appends `[agents.probe] use = "local:probe@1"`, `run = "service"`, then runs
`lock`, `containerfile eject probe`, `secret add probe_key --service
agent-probe` and `secret set claude_token` (values on stdin; a dummy token,
`claude --version` needs none) and `up`. It checks:

1. After `up`, `<p>-<slug>-agent-probe.service` is not `active` (`up` never
   starts an agent, D62); no `<p>-<slug>-agent-claude.container` unit file
   exists (on-demand). The test then runs `systemctl --user start
   <p>-<slug>-agent-probe.service`, what `railswitch agent probe` does before
   it attaches (the attach needs a terminal).
2. `/run/secrets/probe_key` exists in the probe, and `CLAUDE_CODE_OAUTH_TOKEN`
   is not set there (each agent only its own secrets).
3. The probe's annotations: `<p>.firewall=agent-probe`,
   `<p>.firewall.mode=enforce`.
4. From the probe, `curl https://api.anthropic.com` fails (exit 7 or 28) and
   `curl https://example.com` succeeds.
5. `railswitch --json agent ls` lists `claude` (`on-demand`, `"running": false`)
   and `probe` (`service`, `"running": true`).
6. One running agent per worktree (D62): `railswitch agent claude -- --version`
   fails naming `railswitch agent stop probe`; `railswitch agent stop probe`
   exits 0; then `railswitch agent claude -- --version` exits 0 and its last
   line is a version, and `railswitch agent claude -- sh -c 'test ! -e
   /run/secrets/probe_key'` exits 0 (a first argument without `-` takes the
   entrypoint's `exec "$@"`).

Cleanup is `project rm <p> --yes --purge`, also when the test fails.

## Live checks (by hand, on the Filial project or any project)

### The on-demand agent's transient is enforced

In one terminal start the agent: `railswitch agent` (or `agent <name>`). In
another, with `<p>`, `<slug>` and `<n>` filled in:

```sh
podman inspect --format '{{index .Config.Annotations "<p>.firewall"}} {{index .Config.Annotations "<p>.firewall.mode"}}' <p>-<slug>-agent-<n>
```

Expect `agent-<n> enforce`, whatever `[firewall] mode` the project sets
(D55, C7). The hook loads that profile's allow file:

```sh
ls -l <ws>/.railswitch/firewall/agent-<n>.allow   # exists, holds the package allow + [agents.<n>] allow + [firewall] allow.agent
grep 'profiles=agent-<n> mode=enforce' <ws>/.railswitch/firewall/hook.log | tail -1   # the hook's line for this start
```

and a host outside the allow list is refused from the agent's shell
(`curl -sS -m 5 https://example.org` exits 7 or 28).

### db round trip

```sh
railswitch db dump                 # writes <ws>/db-dumps/<db>.dump (0644; db-dumps 0755)
ls -l <ws>/db-dumps/
railswitch stop                    # stops the worktree; Quadlet removes the containers, volumes stay
podman volume ls --format '{{.Name}}' | grep -- '-db-data-pg'   # the exact name: <p>-<slug>-db-data-pg<major>
podman volume rm <p>-<slug>-db-data-pg<major>                   # only the db volume
railswitch up                      # the fresh volume restores from the dump (initdb restore-dump.sh)
```

Do not use `railswitch down --yes` here: it removes every per-worktree
volume (`node_modules`, agent homes with their credentials, seeded volumes),
not just the db's. Expect the app's data back (e.g. a record created before
the dump is listed again).

### git-guard: hooks are read-only in containers

Run these from the base worktree (in a linked worktree `/app/.git` is a file,
so `touch .git/hooks/x` fails with "Not a directory", a false pass), with an
on-demand agent `<n>` (a service agent refuses arguments):

```sh
railswitch exec -- touch .git/hooks/x       # app (base worktree): fails, read-only file system
railswitch nvim                             # then in nvim: :!touch .git/hooks/x  -> fails
railswitch agent <n> -- sh -c 'touch /app/.git/hooks/x'   # fails, read-only file system
railswitch hooks check                      # lists findings; same as doctor's git-guard warning
railswitch agent <n> -- sh -c 'echo /tmp > /app/.git/commondir'  # then: railswitch hooks check -> commondir finding; rm .git/commondir
```

In a linked worktree, `<base>/.git/hooks` is read-only at its host path too
(C11): `touch <base>/.git/hooks/x` from inside the nvim or agent container
fails. On the host, `touch` still works (nothing refuses there, D60).

## Filial run (user, host)

1. Stop the old stack (only one Traefik can own `127.0.0.1:80`).
2. `railswitch project new <ws> --repo <filial repo> --prefix filial --option <slot>=<ref> …`
   with the option slots Filial uses (`--tool nvim` for nvim).
3. In `<ws>/project.toml`: the `[image] addons` and env settings Filial
   needs; `[tools.nvim] config_repo`/`config_ref` if you keep the nvim config
   in a repo. Then `railswitch secret set …` for every declared secret.
4. `railswitch wt add <branch>` for each branch you need.
5. `railswitch up`; the app at http://master.filial.localhost/.
6. `railswitch agent`; `railswitch nvim`.
7. `railswitch db dump`, then `railswitch stop`, `podman volume rm` of the
   exact db volume (`<prefix>-<slug>-db-data-pg<major>`; never `down --yes`,
   which removes every worktree volume), and `railswitch up` again to
   restore; check the data is back (as in "db round trip").

Every gap goes through a fix task (a generic feature plus its test) and into
the gap log; nothing Filial-specific goes into the code or built-in packs
(`TestGenericity`).

### Gap log

| Gap | Generic feature | Commit |
|-----|-----------------|--------|

## Done checklist (roadmap, Stage 4)

- [ ] the Filial repo runs end to end from a fresh `new`;
- [ ] `grep -ri filial` over the code and built-in packs finds nothing outside test fixtures;
- [ ] `grep -ri claude` over the code finds nothing outside `internal/pack/builtin/agents/claude/`;
- [ ] two agents in one project (the built-in `claude@1` and a minimal test agent package, `CMD ["sh"]`, one `run = "on-demand"`, one `run = "service"`) build on one shared agent base and run through the same `railswitch agent <name>` path, each with only its own secrets and allow list;
- [ ] at most one agent runs in a worktree (D62): `up` starts none, `railswitch agent <name>` refuses while another runs, `railswitch agent stop` stops it;
- [ ] every gap Filial hit is closed by a generic feature.

## Run log
