# Stage 2 acceptance: a fresh Rails project end to end

This is the run procedure for the Stage 2 acceptance. Run it on a real host,
not in a sandbox. The reference host is CachyOS/Arch.

## Prerequisites

- podman ≥ 5, with the Quadlet generator at `/usr/lib/podman/quadlet`.
- A systemd user session (`systemctl --user` works). `loginctl enable-linger`
  is not required: the session is open while you run the steps.
- `curl` and `git` on the host. The host does not need Ruby.
- A railswitch binary on `PATH`: `make build && install -m755 railswitch ~/.local/bin/`.
- No project with prefix `demo` registered yet, and ideally no other worktree
  on the machine (see the note on ports in Step 7).

## Steps

### 1. Generate a fresh app without host Ruby

```bash
podman run --rm -v /tmp/rs-demo:/w:Z -w /w docker.io/library/ruby:3.4 \
  sh -c 'gem install rails && rails new demo -d postgresql && cd demo && git config --global user.email t@t && git config --global user.name t && git add -A && git commit -qm init'
```

Expected: `/tmp/rs-demo/demo/.git` exists. Note which branch it is on
(`git -C /tmp/rs-demo/demo branch --show-current`): the base worktree's slug
is that branch, `main` in the commands below. Use `master` instead if that is
what `rails new` created.

### 2. Doctor

```bash
railswitch doctor
```

Expected: every check is OK. If the port 80 check fails (port 80 is
privileged), run `railswitch allow-ports` first, then re-run `doctor`.

### 3. New project, base worktree up

```bash
railswitch new ~/workspace/rs-demo --repo /tmp/rs-demo/demo --prefix demo
cd ~/workspace/rs-demo/main && railswitch up
```

Expected: both exit 0. `up` waits for the app's `/up` health check (D34).

### 4. The app answers

```bash
curl -sS -o /dev/null -w '%{http_code}' http://main.demo.localhost/
```

Expected: `200`. If Rails host authorization or the bind address blocks it,
the fix belongs in the rails pack (`BINDING`), or in the project's `[env]`
for an app-specific setting. Never in core.

### 5. The app reaches its database

```bash
railswitch exec -- bin/rails runner 'puts ActiveRecord::Base.connection.select_value("select 1")'
```

Expected: `1`.

### 6. Firewall

```bash
railswitch fw show
```

Expected: `demo-main-rails  profiles=none mode=log`; no container is
reported with `NO FIREWALL`.

### 7. A second worktree

```bash
railswitch wt add feat-x && railswitch -w feat-x up && curl -s -o /dev/null -w '%{http_code}' http://feat-x.demo.localhost/
podman port demo-feat-x-db 5432/tcp
podman port demo-feat-x-rails
```

Expected: `200`. The DB port is `127.0.0.1:55434` and the Ruby debug port
`127.0.0.1:33002`. These are `55432 + ID` and `33000 + ID` with feat-x's ID 2;
IDs are machine-wide (`railswitch --json wt ls` shows them), so on a machine
with other worktrees the ID, and the ports, are higher.

### 8. Tear the second worktree down

```bash
railswitch -w feat-x down --yes && railswitch wt rm feat-x
podman ps -a --format '{{.Names}}'
podman volume ls --format '{{.Name}}'
```

Expected: neither list has anything named `demo-feat-x-*`; `demo-main-*`
containers are still running.

### 9. The automated run of Steps 3–8

`internal/lifecycle/e2e_integration_test.go` encodes Steps 3–8. It builds
railswitch, creates the workspace in a temp dir, takes the base slug and
feat-x's ID from `wt ls --json` and checks the ports from that ID, and downs
the base worktree at the end. It is skipped when `RAILSWITCH_E2E_REPO` is
unset. `RAILSWITCH_E2E_PREFIX` overrides the prefix (default `demo`). That
prefix must not be registered yet, so use a fresh one if you already ran the
manual steps. railswitch has no command to unregister a project, so the
registry entry remains after the run.

```bash
RAILSWITCH_E2E_REPO=/tmp/rs-demo/demo go test -tags integration ./internal/lifecycle/ -run E2E -timeout 30m -v
```

Expected: `PASS`.

## Known risks to watch

- **`npm ci` EBUSY:** `node_modules` is a volume mounted at
  `/app/node_modules`. `npm ci` tries to remove that directory and gets
  `EBUSY` on the mount point.
- **nvim on the ruby base (glibc):** the nvim tool is built on Debian trixie
  and runs on the ruby base image. A glibc mismatch shows up as a loader
  error when nvim starts.
- **NeedDaemonReload:** after railswitch writes Quadlet units, systemd must
  reload. A unit that still reports `NeedDaemonReload=yes`
  (`systemctl --user show -p NeedDaemonReload <unit>`) runs the old
  definition.
- **`BINDING` honoured by `rails server`:** this is an unverified assumption.
  The rails pack sets `BINDING=0.0.0.0` and expects `rails server` to bind
  every container address. The `curl` 200 in Steps 4 and 7 checks it.
- **Rails < 7.1 has no `/up`:** the app's health check is
  `curl -fsS http://localhost:3000/up`. An app older than 7.1 never becomes
  healthy, so `up` fails. A fresh `rails new` is recent enough.

## Run log

NOT YET RUN — run on a host with podman and record outputs here.
