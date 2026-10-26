# Stage 1 equivalence review: legacy Quadlet units vs railswitch render

## User decisions

**Pending.** Stage 1 is done only when the user accepts this document (plan Task 13, Step 4). Record their decisions on each **gap** and on the [open questions](#open-questions) here.

## How this was produced

- **Legacy:** `testdata/legacy/quadlet/`, copied verbatim from rails-dev-podman `fd7336b`, `.container-config/quadlet/`.
- **Rendered:** `internal/render/testdata/golden/filial/`. This is the `render.Quadlet` output for the Filial fixture (`testdata/fixtures/filial`, worktrees `master` = ID 1 and `feat` = ID 2, subnet index 3, an 8-CPU host), with the temp workspace path written as `/WORKSPACES/filial`. `TestGolden` holds it byte for byte.
- **Cross-check:** a full `railswitch lock && railswitch render` of the same fixture through `workspace.Render` (registry: subnet index 0; this host: 16 CPUs) wrote the same 17 units. After normalizing the workspace path, the only differences were `10.214.0.x` instead of `10.214.3.x` and `--cpus=12/4` instead of `6/2`. The env-file contents quoted below come from that render.
- **Quadlet:** `TestQuadletDryRun` (`go test -tags integration ./internal/render/ -run TestQuadletDryRun`) feeds the rendered units to `quadlet -dryrun -user`. It PASSes with podman 6.1.4's generator (built from source; the sandbox has no podman). All 17 units generate services.

```
diff -ru testdata/legacy/quadlet internal/render/testdata/golden/filial
```

Every file name changed, so `diff -ru` only lists files:

```
Only in testdata/legacy/quadlet: db@.container
Only in testdata/legacy/quadlet: deps@.container
Only in testdata/legacy/quadlet: dozzle.container
Only in internal/render/testdata/golden/filial: filial-feat-browser.container
Only in internal/render/testdata/golden/filial: filial-feat-cache.container
Only in internal/render/testdata/golden/filial: filial-feat-db.container
Only in internal/render/testdata/golden/filial: filial-feat-deps.container
Only in internal/render/testdata/golden/filial: filial-feat-dev.network
Only in internal/render/testdata/golden/filial: filial-feat-nvim.container
Only in internal/render/testdata/golden/filial: filial-feat-rails.container
Only in internal/render/testdata/golden/filial: filial-feat-storage.container
Only in internal/render/testdata/golden/filial: filial-master-browser.container
Only in internal/render/testdata/golden/filial: filial-master-cache.container
Only in internal/render/testdata/golden/filial: filial-master-db.container
Only in internal/render/testdata/golden/filial: filial-master-deps.container
Only in internal/render/testdata/golden/filial: filial-master-dev.network
Only in internal/render/testdata/golden/filial: filial-master-nvim.container
Only in internal/render/testdata/golden/filial: filial-master-rails.container
Only in internal/render/testdata/golden/filial: filial-master-storage.container
Only in internal/render/testdata/golden/filial: filial-proxy.network
Only in testdata/legacy/quadlet: net@.network
Only in testdata/legacy/quadlet: nvim@.container
Only in testdata/legacy/quadlet: playwright@.container
Only in testdata/legacy/quadlet: proxy.network
Only in testdata/legacy/quadlet: rails@.container
Only in testdata/legacy/quadlet: redis@.container
Only in testdata/legacy/quadlet: rustfs-init@.container
Only in testdata/legacy/quadlet: rustfs@.container
Only in testdata/legacy/quadlet: traefik.container
```

So the content diffs below pair each legacy unit with its rendered counterpart for worktree `feat`. The legacy side has its `#` comment lines removed (`grep -v '^#'`). They are rationale, not behaviour, and they would hide the real changes.

```
diff -u <(grep -v '^#' testdata/legacy/quadlet/<legacy>) internal/render/testdata/golden/filial/<rendered>
```

The `master` units differ from `feat` only in per-worktree values: slug, ID-based ports, hosts and env-file path, plus `postgres:16`/`db-data-pg16`, because the fixture pins `feat` to postgres 17.

Each pair's diff is a single hunk (the whole file), so the annotations below go line group by line group. Verdicts:
- **same behaviour**;
- **intended change (D#)**, citing the roadmap decision;
- **gap → Stage N**: legacy behaviour that Stage 1 does not reproduce, and where it lands.

### File mapping

| Legacy | Rendered | Verdict |
|---|---|---|
| `net@.network` | `filial-<slug>-dev.network` | **intended change (D6, D8)** |
| `proxy.network` | `filial-proxy.network` | **intended change (D6, D20)** |
| `rails@.container` | `filial-<slug>-rails.container` | **intended change (D6, D8)** |
| `deps@.container` | `filial-<slug>-deps.container` | **intended change (D6, D8)** |
| `db@.container` | `filial-<slug>-db.container` | **intended change (D3, D6)** |
| `redis@.container` | `filial-<slug>-cache.container` | **intended change (D3: slot id `cache`)** |
| `rustfs@.container` | `filial-<slug>-storage.container` | **intended change (D3: slot id `storage`)** |
| `rustfs-init@.container` | none | **intended change (D18)**: the seed is imported by `podman volume import` in `up` (Stage 2, Task 18) |
| `playwright@.container` | `filial-<slug>-browser.container` | **intended change (D3: slot id `browser`)** |
| `nvim@.container` | `filial-<slug>-nvim.container` | **intended change (D23)** |
| `traefik.container` | `railswitch-traefik.container` (machine-wide) | **intended change (D21)**: one Traefik for every project, rendered from the machine registry (Stage 2, Task 15); not part of this project's goldens |
| `dozzle.container` | none | **gap → Stage 3**: `railswitch-dozzle` at `logs.localhost` (roadmap Stage 3) |

## Applies to every pair

These line groups repeat in every hunk below and are not re-annotated there.

| Lines in the hunk | Verdict | Why |
|---|---|---|
| `+# railswitch: rendered from …` / `+# role: …` header | **intended change (D28)** | Marker line naming prefix and worktree; render uses it to delete units of unregistered worktrees. Comment only, no runtime effect. |
| `-Description=… %i` / `+Description=filial … of worktree feat` | **same behaviour** | Text only. |
| `@@P@@`, `@@ROOT@@`, `%i` replaced by `filial`, `/WORKSPACES/filial`, `feat`; file `x@.container` → `filial-feat-x.container` | **intended change (D6, D8)** | One unit per worktree and service with every value written in, instead of an `@` template instantiated per worktree. Unit = container = `<prefix>-<slug>-<svc>`. |
| `-Requires=/After=@@P@@-net-network@%i.service` | **intended change (D27)** | Dropped because `Network=` now names the `.network` unit, so Quadlet adds `Requires=`/`After=filial-feat-dev-network.service` itself. Shown by the dry-run: `filial-feat-rails.service` has `Requires=filial-feat-dev-network.service` / `After=filial-feat-dev-network.service`. |
| `-[Service] EnvironmentFile=@@ROOT@@/.unit-env/%i.env` (and `%i.share.env`) | **intended change (D8)** | It only existed so systemd could expand `${VAR}` in the unit. Rendered units have no `${VAR}`, so nothing reads it. |
| `-Annotation=@@P@@.firewall.mode=${FIREWALL_MODE}` / `+…=log` | **intended change (D8)** | Same value as the old default (`FIREWALL_MODE:-log` in `units-env.sh`), written in. |
| `-GlobalArgs=--hooks-dir=@@ROOT@@/.firewall/hooks.d` / `+…/.railswitch/firewall/hooks.d` | **intended change (D19)** | Path moves under `.railswitch/`. Render writes the hook descriptor, `hook.sh` and `firewall.sh` there (Stage 2, Task 16). |
| `+NoNewPrivileges=true`, `+Memory=…`, `+PidsLimit=…` | **intended change (D34)** | New hardening and limits on every container. |
| `+Label=filial.unit-hash=<12 hex>` (first line of `[Container]`) | **intended change** (final-review fix I3) | The first 12 hex of the sha256 of the unit without this line. `up` restarts a running container whose label differs from its rendered unit's, so a stale container is caught even after another daemon-reload cleared `NeedDaemonReload`. Traefik carries `railswitch.unit-hash`. Label only, no runtime effect on the app. |
| `+Notify=healthy` on every unit with a `HealthCmd=` | **intended change**: app unit **D34**; data units (db, cache, storage) plan Task 9 ("Data services: health keys from the option, plus `Notify=healthy`"; no D#) | The old rails@ comment says its `Requires=` on db/redis/rustfs is a health gate *because* of `Notify=healthy`, but no old unit set it: the gate was `--sdnotify=conmon` (started, not healthy). Now real; the dry-run shows `--sdnotify=healthy`. |
| `-NetworkAlias=x` + `-Network=@@P@@-%i-dev` / `+Network=filial-feat-dev.network:alias=x` | **intended change (D27)** | Same alias scope for single-network containers; per-network alias for two-network containers, as the old rails@ already did. |
| `-Entrypoint=entrypoint-x.sh` / `+Entrypoint=/usr/local/bin/entrypoint-x.sh` | **same behaviour** | Same file (mounted at `/usr/local/bin/`), absolute instead of found on `PATH`. |
| `-PodmanArgs=--cpus=${CPUS_75}` / `+PodmanArgs=--cpus=6` (25% → `2`) | **intended change (D8)** | Same share of the host CPUs, computed at render. The golden host has 8 CPUs; a `railswitch render` on this 16-CPU sandbox wrote `12`/`4`. |
| `-Volume=@@ROOT@@/.container-config/<asset>` / `+Volume=…/.railswitch/assets/<pkg>/…` | **intended change (D12, D13)** | Package assets are written by render under `.railswitch/assets/` (verified: present in the rendered workspace). |
| `-EnvironmentFile=@@ROOT@@/.unit-env/%i.env` (+ `.container-config/.env`, `%i.share.env`) / `+EnvironmentFile=…/.railswitch/env/feat/<svc>.env` | **intended change (D10)**; share part **intended change (D26)** | One env file per service, mode 0600, holding only that service's env. The old files gave `POSTGRES_PASSWORD` etc. to every container. The share overlay is not rendered until Stage 3. |


## net@.network → filial-feat-dev.network

```diff
--- testdata/legacy/quadlet/net@.network (comments stripped)
+++ internal/render/testdata/golden/filial/filial-feat-dev.network
@@ -1,6 +1,8 @@
+# railswitch: rendered from /WORKSPACES/filial/project.toml (prefix filial, worktree feat). Edit project.toml, not this file.
+
 [Unit]
-Description=Isolated dev network for worktree %i
+Description=Dev network of filial worktree feat
 
 [Network]
-NetworkName=@@P@@-%i-dev
+NetworkName=filial-feat-dev
 Options=mtu=1400
```

| Lines in the hunk | Verdict | Why |
|---|---|---|
| `-NetworkName=@@P@@-%i-dev` / `+NetworkName=filial-feat-dev` | **same behaviour** | Same network name. The unit (and service) name changes: `filial-net-network@feat.service` → `filial-feat-dev-network.service` (D6). |
| `Options=mtu=1400` | **same behaviour** | Unchanged (pinned value). |

## proxy.network → filial-proxy.network

```diff
--- testdata/legacy/quadlet/proxy.network (comments stripped)
+++ internal/render/testdata/golden/filial/filial-proxy.network
@@ -1,9 +1,11 @@
+# railswitch: rendered from /WORKSPACES/filial/project.toml (prefix filial). Edit project.toml, not this file.
+
 [Unit]
-Description=Shared proxy network for @@P@@ worktrees
+Description=Proxy network of project filial
 
 [Network]
-NetworkName=@@P@@_proxy
-Subnet=@@PROXY_SUBNET@@
-Gateway=@@PROXY_GATEWAY@@
-IPRange=@@PROXY_IP_RANGE@@
+NetworkName=filial_proxy
+Subnet=10.214.3.0/24
+Gateway=10.214.3.1
+IPRange=10.214.3.128/25
 Options=mtu=1400
```

| Lines in the hunk | Verdict | Why |
|---|---|---|
| `-NetworkName=@@P@@_proxy` / `+NetworkName=filial_proxy` | **same behaviour** | Same name (D6 `_` rule). |
| `-Subnet/Gateway/IPRange=@@PROXY_…@@` / `+10.214.3.0/24`, `.1`, `.128/25` | **intended change (D20)** | The project's `/24` comes from the machine registry's subnet index (here 3). The gateway is `.1`; the dynamic range `.128/25` keeps Traefik's `.2` free, the same reasoning as the old comment. |

## rails@.container → filial-feat-rails.container

```diff
--- testdata/legacy/quadlet/rails@.container (comments stripped)
+++ internal/render/testdata/golden/filial/filial-feat-rails.container
@@ -1,62 +1,60 @@
+# railswitch: rendered from /WORKSPACES/filial/project.toml (prefix filial, worktree feat). Edit project.toml, not this file.
+# role: app
+
 [Unit]
-Description=Rails dev server for worktree %i
-Requires=@@P@@-net-network@%i.service
-After=@@P@@-net-network@%i.service
-Requires=@@P@@-db@%i.service @@P@@-redis@%i.service @@P@@-rustfs@%i.service
-After=@@P@@-db@%i.service @@P@@-redis@%i.service @@P@@-rustfs@%i.service
-Requires=@@P@@-deps@%i.service
-After=@@P@@-deps@%i.service
-Wants=@@P@@-traefik.service
-After=@@P@@-traefik.service
-Wants=@@P@@-playwright@%i.service
-Wants=@@P@@-nvim@%i.service
-After=@@P@@-playwright@%i.service @@P@@-nvim@%i.service
+Description=filial rails of worktree feat
+Requires=filial-feat-deps.service filial-feat-db.service filial-feat-cache.service filial-feat-storage.service
+Wants=filial-feat-browser.service filial-feat-nvim.service railswitch-traefik.service
+After=filial-feat-deps.service filial-feat-db.service filial-feat-cache.service filial-feat-storage.service filial-feat-browser.service filial-feat-nvim.service railswitch-traefik.service
 
 [Container]
-Image=${RAILS_IMAGE}
-ContainerName=@@P@@-%i-rails
-Network=@@P@@-%i-dev:alias=rails
-Network=@@P@@-proxy.network
-
+Label=filial.unit-hash=0367fe9591f2
+Image=localhost/filial/base:ruby3.3.6-node22.11.0-ba118cc2
+ContainerName=filial-feat-rails
+Network=filial-feat-dev.network:alias=rails
+Network=filial-proxy.network
 UserNS=keep-id:uid=1000,gid=1000
 User=1000:1000
-
-EnvironmentFile=@@ROOT@@/.container-config/.env
-EnvironmentFile=@@ROOT@@/.unit-env/%i.env
-EnvironmentFile=@@ROOT@@/.unit-env/%i.share.env
-
-PodmanArgs=--add-host ${WORKTREE_HOST}:${PODMAN_TRAEFIK_IP} --add-host ${S3_HOST}:${PODMAN_TRAEFIK_IP} --add-host ${RUSTFS_UI_HOST}:${PODMAN_TRAEFIK_IP} --add-host ${TS_HOST_ENTRY}
-PublishPort=127.0.0.1:${RUBY_DEBUG_PORT}:38698
-PublishPort=127.0.0.1:${APP_PORT}:3000
-
-Volume=@@ROOT@@/%i:/app:z
-Volume=${MAIN_WORKTREE_PATH}/.git:${MAIN_WORKTREE_PATH}/.git:ro,z
-Volume=@@ROOT@@/.container-config/entrypoint-app.sh:/usr/local/bin/entrypoint-app.sh:ro,z
-Volume=${GEM_VOLUME}:/usr/local/bundle
-Volume=@@P@@-%i-node-modules:/app/node_modules:U
-Volume=@@P@@_npm_cache:/home/appuser/.npm:U
-Volume=@@P@@_npm_global:/home/appuser/.npm-global:U
-Volume=@@P@@_playwright_browsers:/home/appuser/.cache/ms-playwright:U
-
+EnvironmentFile=/WORKSPACES/filial/.railswitch/env/feat/rails.env
+AddHost=feat.filial.localhost:10.214.3.2
+AddHost=s3.feat.filial.localhost:10.214.3.2
+AddHost=s3-ui.feat.filial.localhost:10.214.3.2
+AddHost=vnc.feat.filial.localhost:10.214.3.2
+PublishPort=127.0.0.1:33002:33002
+PublishPort=127.0.0.1:13002:3000
+Volume=/WORKSPACES/filial/feat:/app:z
+Volume=/WORKSPACES/filial/master/.git:/WORKSPACES/filial/master/.git:ro,z
+Volume=/WORKSPACES/filial/.railswitch/assets/rails/entrypoints/entrypoint-app.sh:/usr/local/bin/entrypoint-app.sh:ro,z
+Volume=filial_gems_ruby3.3.6:/usr/local/bundle
+Volume=filial_npm_cache:/home/appuser/.npm:U
+Volume=filial-feat-node-modules:/app/node_modules:U
+Secret=filial_twilio_auth_token,type=env,target=TWILIO_AUTH_TOKEN
+DropCapability=all
+NoNewPrivileges=true
+Memory=4g
+PidsLimit=4096
 Label=traefik.enable=true
-Label=traefik.http.routers.@@P@@-%i-app.rule=Host(`${WORKTREE_HOST}`)
-Label=traefik.http.routers.@@P@@-%i-app.entrypoints=web
-Label=traefik.http.services.@@P@@-%i-app.loadbalancer.server.port=3000
-
-GlobalArgs=--hooks-dir=@@ROOT@@/.firewall/hooks.d
-Annotation=@@P@@.firewall=none
-Annotation=@@P@@.firewall.networks=@@P@@-%i-dev,@@P@@_proxy
-Annotation=@@P@@.firewall.mode=${FIREWALL_MODE}
-
-PodmanArgs=--cpus=${CPUS_75}
+Label=traefik.docker.network=filial_proxy
+Label=traefik.http.routers.filial-feat-app.rule=Host(`feat.filial.localhost`)
+Label=traefik.http.routers.filial-feat-app.entrypoints=web
+Label=traefik.http.routers.filial-feat-app.service=filial-feat-app
+Label=traefik.http.services.filial-feat-app.loadbalancer.server.port=3000
+HealthCmd=curl -fsS http://localhost:3000/up
+HealthInterval=10s
+HealthRetries=3
+HealthStartPeriod=120s
+Notify=healthy
+GlobalArgs=--hooks-dir=/WORKSPACES/filial/.railswitch/firewall/hooks.d
+Annotation=filial.firewall=none
+Annotation=filial.firewall.networks=filial-feat-dev,filial_proxy
+Annotation=filial.firewall.mode=log
+PodmanArgs=--cpus=6
 LogDriver=k8s-file
 LogOpt=max-size=20m
-Entrypoint=entrypoint-app.sh
+Entrypoint=/usr/local/bin/entrypoint-app.sh
 Exec=bin/dev
 
 [Service]
-EnvironmentFile=@@ROOT@@/.unit-env/%i.env
-EnvironmentFile=@@ROOT@@/.unit-env/%i.share.env
 Restart=on-failure
 RestartSec=5
 TimeoutStartSec=180
```

| Lines in the hunk | Verdict | Why |
|---|---|---|
| `-Requires/After=…db@ …redis@ …rustfs@`, `…deps@` / `+Requires=filial-feat-deps.service filial-feat-db.service filial-feat-cache.service filial-feat-storage.service` (+ `After=`) | **same behaviour** | Same hard dependencies, merged into one line. The dry-run shows them on `filial-feat-rails.service`. With `Notify=healthy` on the data services, this is now the health gate the old comment described. |
| `-Wants=…playwright@ …nvim@ …traefik` / `+Wants=filial-feat-browser.service filial-feat-nvim.service railswitch-traefik.service` (+ `After=`) | **same behaviour**; Traefik name **intended change (D21)** | Same soft dependencies. Traefik becomes the machine-wide `railswitch-traefik.service` (rendered in Stage 2). |
| `-Image=${RAILS_IMAGE}` / `+Image=localhost/filial/base:ruby3.3.6-node22.11.0-ba118cc2` | **intended change (D8, D9, D31)** | Same role (one image per ruby/node pair, shared by worktrees), with a content hash in the tag. |
| `-Network=@@P@@-proxy.network` / `+Network=filial-proxy.network` | **same behaviour** | Same network. |
| `-EnvironmentFile=…/.container-config/.env`, `…/.unit-env/%i.env`, `…/%i.share.env` / `+EnvironmentFile=…/.railswitch/env/feat/rails.env` | **intended change (D10, D26, D42)** | One file. See [Env contents](#env-contents) for the variables that went away. |
| `-PodmanArgs=--add-host ${WORKTREE_HOST}:… ${S3_HOST} ${RUSTFS_UI_HOST} ${TS_HOST_ENTRY}` / `+AddHost=` for `feat`, `s3.feat`, `s3-ui.feat`, `vnc.feat` `.filial.localhost` → `10.214.3.2` | **intended change (D5, D8)**; `TS_HOST_ENTRY` **intended change (D26)** | Same Traefik-routed hosts mapped to Traefik's `.2`, now as native `AddHost=`, plus `vnc.`. The share entry returns with the share redesign (Stage 3). |
| `-PublishPort=127.0.0.1:${RUBY_DEBUG_PORT}:38698`, `${APP_PORT}:3000` / `+127.0.0.1:33002:33002`, `13002:3000` | **intended change (D8, Task 13F)** | Same loopback publishes, with pinned bases 33000+ID and 13000+ID written in. The rails pack's debug port has `same_port = true`: rdbg listens on `RUBY_DEBUG_PORT` (33000+ID) inside the container too, so the port it reports is the one published. |
| `-Volume=@@ROOT@@/%i:/app:z`, `${MAIN_WORKTREE_PATH}/.git…` / `+Volume=/WORKSPACES/filial/feat:/app:z`, `/WORKSPACES/filial/master/.git…:ro,z` | **intended change (D8, D22)** | Same mounts, with the base worktree `master/` written in. |
| `-Volume=${GEM_VOLUME}:/usr/local/bundle` / `+Volume=filial_gems_ruby3.3.6:/usr/local/bundle` | **intended change (D8)** | Same shared gem volume, keyed by ruby version (pack `[[caches]]`). |
| `-Volume=@@P@@-%i-node-modules…:U`, `@@P@@_npm_cache…:U` / `+` same | **same behaviour** | Same volumes and `:U`. |
| `-Volume=@@P@@_npm_global:/home/appuser/.npm-global:U`, `-Volume=@@P@@_playwright_browsers:/home/appuser/.cache/ms-playwright:U` | **gap → Stage 4** | Not in the rails pack's `[[caches]]`, and nothing replaces them. `npm i -g` state and a local Playwright browser cache are not kept across container restarts. The browser runs in its own container, so this probably only matters for Filial-specific workflows. **Open question 1.** |
| `+DropCapability=all` | **intended change (D34)** | App hardening. |
| `+Label=traefik.docker.network=filial_proxy` | **intended change (D21)** | The old project-local Traefik set `--providers.docker.network=@@P@@_proxy` globally. The machine-wide Traefik serves several projects' proxy networks, so each two-network container names its own. |
| `-…routers.@@P@@-%i-app.rule=Host(`${WORKTREE_HOST}`)` / `+…filial-feat-app.rule=Host(`feat.filial.localhost`)`, `+…routers.filial-feat-app.service=filial-feat-app` | **intended change (D5, D8)** | Same router; hostname written in. The explicit `.service=` label names the service Traefik already inferred (single service), so routing is unchanged. |
| `+HealthCmd=curl -fsS http://localhost:3000/up`, `HealthInterval/Retries/StartPeriod`, `Notify=healthy` | **intended change (D34)** | App health. The dry-run shows `--health-cmd` and `--sdnotify=healthy`; `TimeoutStartSec=180` > start period 120s. |
| `+Secret=filial_twilio_auth_token,type=env,target=TWILIO_AUTH_TOKEN` | **intended change (D33)** | The fixture's `[secrets.twilio_auth_token] services = ["rails"]`, delivered by podman as an env var (Task 16A). Legacy had no secret delivery at all (env passthrough). |

## deps@.container → filial-feat-deps.container

```diff
--- testdata/legacy/quadlet/deps@.container (comments stripped)
+++ internal/render/testdata/golden/filial/filial-feat-deps.container
@@ -1,33 +1,34 @@
+# railswitch: rendered from /WORKSPACES/filial/project.toml (prefix filial, worktree feat). Edit project.toml, not this file.
+# role: deps
+
 [Unit]
-Description=Install gems and npm packages for worktree %i
-Requires=@@P@@-net-network@%i.service
-After=@@P@@-net-network@%i.service
+Description=filial deps of worktree feat
 
 [Container]
-Image=${RAILS_IMAGE}
-ContainerName=@@P@@-%i-deps
-Network=@@P@@-%i-dev
+Label=filial.unit-hash=351a507dc52f
+Image=localhost/filial/base:ruby3.3.6-node22.11.0-ba118cc2
+ContainerName=filial-feat-deps
+Network=filial-feat-dev.network:alias=deps
 UserNS=keep-id:uid=1000,gid=1000
 User=1000:1000
-
-Environment=PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1
-
-Volume=@@ROOT@@/%i:/app:z
-Volume=@@ROOT@@/.container-config/entrypoint-deps.sh:/usr/local/bin/entrypoint-deps.sh:ro,z
-Volume=${GEM_VOLUME}:/usr/local/bundle
-Volume=@@P@@-%i-node-modules:/app/node_modules:U
-Volume=@@P@@_npm_cache:/home/appuser/.npm:U
-
-GlobalArgs=--hooks-dir=@@ROOT@@/.firewall/hooks.d
-Annotation=@@P@@.firewall=deps
-Annotation=@@P@@.firewall.networks=@@P@@-%i-dev
-Annotation=@@P@@.firewall.mode=${FIREWALL_MODE}
-
-PodmanArgs=--cpus=${CPUS_75}
+EnvironmentFile=/WORKSPACES/filial/.railswitch/env/feat/deps.env
+Volume=/WORKSPACES/filial/feat:/app:z
+Volume=/WORKSPACES/filial/.railswitch/assets/rails/entrypoints/entrypoint-deps.sh:/usr/local/bin/entrypoint-deps.sh:ro,z
+Volume=filial_gems_ruby3.3.6:/usr/local/bundle
+Volume=filial_npm_cache:/home/appuser/.npm:U
+Volume=filial-feat-node-modules:/app/node_modules:U
+DropCapability=all
+NoNewPrivileges=true
+Memory=4g
+PidsLimit=4096
+GlobalArgs=--hooks-dir=/WORKSPACES/filial/.railswitch/firewall/hooks.d
+Annotation=filial.firewall=deps
+Annotation=filial.firewall.networks=filial-feat-dev
+Annotation=filial.firewall.mode=log
+PodmanArgs=--cpus=6
 LogDriver=passthrough
-Entrypoint=entrypoint-deps.sh
+Entrypoint=/usr/local/bin/entrypoint-deps.sh
 
 [Service]
-EnvironmentFile=@@ROOT@@/.unit-env/%i.env
 Type=oneshot
 TimeoutStartSec=900
```

| Lines in the hunk | Verdict | Why |
|---|---|---|
| `-Image=${RAILS_IMAGE}` / `+Image=localhost/filial/base:…` | **intended change (D9, D31)** | Same image as rails. |
| `-Network=@@P@@-%i-dev` / `+Network=filial-feat-dev.network:alias=deps` | **intended change (D27)** | Same network; `.network` reference gives the dependency. The alias is new, and harmless (nothing resolves `deps`). |
| `-Environment=PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1` / `+EnvironmentFile=…/env/feat/deps.env` | **intended change (D11, D35, D44)** | `deps.env` holds only the pack layer, `BINDING=0.0.0.0`, `RAILS_ENV=development`, `RUBY_DEBUG_HOST=0.0.0.0`, and `BUNDLE_FROZEN=true` (D35, new: frozen installs). With `browser = "playwright@1"`, `PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1` comes from the option's `[deps_env]` (D44), also under `railswitch deps`. **Open question 2, resolved.** |
| Volumes: `/app`, `entrypoint-deps.sh`, gems, `node-modules`, `npm_cache` | **same behaviour** | Same five mounts. `entrypoint-deps.sh` now comes from the rails pack assets. |
| `+DropCapability=all` | **intended change (D34)** |  |
| `Annotation=…firewall=deps`, `networks=filial-feat-dev` | **same behaviour** | Same profile and networks. The `allow.deps = ["github.com"]` list lands in the descriptor (Stage 2, Task 16). |
| `LogDriver=passthrough`, `Type=oneshot`, `TimeoutStartSec=900` | **same behaviour** | Unchanged; still no `RemainAfterExit`, so each rails start reruns deps. |
| No `Exec=` after `Entrypoint=` | **same behaviour** | A pack's `[app] install` becomes the deps unit's `Exec=`, which the deps entrypoint runs after its setup. The rails pack sets none: its `entrypoint-deps.sh` does the lockfile-frozen install itself, as the legacy unit did. |

## db@.container → filial-feat-db.container

```diff
--- testdata/legacy/quadlet/db@.container (comments stripped)
+++ internal/render/testdata/golden/filial/filial-feat-db.container
@@ -1,69 +1,37 @@
+# railswitch: rendered from /WORKSPACES/filial/project.toml (prefix filial, worktree feat). Edit project.toml, not this file.
+# role: data
+
 [Unit]
-Description=PostgreSQL for worktree %i
-Requires=@@P@@-net-network@%i.service
-After=@@P@@-net-network@%i.service
+Description=filial db of worktree feat
 
 [Container]
-Image=docker.io/library/postgres:16
-ContainerName=@@P@@-%i-db
-Network=@@P@@-%i-dev
-NetworkAlias=db
-
-EnvironmentFile=@@ROOT@@/.unit-env/%i.env
-Environment=TZ=Asia/Bangkok
-Environment=PGTZ=Asia/Bangkok
-
+Label=filial.unit-hash=3e72bc09ca96
+Image=docker.io/library/postgres:17
+ContainerName=filial-feat-db
+Network=filial-feat-dev.network:alias=db
+EnvironmentFile=/WORKSPACES/filial/.railswitch/env/feat/db.env
+PublishPort=127.0.0.1:55434:5432
+Volume=/WORKSPACES/filial/.railswitch/assets/postgres/initdb/restore-dump.sh:/docker-entrypoint-initdb.d/restore-dump.sh:ro,z
+Volume=/WORKSPACES/filial/db-dumps:/db-dumps:ro,z
+Volume=filial-feat-db-data-pg17:/var/lib/postgresql/data
 ShmSize=256mb
-
-PublishPort=127.0.0.1:${DB_PORT}:5432
-
-Volume=@@P@@-%i-db-data:/var/lib/postgresql/data
-Volume=@@ROOT@@/.container-config/initdb/restore-dump.sh:/docker-entrypoint-initdb.d/restore-dump.sh:ro,z
-Volume=@@ROOT@@/.container-config/db-dumps:/db-dumps:ro,z
-
-
-Exec=postgres \
-  -c shared_preload_libraries=pg_stat_statements \
-  -c pg_stat_statements.track=all \
-  -c max_connections=200 \
-  -c max_locks_per_transaction=256 \
-  -c shared_buffers=256MB \
-  -c temp_buffers=128MB \
-  -c work_mem=64MB \
-  -c maintenance_work_mem=256MB \
-  -c fsync=off \
-  -c synchronous_commit=off \
-  -c full_page_writes=off \
-  -c max_wal_senders=0 \
-  -c wal_level=minimal \
-  -c wal_compression=off \
-  -c checkpoint_timeout=1h \
-  -c max_wal_size=4GB \
-  -c checkpoint_completion_target=0.9 \
-  -c autovacuum=off \
-  -c random_page_cost=1.0 \
-  -c seq_page_cost=0.1 \
-  -c log_statement=none \
-  -c log_checkpoints=off \
-  -c log_autovacuum_min_duration=-1 \
-  -c log_temp_files=-1 \
-  -c max_parallel_workers_per_gather=4 \
-  -c parallel_setup_cost=0 \
-  -c parallel_tuple_cost=0
-
+NoNewPrivileges=true
+Memory=2g
+PidsLimit=1024
 HealthCmd=pg_isready -U postgres
 HealthInterval=5s
 HealthTimeout=3s
 HealthRetries=5
 HealthStartPeriod=30s
+Notify=healthy
+GlobalArgs=--hooks-dir=/WORKSPACES/filial/.railswitch/firewall/hooks.d
+Annotation=filial.firewall=none
+Annotation=filial.firewall.networks=filial-feat-dev
+Annotation=filial.firewall.mode=log
 LogDriver=k8s-file
 LogOpt=max-size=20m
-GlobalArgs=--hooks-dir=@@ROOT@@/.firewall/hooks.d
-Annotation=@@P@@.firewall=none
-Annotation=@@P@@.firewall.networks=@@P@@-%i-dev
-Annotation=@@P@@.firewall.mode=${FIREWALL_MODE}
+Exec=postgres -c shared_preload_libraries=pg_stat_statements -c pg_stat_statements.track=all -c max_connections=200 -c max_locks_per_transaction=256 -c shared_buffers=256MB -c temp_buffers=128MB -c work_mem=64MB -c maintenance_work_mem=256MB -c fsync=off -c synchronous_commit=off -c full_page_writes=off -c max_wal_senders=0 -c wal_level=minimal -c wal_compression=off -c checkpoint_timeout=1h -c max_wal_size=4GB -c checkpoint_completion_target=0.9 -c autovacuum=off -c random_page_cost=1.0 -c seq_page_cost=0.1 -c log_statement=none -c log_checkpoints=off -c log_autovacuum_min_duration=-1 -c log_temp_files=-1 -c max_parallel_workers_per_gather=4 -c parallel_setup_cost=0 -c parallel_tuple_cost=0
 
 [Service]
-EnvironmentFile=@@ROOT@@/.unit-env/%i.env
 Restart=on-failure
 RestartSec=5
```

| Lines in the hunk | Verdict | Why |
|---|---|---|
| `-Image=docker.io/library/postgres:16` / `+…:17` | **intended change**: per-worktree version override (`[worktrees.<slug>.versions]`, HANDOFF §3.2; plan Task 9 `TestWorktreeVersionOverride`) | `feat` is pinned to 17 by the fixture's `[worktrees.feat.versions]`. `filial-master-db.container` has `postgres:16`. |
| `-NetworkAlias=db` / `+Network=filial-feat-dev.network:alias=db` | **same behaviour** | Same alias, `db` (D3 slot id equals the old name). |
| `-EnvironmentFile=…/.unit-env/%i.env`, `-Environment=TZ=…`, `-Environment=PGTZ=…` / `+EnvironmentFile=…/env/feat/db.env` | **intended change (D10, D29)** | `db.env` = `DB_PORT`, `PGTZ`, `POSTGRES_PASSWORD` (generated, D17), `TZ`. It has no `PGHOST`, which the old comment says breaks initdb. The old file passed every worktree var to db. |
| `-PublishPort=127.0.0.1:${DB_PORT}:5432` / `+127.0.0.1:55434:5432` | **intended change (D8)** | 55432+ID. |
| `-Volume=@@P@@-%i-db-data:…` / `+Volume=filial-feat-db-data-pg17:…` | **intended change (postgres option `db-data-pg{postgres.major}`)** | The volume name carries the major version, so switching a worktree's postgres version gets a fresh data dir instead of a data dir the new server refuses. `master`: `filial-master-db-data-pg16`. Existing `filial-<slug>-db-data` volumes are not reused: data is restored from `db-dumps/` by initdb. |
| `restore-dump.sh` and `db-dumps` mounts | **intended change (D12, D18)** | Same mounts. The script now comes from the postgres option assets; `db-dumps/` moves from `.container-config/db-dumps` to `<workspace>/db-dumps`. |
| `ShmSize=256mb`, `HealthCmd=pg_isready…` and timings | **same behaviour** | Unchanged. |
| `Exec=postgres \ -c …` (continued lines) / one-line `Exec=postgres -c …` | **same behaviour** | Same 27 `-c` flags in the same order (D42: postgres keeps its dev tuning). |
| No `DropCapability`, no `UserNS` | **same behaviour** | Data containers keep podman defaults (D34), as before. |

## redis@.container → filial-feat-cache.container

```diff
--- testdata/legacy/quadlet/redis@.container (comments stripped)
+++ internal/render/testdata/golden/filial/filial-feat-cache.container
@@ -1,27 +1,31 @@
+# railswitch: rendered from /WORKSPACES/filial/project.toml (prefix filial, worktree feat). Edit project.toml, not this file.
+# role: data
+
 [Unit]
-Description=Redis for worktree %i
-Requires=@@P@@-net-network@%i.service
-After=@@P@@-net-network@%i.service
+Description=filial cache of worktree feat
 
 [Container]
+Label=filial.unit-hash=c0c3e332d3b7
 Image=docker.io/library/redis:8-alpine
-ContainerName=@@P@@-%i-redis
-Network=@@P@@-%i-dev
-NetworkAlias=redis
-Exec=redis-server --loglevel warning --save '' --appendonly no
+ContainerName=filial-feat-cache
+Network=filial-feat-dev.network:alias=cache
+EnvironmentFile=/WORKSPACES/filial/.railswitch/env/feat/cache.env
+NoNewPrivileges=true
+Memory=2g
+PidsLimit=1024
 HealthCmd=redis-cli ping
 HealthInterval=5s
 HealthTimeout=3s
 HealthRetries=5
+Notify=healthy
+GlobalArgs=--hooks-dir=/WORKSPACES/filial/.railswitch/firewall/hooks.d
+Annotation=filial.firewall=none
+Annotation=filial.firewall.networks=filial-feat-dev
+Annotation=filial.firewall.mode=log
 LogDriver=k8s-file
 LogOpt=max-size=20m
-GlobalArgs=--hooks-dir=@@ROOT@@/.firewall/hooks.d
-Annotation=@@P@@.firewall=none
-Annotation=@@P@@.firewall.networks=@@P@@-%i-dev
-Annotation=@@P@@.firewall.mode=${FIREWALL_MODE}
+Exec=redis-server --loglevel warning --save "" --appendonly no
 
 [Service]
-EnvironmentFile=@@ROOT@@/.unit-env/%i.env
 Restart=on-failure
 RestartSec=5
-
```

| Lines in the hunk | Verdict | Why |
|---|---|---|
| `-ContainerName=@@P@@-%i-redis`, `-NetworkAlias=redis` / `+filial-feat-cache`, `alias=cache` | **intended change (D3)** | Slot id `cache`. Clients follow: `REDIS_URL=redis://cache:6379/1` in rails.env (was `redis://redis:6379/1`). |
| `-Exec=redis-server … --save ''` / `+Exec=redis-server … --save ""` | **same behaviour** | Both quote an empty argument to systemd's parser. |
| `+EnvironmentFile=…/env/feat/cache.env` | **intended change (D10)** | Holds `TZ` only (project `[env]`). Old redis got no in-container env. |
| Health check lines | **same behaviour** | Unchanged. |

## rustfs@.container → filial-feat-storage.container

```diff
--- testdata/legacy/quadlet/rustfs@.container (comments stripped)
+++ internal/render/testdata/golden/filial/filial-feat-storage.container
@@ -1,53 +1,53 @@
+# railswitch: rendered from /WORKSPACES/filial/project.toml (prefix filial, worktree feat). Edit project.toml, not this file.
+# role: data
+
 [Unit]
-Description=RustFS S3 storage for worktree %i
-Requires=@@P@@-net-network@%i.service @@P@@-rustfs-init@%i.service
-After=@@P@@-net-network@%i.service @@P@@-rustfs-init@%i.service
-Wants=@@P@@-traefik.service
-After=@@P@@-traefik.service
+Description=filial storage of worktree feat
+Wants=railswitch-traefik.service
+After=railswitch-traefik.service
 
 [Container]
+Label=filial.unit-hash=8a2d620cb4ef
 Image=docker.io/rustfs/rustfs:1.0.0-alpha.83
-ContainerName=@@P@@-%i-rustfs
-Network=@@P@@-%i-dev:alias=rustfs
-Network=@@P@@-proxy.network
-PublishPort=127.0.0.1:${S3_PORT}:9000
-EnvironmentFile=@@ROOT@@/.unit-env/%i.env
-Environment=RUSTFS_VOLUMES=/data/rustfs{0..3}
-Environment=RUSTFS_ACCESS_KEY=rustfsadmin
-Environment=RUSTFS_SECRET_KEY=rustfsadmin
-Environment=RUSTFS_CONSOLE_ENABLE=true
-Environment=RUSTFS_ADDRESS=0.0.0.0:9000
-Environment=RUSTFS_CONSOLE_ADDRESS=0.0.0.0:9001
-Environment=RUSTFS_CHECK_UPDATE=false
-Environment=RUSTFS_TELEMETRY=off
-Volume=@@P@@-%i-rustfs-data:/data
-
+ContainerName=filial-feat-storage
+Network=filial-feat-dev.network:alias=storage
+Network=filial-proxy.network
+EnvironmentFile=/WORKSPACES/filial/.railswitch/env/feat/storage.env
+AddHost=feat.filial.localhost:10.214.3.2
+AddHost=s3.feat.filial.localhost:10.214.3.2
+AddHost=s3-ui.feat.filial.localhost:10.214.3.2
+AddHost=vnc.feat.filial.localhost:10.214.3.2
+PublishPort=127.0.0.1:19002:9000
+Volume=filial-feat-rustfs-data:/data
+NoNewPrivileges=true
+Memory=2g
+PidsLimit=1024
 Label=traefik.enable=true
-Label=traefik.http.routers.@@P@@-%i-s3.rule=Host(`${S3_HOST}`)
-Label=traefik.http.routers.@@P@@-%i-s3.entrypoints=web
-Label=traefik.http.routers.@@P@@-%i-s3.service=@@P@@-%i-s3
-Label=traefik.http.services.@@P@@-%i-s3.loadbalancer.server.port=9000
-Label=traefik.http.routers.@@P@@-%i-s3-ui.rule=Host(`${RUSTFS_UI_HOST}`)
-Label=traefik.http.routers.@@P@@-%i-s3-ui.entrypoints=web
-Label=traefik.http.routers.@@P@@-%i-s3-ui.service=@@P@@-%i-s3-ui
-Label=traefik.http.services.@@P@@-%i-s3-ui.loadbalancer.server.port=9001
-Label=traefik.http.routers.@@P@@-%i-s3-ui.middlewares=@@P@@-%i-s3-ui-root
-Label=traefik.http.middlewares.@@P@@-%i-s3-ui-root.redirectregex.regex=^http://([^/]+)/?$$
-Label=traefik.http.middlewares.@@P@@-%i-s3-ui-root.redirectregex.replacement=http://$${1}/rustfs/console/index.html
-
+Label=traefik.docker.network=filial_proxy
+Label=traefik.http.routers.filial-feat-s3.rule=Host(`s3.feat.filial.localhost`)
+Label=traefik.http.routers.filial-feat-s3.entrypoints=web
+Label=traefik.http.routers.filial-feat-s3.service=filial-feat-s3
+Label=traefik.http.services.filial-feat-s3.loadbalancer.server.port=9000
+Label=traefik.http.routers.filial-feat-s3-ui.rule=Host(`s3-ui.feat.filial.localhost`)
+Label=traefik.http.routers.filial-feat-s3-ui.entrypoints=web
+Label=traefik.http.routers.filial-feat-s3-ui.service=filial-feat-s3-ui
+Label=traefik.http.services.filial-feat-s3-ui.loadbalancer.server.port=9001
+Label=traefik.http.routers.filial-feat-s3-ui.middlewares=filial-feat-s3-ui-root
+Label=traefik.http.middlewares.filial-feat-s3-ui-root.redirectregex.regex=^http://([^/]+)/?$$
+Label=traefik.http.middlewares.filial-feat-s3-ui-root.redirectregex.replacement=http://$${1}/rustfs/console/index.html
 HealthCmd=curl -f http://localhost:9000/health
 HealthInterval=5s
 HealthTimeout=3s
 HealthRetries=5
 HealthStartPeriod=15s
+Notify=healthy
+GlobalArgs=--hooks-dir=/WORKSPACES/filial/.railswitch/firewall/hooks.d
+Annotation=filial.firewall=none
+Annotation=filial.firewall.networks=filial-feat-dev,filial_proxy
+Annotation=filial.firewall.mode=log
 LogDriver=k8s-file
 LogOpt=max-size=20m
-GlobalArgs=--hooks-dir=@@ROOT@@/.firewall/hooks.d
-Annotation=@@P@@.firewall=none
-Annotation=@@P@@.firewall.networks=@@P@@-%i-dev,@@P@@_proxy
-Annotation=@@P@@.firewall.mode=${FIREWALL_MODE}
 
 [Service]
-EnvironmentFile=@@ROOT@@/.unit-env/%i.env
 Restart=on-failure
 RestartSec=5
```

| Lines in the hunk | Verdict | Why |
|---|---|---|
| `-Requires/After=…rustfs-init@%i.service` | **intended change (D18)** | No init container. `up` imports `db-dumps/rustfs_data.tar.gz` (rustfs option `seed`) into `filial-feat-rustfs-data` when it creates the volume (Task 18). Old: the tarball was extracted when `/data/rustfs0` was missing. New: imported once at volume creation. Equivalent for a fresh volume. |
| `-ContainerName=…-rustfs`, `alias=rustfs` / `+…-storage`, `alias=storage` | **intended change (D3)** | Clients follow: `S3_ENDPOINT=http://storage:9000` in rails.env. Legacy rails got `RUSTFS_ENDPOINT=http://${S3_HOST}` (via Traefik); see [Env contents](#env-contents). |
| `-Environment=RUSTFS_*` (8 lines) / `+EnvironmentFile=…/env/feat/storage.env` | **intended change (D10, D17)** | Same 8 `RUSTFS_*` values in `storage.env`, except `RUSTFS_SECRET_KEY`: it is the generated `S3_SECRET_KEY` (D17) instead of the hard-coded `rustfsadmin`. |
| `+AddHost=…` (4 lines) | **intended change (D5)** | New: every proxy-network container gets the Traefik host entries. Harmless for storage. |
| `-PublishPort=127.0.0.1:${S3_PORT}:9000` / `+127.0.0.1:19002:9000` | **intended change (D8)** | 19000+ID (pinned "Storage (share)"). |
| `Volume=…-rustfs-data:/data` | **same behaviour** | Same name. |
| Labels: `s3`, `s3-ui` routers and the `redirectregex` middleware with `$$` | **same behaviour** + `traefik.docker.network` **intended change (D21)** | Same routers, hosts written in (`s3.feat.filial.localhost`, `s3-ui.feat.filial.localhost`). `$$` stays escaped (D8 lint). |
| Health check lines | **same behaviour** | Unchanged. |

## playwright@.container → filial-feat-browser.container

```diff
--- testdata/legacy/quadlet/playwright@.container (comments stripped)
+++ internal/render/testdata/golden/filial/filial-feat-browser.container
@@ -1,42 +1,43 @@
+# railswitch: rendered from /WORKSPACES/filial/project.toml (prefix filial, worktree feat). Edit project.toml, not this file.
+# role: tool
+
 [Unit]
-Description=Playwright browser server for worktree %i
-Requires=@@P@@-net-network@%i.service
-After=@@P@@-net-network@%i.service
-Wants=@@P@@-traefik.service
-After=@@P@@-traefik.service
+Description=filial browser of worktree feat
+Wants=railswitch-traefik.service
+After=railswitch-traefik.service
 
 [Container]
-Image=${PLAYWRIGHT_IMAGE}
-ContainerName=@@P@@-%i-playwright
-Network=@@P@@-%i-dev:alias=playwright
-Network=@@P@@-proxy.network
-
-EnvironmentFile=@@ROOT@@/.unit-env/%i.env
-
-PublishPort=127.0.0.1:${PLAYWRIGHT_HOST_PORT}:8888
-PodmanArgs=--add-host ${WORKTREE_HOST}:${PODMAN_TRAEFIK_IP} --add-host ${S3_HOST}:${PODMAN_TRAEFIK_IP} --add-host ${RUSTFS_UI_HOST}:${PODMAN_TRAEFIK_IP}
-
+Label=filial.unit-hash=54588bf5adb0
+Image=localhost/filial/playwright:1.54.0-8ed7a43c
+ContainerName=filial-feat-browser
+Network=filial-feat-dev.network:alias=browser
+Network=filial-proxy.network
+EnvironmentFile=/WORKSPACES/filial/.railswitch/env/feat/browser.env
+AddHost=feat.filial.localhost:10.214.3.2
+AddHost=s3.feat.filial.localhost:10.214.3.2
+AddHost=s3-ui.feat.filial.localhost:10.214.3.2
+AddHost=vnc.feat.filial.localhost:10.214.3.2
+PublishPort=127.0.0.1:18890:8888
+Volume=/WORKSPACES/filial/.railswitch/assets/playwright/init-firewall-playwright.sh:/usr/local/bin/init-firewall-playwright.sh:ro,z
+Volume=/WORKSPACES/filial/.railswitch/assets/playwright/entrypoints/entrypoint-playwright.sh:/usr/local/bin/entrypoint-playwright.sh:ro,z
 ShmSize=1g
-RunInit=yes
+RunInit=true
 AddCapability=NET_ADMIN
 AddCapability=NET_RAW
-
-Volume=@@ROOT@@/.container-config/entrypoint-playwright.sh:/usr/local/bin/entrypoint-playwright.sh:ro,z
-Volume=@@ROOT@@/.container-config/init-firewall-playwright.sh:/usr/local/bin/init-firewall-playwright.sh:ro,z
-
+NoNewPrivileges=true
+Memory=2g
+PidsLimit=4096
 Label=traefik.enable=true
-Label=traefik.http.routers.@@P@@-%i-vnc.rule=Host(`vnc.${WORKTREE_HOST}`)
-Label=traefik.http.routers.@@P@@-%i-vnc.entrypoints=web
-Label=traefik.http.routers.@@P@@-%i-vnc.service=@@P@@-%i-vnc
-Label=traefik.http.services.@@P@@-%i-vnc.loadbalancer.server.port=8080
-
-PodmanArgs=--cpus=${CPUS_25}
+Label=traefik.docker.network=filial_proxy
+Label=traefik.http.routers.filial-feat-vnc.rule=Host(`vnc.feat.filial.localhost`)
+Label=traefik.http.routers.filial-feat-vnc.entrypoints=web
+Label=traefik.http.routers.filial-feat-vnc.service=filial-feat-vnc
+Label=traefik.http.services.filial-feat-vnc.loadbalancer.server.port=8080
+PodmanArgs=--cpus=2
 LogDriver=k8s-file
 LogOpt=max-size=20m
-Entrypoint=entrypoint-playwright.sh
+Entrypoint=/usr/local/bin/entrypoint-playwright.sh
 
 [Service]
-EnvironmentFile=@@ROOT@@/.unit-env/%i.env
 Restart=on-failure
 RestartSec=5
-
```

| Lines in the hunk | Verdict | Why |
|---|---|---|
| `-Image=${PLAYWRIGHT_IMAGE}` / `+Image=localhost/filial/playwright:1.54.0-8ed7a43c` | **intended change (D9, D31)** | Standalone build (not on the base chain), tag keyed by Playwright version + hash. |
| `-…-playwright`, `alias=playwright` / `+…-browser`, `alias=browser` | **intended change (D3)** | Clients follow: `PLAYWRIGHT_HOST=ws://browser:8888/connect` (was `ws://playwright:8888/connect`). |
| `-EnvironmentFile=…/.unit-env/%i.env` / `+EnvironmentFile=…/env/feat/browser.env` | **intended change (D10)** | `browser.env` = `CHROME_START_URL`, `PLAYWRIGHT_HOST_PORT`, `TZ`, `WORKTREE_HOST`, `WORKTREE_ID`, `WORKTREE_SLUG`. It no longer receives `POSTGRES_PASSWORD` etc. The old entrypoint read `TRAEFIK_IP` for its socat `:80` forward. The new one resolves Traefik's address from the `AddHost=` entry for `$WORKTREE_HOST` (`getent hosts`), so it needs no `TRAEFIK_IP`. |
| `-PodmanArgs=--add-host …` / `+AddHost=…` (4 lines) | **same behaviour** | Same hosts plus `vnc.`. |
| `-PublishPort=127.0.0.1:${PLAYWRIGHT_HOST_PORT}:8888` / `+127.0.0.1:18890:8888` | **intended change (D8)** | 18888+ID. |
| `-RunInit=yes` / `+RunInit=true` | **same behaviour** | Same boolean. |
| `ShmSize=1g`, `AddCapability=NET_ADMIN`/`NET_RAW`, entrypoint and init-firewall mounts | **same behaviour** | Unchanged (assets from the playwright option). No `DropCapability`, no hooks dir: it runs its own firewall (D34, D19 `firewall = "self"`). |
| `vnc` router labels | **same behaviour** + `traefik.docker.network` **intended change (D21)** |  |
| `-PodmanArgs=--cpus=${CPUS_25}` / `+--cpus=2` | **intended change (D8)** | 25% of the host CPUs. |

## nvim@.container → filial-feat-nvim.container

```diff
--- testdata/legacy/quadlet/nvim@.container (comments stripped)
+++ internal/render/testdata/golden/filial/filial-feat-nvim.container
@@ -1,53 +1,43 @@
+# railswitch: rendered from /WORKSPACES/filial/project.toml (prefix filial, worktree feat). Edit project.toml, not this file.
+# role: tool
+
 [Unit]
-Description=Neovim server for worktree %i
-Requires=@@P@@-net-network@%i.service
-After=@@P@@-net-network@%i.service
+Description=filial nvim of worktree feat
 
 [Container]
-Image=${NVIM_IMAGE}
-ContainerName=@@P@@-%i-nvim
-Network=@@P@@-%i-dev
-NetworkAlias=nvim
+Label=filial.unit-hash=6be063e54a09
+Image=localhost/filial/nvim:ruby3.3.6-node22.11.0-d2ae020d
+ContainerName=filial-feat-nvim
+Network=filial-feat-dev.network:alias=nvim
 UserNS=keep-id:uid=1000,gid=1000
 User=1000:1000
-
-EnvironmentFile=@@ROOT@@/.container-config/.env
-EnvironmentFile=@@ROOT@@/.unit-env/%i.env
-Environment=SSH_AUTH_SOCK=/tmp/ssh-agent.sock
-Environment=RUSTFS_ENDPOINT=http://rustfs:9000
-
-PublishPort=127.0.0.1:${NVIM_PORT}:7777
-
-Volume=@@ROOT@@/.home/%i:/home/appuser:z
-Volume=@@ROOT@@/%i:/app:z
-Volume=${MAIN_WORKTREE_PATH}/.git:${MAIN_WORKTREE_PATH}/.git:z
-Volume=${NVIM_CONFIG_MOUNT}
-Volume=${SSH_PATH}:/home/appuser/.ssh:ro,z
-Volume=${SSH_AGENT_SOCK}:/tmp/ssh-agent.sock:ro,z
-Volume=@@ROOT@@/.container-config/entrypoint-nvim.sh:/usr/local/bin/entrypoint-nvim.sh:ro,z
-Volume=${GITIGNORE_PATH}:/home/appuser/.config/git/ignore:ro,z
-Volume=${GITCONFIG_PATH}:/home/appuser/.gitconfig:ro,z
-Volume=/usr/bin/kitten:/usr/bin/kitten:ro
-Volume=%h/.local/state/kitty:/kitty-sock:z
-Volume=${GEM_VOLUME}:/usr/local/bundle
-Volume=@@P@@-%i-node-modules:/app/node_modules:U
-Volume=@@P@@_npm_cache:/home/appuser/.npm:U
-Volume=@@P@@_npm_global:/home/appuser/.npm-global:U
-Volume=@@P@@_nvim_share:/home/appuser/.local/share/nvim:U
-
-GlobalArgs=--hooks-dir=@@ROOT@@/.firewall/hooks.d
-Annotation=@@P@@.firewall=agent,deps
-Annotation=@@P@@.firewall.networks=@@P@@-%i-dev
-Annotation=@@P@@.firewall.mode=${FIREWALL_MODE}
-
-PodmanArgs=--cpus=${CPUS_25} --tty --interactive
+EnvironmentFile=/WORKSPACES/filial/.railswitch/env/feat/nvim.env
+PublishPort=127.0.0.1:17002:7777
+Volume=/WORKSPACES/filial/feat:/app:z
+Volume=/WORKSPACES/filial/master/.git:/WORKSPACES/filial/master/.git:z
+Volume=/WORKSPACES/filial/.railswitch/home/feat:/home/appuser:z
+Volume=/WORKSPACES/filial/.railswitch/assets/nvim/entrypoints/entrypoint-nvim.sh:/usr/local/bin/entrypoint-nvim.sh:ro,z
+Volume=filial-feat-node-modules:/app/node_modules:U
+Volume=filial_gems_ruby3.3.6:/usr/local/bundle
+Volume=filial_npm_cache:/home/appuser/.npm:U
+Volume=filial_nvim_config:/home/appuser/.config/nvim:U
+Volume=filial_nvim_share:/home/appuser/.local/share/nvim:U
+Secret=filial_github_token,type=mount,target=/run/secrets/github_token,uid=1000,gid=1000,mode=0400
+DropCapability=all
+NoNewPrivileges=true
+Memory=2g
+PidsLimit=4096
+GlobalArgs=--hooks-dir=/WORKSPACES/filial/.railswitch/firewall/hooks.d
+Annotation=filial.firewall=agent,deps
+Annotation=filial.firewall.networks=filial-feat-dev
+Annotation=filial.firewall.mode=log
+PodmanArgs=--cpus=2 --tty --interactive
 LogDriver=k8s-file
 LogOpt=max-size=20m
-Entrypoint=entrypoint-nvim.sh
+Entrypoint=/usr/local/bin/entrypoint-nvim.sh
 Exec=/usr/local/bin/nvim --headless --listen 0.0.0.0:7777
 
 [Service]
-EnvironmentFile=@@ROOT@@/.unit-env/%i.env
 Restart=on-failure
 RestartSec=5
 SuccessExitStatus=1
```

| Lines in the hunk | Verdict | Why |
|---|---|---|
| `-Image=${NVIM_IMAGE}` / `+Image=localhost/filial/nvim:ruby3.3.6-node22.11.0-d2ae020d` | **intended change (D9, D23, D31)** | Built on the base image. |
| `-EnvironmentFile=…/.container-config/.env`, `…/.unit-env/%i.env`, `-Environment=SSH_AUTH_SOCK=…`, `-Environment=RUSTFS_ENDPOINT=http://rustfs:9000` / `+EnvironmentFile=…/env/feat/nvim.env` | **intended change (D10, D33, D42)** | `nvim.env` has the app env (DB, `REDIS_URL`, `S3_ENDPOINT=http://storage:9000`, `PLAYWRIGHT_HOST`, worktree vars). `SSH_AUTH_SOCK` is gone (D33); `RUSTFS_ENDPOINT` is now `S3_ENDPOINT` (D3/option `gives`). |
| `-PublishPort=127.0.0.1:${NVIM_PORT}:7777` / `+127.0.0.1:17002:7777` | **intended change (D8)** | 17000+ID. |
| `-Volume=@@ROOT@@/.home/%i:/home/appuser:z` / `+Volume=…/.railswitch/home/feat:/home/appuser:z` | **intended change (D30)** | Per-worktree home moves under `.railswitch/`. Render creates `.railswitch/home/<slug>/` (0700) for every worktree when a service mounts it (Task 13F); orphan removal never deletes one. |
| `-Volume=${MAIN_WORKTREE_PATH}/.git…:z` / `+Volume=/WORKSPACES/filial/master/.git…:z` | **same behaviour** | Same mount, read-write as before. |
| `-Volume=${NVIM_CONFIG_MOUNT}` / `+Volume=filial_nvim_config:/home/appuser/.config/nvim:U` | **intended change (D33)** | No host nvim config; a named volume instead. |
| `-Volume=${SSH_PATH}…`, `${SSH_AGENT_SOCK}…`, `${GITIGNORE_PATH}…`, `${GITCONFIG_PATH}…` / `+Secret=filial_github_token,type=mount,target=/run/secrets/github_token,uid=1000,gid=1000,mode=0400` | **intended change (D33)** | Nothing of the host identity reaches a container. A GitHub token is the fixture's declared `[secrets.github_token]`, mounted read-only for the tool user (Task 16A). |
| `-Volume=/usr/bin/kitten…`, `-Volume=%h/.local/state/kitty:/kitty-sock:z` | **gap → Stage 4** | Machine-local terminal integration. The plan (Task 4) defers it to Stage 4. |
| `-Volume=@@P@@_npm_global…:U` | **gap → Stage 4** | As for rails: **open question 1**. |
| gems, `node-modules`, `npm_cache`, `nvim_share`, entrypoint | **same behaviour** | Same volumes (gems/npm from the pack caches). |
| `+DropCapability=all` | **intended change (D34)** | Tool hardening. |
| `Annotation=…firewall=agent,deps` | **same behaviour** | Same profile. |
| `PodmanArgs=… --tty --interactive`, `Exec=… --listen 0.0.0.0:7777`, `SuccessExitStatus=1` | **same behaviour** | Unchanged. |

## rustfs-init@.container → (none)

Removed: **intended change (D18)**. See the storage `Requires=` row. The seed import runs in `up` (Stage 2, Task 18).

## traefik.container, dozzle.container → (none)

- `traefik.container` is an **intended change (D21)**. Render writes one machine-wide `railswitch-traefik.container` with `Network=filial-proxy.network:ip=10.214.<n>.2` per project (Stage 2, Task 15), instead of one Traefik per project. App, storage and browser units `Wants=railswitch-traefik.service`, and carry `traefik.docker.network`.
- `dozzle.container` is a **gap → Stage 3** (`railswitch-dozzle` at `logs.localhost`).

## Env contents

The units no longer hold env; it moved to the per-service env files (D10). Compared with the old `.container-config/.env` + `.unit-env/<slug>.env`, rails.env has `APP_PORT`, `BINDING`, `DATABASE_URL`, `HEADLESS_SYSTEM_TESTS` (from the fixture's `[services.rails.env]`), `PG*`, `PLAYWRIGHT_HOST`, `RAILS_ENV`, `REDIS_URL`, `RUBY_DEBUG_HOST`, `RUBY_DEBUG_PORT`, `S3_*`, `TZ`, `WORKTREE_*`.

These variables are gone. Each is **intended (D42)**: Filial preferences, not generic defaults. They come back via Filial's own `[env]`/`[services.<id>.env]`, a **gap → Stage 4**:
- `DEV_HOSTS`, `DOMAIN`, `WORKTREE_NAME`;
- `GIT_EDITOR`, `EDITOR`, `TERM`, `SHELL`, `COLORTERM`, `KITTY_WINDOW_ID`;
- `TAILWINDCSS_DEBUG`, `LAUNCHY_DRY_RUN`, `BROWSER`;
- `RUBY_DEBUG_IRB_CONSOLE`;
- `SKIP_TEST_DATABASE_TRUNCATE`, `DOCKER`;
- `RUSTFS_ENDPOINT`, `S3_HOST`, `RUSTFS_UI_HOST` (`TRAEFIK_IP`/`PODMAN_TRAEFIK_IP` are obsolete: the playwright entrypoint reads the `AddHost=` entry).

Others are intended for different reasons:
- **D33:** the `GH_TOKEN`/`GITHUB_PERSONAL_ACCESS_TOKEN` passthrough;
- **D8:** the image/version/path vars that only existed for `${VAR}` expansion;
- **D17:** `PGPASSWORD=password` (now generated).

`PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1` is not gone: the playwright option gives it to rails, worker, nvim and deps (D44).

`RUBY_DEBUG_HOST=0.0.0.0` is not gone: the rails pack's `[env]` gives it (Task 13F) to the rails, worker, nvim and deps env files, so rdbg is reachable through the published port.

## Open questions

1. **`npm_global` and `playwright_browsers` volumes** (rails, nvim): add them as rails pack `[[caches]]`, or drop them for good (D42)?
2. **Resolved (D44, commit `feat(pack): options can give env to deps; playwright skips its browser download`):** the playwright option gives it to deps via the new `[deps_env]`, and to the app via `[gives]`. Was: **`PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1` for deps:** should the playwright option give it to deps when the browser slot is on (as the design mock-up does), or should Filial set `[services.deps.env]` (Stage 4)?
