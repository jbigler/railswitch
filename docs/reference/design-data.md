# design-data.md: data model extracted from the `*.dc.html` mock-ups

## 1. Pack definitions

Six packs appear on every board: `rails`, `hanami`, `phoenix`, `django`, `flask`, `node`, always referenced as `<id>@1`. No single board holds a complete pack, so the tables below merge them, and each row names the board it comes from.

### 1a. App service, command, port and install

| pack | app svc | server (Main `Main.dc.html:203-208`) | server (Review `Review.dc.html:132-137`) | port | install (Main) | install (Review) |
|---|---|---|---|---|---|---|
| rails | `rails` | `bin/dev` | `bin/dev` | 3000 | `bundle install · npm install` | `bundle install && npm install` |
| hanami | `web` | `bundle exec hanami dev` | same | 2300 | `bundle install · npm install` | `bundle install && npm install` |
| phoenix | `web` | `mix phx.server` | same | 4000 | `mix deps.get · npm install --prefix assets` | `mix deps.get` |
| django | `web` | `manage.py runserver 0.0.0.0:8000` | `uv run manage.py runserver 0.0.0.0:8000` | 8000 | `uv sync` | `uv sync` |
| flask | `web` | `flask run --host 0.0.0.0` | `uv run flask run --host 0.0.0.0` | 5000 | `uv sync` | `uv sync` |
| node | `web` | `npm run dev` | same | 3000 | `npm ci` | `npm ci` |

### 1b. Versions, caches, hosts, console/tests and registries (Main `:203-208`)

| pack | versions | caches | hosts | hostEnv | console | tests | registries |
|---|---|---|---|---|---|---|---|
| rails | `.ruby-version, .nvmrc` | `gems (shared per Ruby version), node_modules per worktree, npm cache` | `config.hosts ← DEV_HOSTS; links from DOMAIN` | DEV_HOSTS, DOMAIN | `bin/rails console` | `bin/rails test` | `rubygems.org, registry.npmjs.org` |
| hanami | `.ruby-version, .nvmrc` | `gems (shared per Ruby version), node_modules per worktree` | `no host check; links from the request` | — | `bundle exec hanami console` | `bundle exec rspec` | same as rails |
| phoenix | `.tool-versions (elixir, erlang)` | `hex + mix (shared per Elixir version), deps/ and _build/ per worktree` | `check_origin and url host ← PHX_HOST` | PHX_HOST | `iex -S mix` | `mix test` | `hex.pm, repo.hex.pm, builds.hex.pm` |
| django | `.python-version` | `uv cache (shared), .venv per worktree` | `ALLOWED_HOSTS and CSRF_TRUSTED_ORIGINS` | ALLOWED_HOSTS, CSRF_TRUSTED_ORIGINS | `manage.py shell` | `pytest` | `pypi.org, files.pythonhosted.org` |
| flask | `.python-version` | same as django | `no host check; SERVER_NAME for links` | SERVER_NAME | `flask shell` | `pytest` | same as django |
| node | `.nvmrc, .node-version` | `node_modules per worktree, npm cache (shared)` | `the framework’s allowed-hosts setting (Vite: server.allowedHosts)` | ALLOWED_HOSTS | `node` | `npm test` | `registry.npmjs.org` |

Detection evidence (Rails only), from `Main.dc.html:203`: `evidence: 'Gemfile: rails, config/application.rb'`. The per-slot `DETECT` table is at `Main.dc.html:213-216`:

| slot | value | evidence |
|---|---|---|
| database | postgres | `Gemfile: pg` |
| cache | redis | `Gemfile: redis` |
| storage | rustfs | `Gemfile: aws-sdk-s3; config/storage.yml uses S3` |
| worker | none | `no job runner gem` |
| mail | none | `Gemfile: letter_opener (no SMTP)` |
| browser | playwright | `Gemfile: capybara-playwright-driver` |

### 1c. DATABASE_URL scheme per pack (Main `:203-208`, Environment `:131-136`)

| pack | postgres | mysql | mariadb | sqlite (Main) | sqlite (Env) |
|---|---|---|---|---|---|
| rails | `postgres://` | `mysql2://` | `mysql2://` | `sqlite3:storage/development.sqlite3` | same |
| hanami | `postgres://` | `mysql2://` | `mysql2://` | `sqlite://db/development.sqlite` | same |
| phoenix | `ecto://` | `ecto://` | `ecto://` | `path to dev.db (ecto_sqlite3)` | `dev.db` |
| django | `postgres://` | `mysql://` | `mysql://` | `sqlite:///db.sqlite3` | same |
| flask | `postgresql+psycopg://` | `mysql+pymysql://` | `mysql+pymysql://` | `sqlite:///instance/dev.db` | same |
| node | `postgres://` | `mysql://` | `mysql://` | `file:./dev.db` | same |

### 1d. Worker options per pack

The tuple is `[id, label, command, needs]`, from `Main.dc.html:203-208`.

| pack | workers |
|---|---|
| rails | `['sidekiq','Sidekiq','bundle exec sidekiq','cache']`, `['solid','Solid Queue','bin/jobs','database']`, `['goodjob','GoodJob','bundle exec good_job start','postgres']` |
| hanami | `['sidekiq','Sidekiq','bundle exec sidekiq','cache']` |
| phoenix | `['oban','Oban','in-process','database']` |
| django | `['celery','Celery','celery -A [project] worker','cache']`, `['rq','RQ','rq worker','redis']` |
| flask | `['celery','Celery','celery -A [app] worker','cache']`, `['rq','RQ','rq worker','redis']` |
| node | `['bullmq','BullMQ','npm run worker','cache']` |

Review (`:132-137`) uses `solid-queue` where Main uses `solid`. Templates (`:258`) has the same `solid-queue`.

### 1e. Pack environment (Environment `:131-136`)

| pack | env | hosts | debug (worktree layer) |
|---|---|---|---|
| rails | `RAILS_ENV=development` | `DEV_HOSTS={worktree.host}`, `DOMAIN={worktree.host}` | `RUBY_DEBUG_PORT=33007` |
| hanami | `HANAMI_ENV=development` | — | `RUBY_DEBUG_PORT=33007` |
| phoenix | `MIX_ENV=dev` | `PHX_HOST={worktree.host}` | — |
| django | `DJANGO_SETTINGS_MODULE=[project].settings`, `DEBUG=1` | `ALLOWED_HOSTS={worktree.host}`, `CSRF_TRUSTED_ORIGINS=http://{worktree.host}` | — |
| flask | `FLASK_APP=[app]`, `FLASK_DEBUG=1` | `SERVER_NAME={worktree.host}` | — |
| node | `NODE_ENV=development` | `ALLOWED_HOSTS={worktree.host}` | — |

### 1f. Runtimes and version chains (Images `:369-388`)

Each chain is the list of files read in order, first match wins.

| pack | runtime | chain | fallback / val | bump | image tag | base | build args |
|---|---|---|---|---|---|---|---|
| rails, hanami | ruby | `.ruby-version › .tool-versions › Gemfile › Gemfile.lock` | `3.4.2` | `3.4.4` | `ruby{ruby}-node{node}` | `FROM ruby:${RUBY_VERSION}-slim · copies node` | `RUBY_VERSION ← ruby`, `NODE_VERSION ← node` |
| rails, hanami | node | `.nvmrc › .node-version › .tool-versions` | `22.11.0` | `22.12.0` | | | |
| phoenix | elixir | `.tool-versions:elixir › .elixir-version` | `1.17.3` | `1.18.1` | `elixir{elixir}-otp{erlang.major}` | `FROM elixir:${ELIXIR_VERSION}-otp-${OTP_MAJOR}-slim` | `ELIXIR_VERSION ← elixir`, `OTP_MAJOR ← erlang.major` |
| phoenix | erlang | `.tool-versions:erlang` | `27.1` | `27.2` | | | |
| django, flask | python | `.python-version › .tool-versions:python` | `3.12` | `3.13` | `python{python}` | `FROM python:${PYTHON_VERSION}-slim · copies uv` | `PYTHON_VERSION ← python` |
| node | node | `.nvmrc › .node-version › .tool-versions:nodejs` | `22.11.0` | `22.12.0` | `node{node}` | `FROM node:${NODE_VERSION}-slim` | `NODE_VERSION ← node` |

Where each pack reads the Playwright version from (`pw` field):

| pack | Playwright version source |
|---|---|
| rails, hanami | `Gemfile.lock › playwright-ruby-client` |
| phoenix | `pinned (Elixir has no Playwright lockfile)`, fallback `1.60.0` |
| django, flask | `uv.lock › playwright` |
| node | `package-lock.json › @playwright/test` |

The runtimes list in Review (`:132-137`) is: rails/hanami `ruby, node`; phoenix `elixir, erlang`; django/flask `python`; node `node`.

### 1g. Library view (Templates `:245-252`)

Every pack card also shows "Brings: `Containerfile.app, entrypoints, deps.allow`" (markup).

| id | server | versions | hosts | tasks | where |
|---|---|---|---|---|---|
| rails | `bin/dev :3000` | ruby, node | DEV_HOSTS, DOMAIN | `console, test, test:system` | built in |
| hanami | `hanami dev :2300` | ruby, node | none needed | `console, test` | built in |
| phoenix | `mix phx.server :4000` | elixir, erlang | PHX_HOST | `console (iex), test` | built in |
| django | `runserver :8000` | python | ALLOWED_HOSTS, CSRF_TRUSTED_ORIGINS | `console, test, migrate` | **stack-library** |
| flask | `flask run :5000` | python | SERVER_NAME | `console, test` | built in |
| node | `npm run dev :3000` | node | ALLOWED_HOSTS | `console, test` | built in |

### 1h. Django `pack.toml` as shown in Install (`Install.dc.html:150-152`)

```toml
[app]
server  = "uv run manage.py runserver 0.0.0.0:8000"
port    = 8000
install = "uv sync"
[versions]
python  = { from = [".python-version", ".tool-versions:python"], fallback = "3.12" }
[env]
ALLOWED_HOSTS = "{worktree.host}"
```

The Celery `option.toml` (`Install.dc.html:153-155`) has the v2 → v3 diff shown in section 8.

### 1i. App Containerfile text per pack (Images `APPTEXT` `:477-484`)

Every pack's file has the stages `FROM base AS <app>`, `FROM base AS nvim` and `FROM base AS claude`. The targets map to images as follows: `<app>` → `CMD <cmd>`, `nvim` → "editor server", `claude` → "agent" (`:486`).

| pack | key lines |
|---|---|
| rails | `ARG RUBY_VERSION`, `ARG NODE_VERSION`, `FROM node:${NODE_VERSION}-slim AS node`, `FROM ruby:${RUBY_VERSION}-slim AS base`, `COPY --from=node /usr/local/bin /usr/local/bin`, `RUN …`, `FROM base AS rails`, `CMD ["bin/dev"]` |
| hanami | same as rails but `FROM base AS web` and `CMD ["bundle", "exec", "hanami", "dev"]` |
| phoenix | `ARG ELIXIR_VERSION`, `ARG OTP_MAJOR`, `FROM elixir:${ELIXIR_VERSION}-otp-${OTP_MAJOR}-slim AS base`, `RUN apt-get update && apt-get install -y --no-install-recommends \ build-essential git inotify-tools nodejs npm && rm -rf /var/lib/apt/lists/*`, `RUN mix local.hex --force && mix local.rebar --force`, `CMD ["mix", "phx.server"]` |
| django | `ARG PYTHON_VERSION`, `FROM python:${PYTHON_VERSION}-slim AS base`, `COPY --from=ghcr.io/astral-sh/uv:[version] /uv /usr/local/bin/uv`, `ENV UV_PROJECT_ENVIRONMENT=/venv`, `CMD ["uv", "run", "manage.py", "runserver", "0.0.0.0:8000"]` |
| flask | same as django but `CMD ["uv", "run", "flask", "run", "--host", "0.0.0.0"]` |
| node | `ARG NODE_VERSION`, `FROM node:${NODE_VERSION}-slim AS base`, `CMD ["npm", "run", "dev"]` |

### 1j. Tools (Main `:258-261`)

- `claude`: "agent, on demand"
- `nvim`: "editor server"

Both are on by default.

## 2. Option definitions per slot

### Slots (Main `:234`)

| id | label | role |
|---|---|---|
| `database` | Database | data |
| `cache` | Cache and queues | data |
| `storage` | Object storage | data |
| `worker` | Background jobs | worker |
| `mail` | Mail catcher | tool |
| `browser` | Browser | tool |

Review calls the worker slot `jobs` in `[options]` (`:154`). Settings and Install's `option.toml` use `worker`.

### Option catalogue

Sources: Review `OPT` `:139-148`, Images `OPT` `:390-405` and `REG` `:463-475`, Templates `:254-259`, Main `OPTS` and `DETAIL` `:217-231`.

| option | image (full) | pin | tag fmt | next / trial | health | option secret | volume | dump / restore | gives the app | works with |
|---|---|---|---|---|---|---|---|---|---|---|
| postgres | `docker.io/library/postgres:{postgres}` | `16` | `{postgres}` | next `17`; choices `15`, `16.4 (exact)`, `17` | `pg_isready` | `POSTGRES_PASSWORD` | `db-data-pg{postgres.major}` → `db-data-pg16`; new `db-data-pg17`; mount `/var/lib/postgresql/data` | `pg_dump` / `pg_restore` (PROVIDES: `pg_dump -Fc {db}`, `pg_restore -d {db}`) | `DATABASE_URL` | every pack |
| mysql | `docker.io/library/mysql:{mysql}` | `8.4` | `{mysql}` | next `9.1`; choices `8.0`, `8.4.3 (exact)`, `9.1` | `mysqladmin ping` | `MYSQL_ROOT_PASSWORD` | `db-data-mysql{mysql.major}` → `db-data-mysql8`; new `db-data-mysql9` | `mysqldump` / `mysql < dump` | `DATABASE_URL` | every pack |
| mariadb | `docker.io/library/mariadb:{mariadb}` | `11.4` | `{mariadb}` | next `11.8`; choices `10.11`, `11.4.4 (exact)`, `11.8` | `healthcheck.sh --connect` | `MARIADB_ROOT_PASSWORD` | `db-data-mariadb{mariadb.major}` → `db-data-mariadb11`; new `db-data-mariadb12` | `mariadb-dump` / `mariadb < dump` | `DATABASE_URL` | every pack |
| sqlite | `no container` | — | — | — | — | — | file per worktree | — | `DATABASE_URL (a file)` | every pack |
| redis | `docker.io/library/redis:{redis}-alpine` | `8` | `{redis}-alpine` | — | `redis-cli ping` | — | none ("No persistence, dev network only.") | — | `REDIS_URL=redis://cache:6379/1` | every pack |
| valkey | `docker.io/valkey/valkey:{valkey}-alpine` | `8` | `{valkey}-alpine` | — | `valkey-cli ping` | — | — | — | `REDIS_URL` ("Speaks the Redis protocol; REDIS_URL works unchanged.") | every pack |
| memcached (added on this host) | `docker.io/library/memcached:{memcached}-alpine` | `1.6` | `{memcached}-alpine` | — | `nc -z localhost 11211` | — | — | — | `MEMCACHE_SERVERS=cache:11211`, port 11211 | Rails, Django, Flask, Node |
| rustfs | `docker.io/rustfs/rustfs:{rustfs}` | `1.0.0-alpha.83` | `{rustfs}` | — | `curl -f localhost:9000/health` (storage PROVIDES) | `RUSTFS_SECRET_KEY` | `/data`; "Seeded from db-dumps/ when created." | — | `S3_ENDPOINT, S3_ACCESS_KEY` | every pack |
| minio | `quay.io/minio/minio:{minio}` | `RELEASE.[date]` | `{minio}` | — | — | `MINIO_ROOT_PASSWORD` | — | — | `S3_ENDPOINT, S3_ACCESS_KEY` | every pack |
| mailpit | `docker.io/axllent/mailpit:{mailpit}` (Main: `axllent/mailpit:{mailpit}`) | not given | — | — | — | — | — | — | `SMTP_HOST, SMTP_PORT` (`mail, 1025`); ports `1025 smtp, 8025 web` | every pack |
| playwright | `Containerfile.playwright` (built) | `1.60.0` fallback | — | — | — | — | — | — | `PLAYWRIGHT_HOST=ws://browser:8888/connect` | every pack |

Worker options (Templates `:258`):

| id | image | gives / needs | works with | where |
|---|---|---|---|---|
| sidekiq | `app image: bundle exec sidekiq` | needs REDIS_URL | Rails, Hanami | |
| solid-queue | `app image: bin/jobs` | uses DATABASE_URL | Rails | |
| goodjob | `app image: good_job start` | PostgreSQL only | Rails | |
| oban | `in the app process` | uses DATABASE_URL | Phoenix | |
| celery | `app image: celery worker` | needs REDIS_URL | Django, Flask | **stack-library** |
| rq | `app image: rq worker` | needs REDIS_URL | Django, Flask | |
| bullmq | `app image: npm run worker` | needs REDIS_URL | Node | |

### Main short image subtitles (`:218-222`)

| option | subtitle |
|---|---|
| postgres | `postgres:{postgres}` |
| mysql | `mysql:{mysql}` |
| mariadb | `mariadb:{mariadb}` |
| redis | `redis:{redis}-alpine` |
| valkey | `valkey/valkey:{valkey}` (no `-alpine`) |
| rustfs | `rustfs/rustfs:{rustfs}` |
| minio | `minio/minio:{minio}` |
| mailpit | `axllent/mailpit:{mailpit}` |
| playwright | `Containerfile.playwright`, label `Playwright + VNC` |

### Main option detail text (`DETAIL` `:224-231`)

| slot | option | text |
|---|---|---|
| database | postgres | "Health check pg_isready. Dump with pg_dump, restored on a fresh volume named for the major version." |
| database | mysql | "Health check mysqladmin ping. Dump with mysqldump." |
| database | mariadb | "Health check healthcheck.sh --connect. Dump with mariadb-dump." |
| database | sqlite | "No container: a file in each worktree." |
| database | none | "The app gets no DATABASE_URL." |
| cache | redis | "No persistence, dev network only." |
| cache | valkey | "Speaks the Redis protocol; REDIS_URL works unchanged." |
| cache | none | "The app gets no REDIS_URL." |
| storage | rustfs | "S3-compatible, routed at s3.<slug>. Volume seeded from db-dumps/ when created." |
| storage | minio | "S3-compatible, routed at s3.<slug>." |
| storage | none | "Use local disk storage in the app." |
| mail | mailpit | "Catches every email; read them at mail.<slug>.localhost." |
| mail | none | "Mail goes wherever the app’s development config sends it." |
| browser | playwright | "Browser server for system tests, watched at vnc.<slug>.localhost." |
| browser | none | "No system-test browser." |

Worker detail text:

- `No job runner.`
- `<label> runs inside the app; no extra container.` (for an in-process worker)
- `A worker container from the app image, running <cmd>.`

### "Each worktree gets" summary (Main `:264-286`)

Here `H = <slug>.<prefix>.localhost`.

Routes:

| host | target |
|---|---|
| `H` | `<app>:<port>` |
| `s3.H` | `storage:9000` |
| `mail.H` | `mail:8025` |
| `vnc.H` | `browser (noVNC)` |

Containers are named `<prefix>-<svc>@<slug>`:

| unit | role | when |
|---|---|---|
| `<app>` | app | always |
| `deps` | deps | always |
| `worker` | worker | unless the worker is in-process |
| `db` | data | not none and not sqlite |
| `cache` | data | cache chosen |
| `storage` | data | storage chosen |
| `mail` | tool | mail chosen |
| `browser` | tool | browser chosen |
| `nvim` | tool | nvim tool on |
| `claude (on demand)` | tool | claude tool on |

App env:

| variable | value |
|---|---|
| `DATABASE_URL` | `<scheme>…@db/[db]`, or the sqlite path |
| `REDIS_URL` | `redis://cache:6379/1` |
| `S3_ENDPOINT` | `http://storage:9000 (browser: s3.H)` |
| `SMTP_HOST, SMTP_PORT` | `mail, 1025` |
| `PLAYWRIGHT_HOST` | `ws://browser:8888/connect` |
| each pack hostEnv | `H` |

Warnings (`:287-291`):

- `<Worker> needs Redis or Valkey.`
- `RQ needs Redis or Valkey.`
- `GoodJob only runs on PostgreSQL.`
- `<Worker> stores jobs in the database.`
- `<Slot>: you picked <X>, but the repository’s dependencies suggest otherwise. The app may need a different driver.`

### Option-owned env (Environment `:139-147`)

| option | own env | secret |
|---|---|---|
| postgres | `PGTZ={TZ}` | `POSTGRES_PASSWORD` (db user `postgres`) |
| mysql | `MYSQL_DATABASE=[db]` | `MYSQL_ROOT_PASSWORD` (user `root`) |
| mariadb | `MARIADB_DATABASE=[db]` | `MARIADB_ROOT_PASSWORD` (user `root`) |
| rustfs | `RUSTFS_ADDRESS=0.0.0.0:9000`, `RUSTFS_ACCESS_KEY=rustfsadmin`, `RUSTFS_CHECK_UPDATE=false`, `RUSTFS_TELEMETRY=off` | `RUSTFS_SECRET_KEY` |
| minio | `MINIO_ROOT_USER=minioadmin` | `MINIO_ROOT_PASSWORD` |
| playwright (browser svc) | `HEADLESS_SYSTEM_TESTS=0`, `CHROME_START_URL=http://{worktree.host}` | — |

### Links each option suggests (Settings `:317-330`)

| link | from | url |
|---|---|---|
| App | core | `{worktree.host}` |
| Letter opener | Rails pack, "the app has letter_opener_web" | `{worktree.host}/letter_opener` |
| Mailer previews | Rails pack | `{worktree.host}/rails/mailers` |
| Routes | Rails pack | `{worktree.host}/rails/info/routes` |
| Mailpit | Mailpit option | `mail.{worktree.host}` |
| Storage console | RustFS/MinIO option | `s3-ui.{worktree.host}` |
| Browser (VNC) | Playwright option | `vnc.{worktree.host}` |
| Sidekiq | Sidekiq option | `{worktree.host}/sidekiq` |
| GoodJob | GoodJob option | `{worktree.host}/good_job` |
| Logs | core | `logs.localhost (filtered to this worktree)` |

The user's own link is `Admin` → `{worktree.host}/admin`, and the draft link is `Feature flags` → `{worktree.host}/flipper`. Placeholders the user can type: `{worktree.host}`, `{worktree.slug}`, `{branch}`.

## 3. Environment layers (Environment board)

### Layers (`:218-226`, colours `:176-184`)

| n | layer | colour | where stored | desc |
|---|---|---|---|---|
| 1 | Pack (`<Label> pack`) | #BC5215 | `stack pack (<Label>)` | "The framework’s own variables: environment name, allowed hosts, link host. Written in terms of the worktree." |
| 2 | Option | #205EA6 | `the option definitions` | "What each option on step 1 gives the app (DATABASE_URL, REDIS_URL…) and needs itself." |
| 3 | Project | #536907 | `project.toml [env]` | "Every service. Saved in templates." |
| 4 | Service | #3E6E8E | `project.toml [services.<id>.env]` | "One service only. Saved in templates." |
| 5 | Worktree | #5E409D | `.unit-env/<slug>.env` | "Hosts and ports worked out per worktree. Read-only." |
| 6 | Secret | #AF3029 | `mise.local.toml` | "Names in project.toml, values only on this host, never in a template." |
| 7 | wt:share | #8E6B01 | `.unit-env/<slug>.share.env` | "Only while a worktree is shared over the tailnet; replaces the pack’s host variable." |

The last layer to set a name wins; earlier rows for that name are struck through. Worktree and wt:share rows cannot be edited. The "Set for" choices when adding a variable are `Every service`, `This service only` (the default) and `Secret (stored locally)`. The board also notes: "Build args are not runtime environment."

### Every variable, per service

Defaults are stack rails, postgres, redis, rustfs, playwright, and `H = my-feature.localhost`. Source lines are `:168-205`.

| service | var | layer | value |
|---|---|---|---|
| app / nvim / claude | RAILS_ENV | pack | `development` |
| app / nvim / claude | DEV_HOSTS | pack | `{worktree.host}` (struck on app, overridden by wt:share) |
| app / nvim / claude | DOMAIN | pack | `{worktree.host}` |
| app / nvim / claude | DATABASE_URL | option | `postgres://postgres:${DB_PASSWORD}@db/[db]` (mysql/mariadb use user `root`; sqlite uses the pack path) |
| app / nvim / claude | REDIS_URL | option | `redis://cache:6379/1` |
| app / nvim / claude | S3_ENDPOINT | option | `http://storage:9000` |
| app / nvim / claude | S3_ACCESS_KEY | option | `rustfsadmin` (MinIO: `minioadmin`) |
| app / nvim / claude | PLAYWRIGHT_HOST | option | `ws://browser:8888/connect` |
| all services | TZ | project | `Asia/Bangkok` |
| app | HEADLESS_SYSTEM_TESTS | service | `0` (when a browser is chosen) |
| nvim, claude | EDITOR | service | `nvim` |
| nvim, claude | SSH_AUTH_SOCK | service | `/tmp/ssh-agent.sock` |
| claude | CLAUDE_FIREWALL_ALLOW | service | `` (shown as "(empty)") |
| app / nvim / claude / browser | WORKTREE_HOST | worktree | `my-feature.localhost` |
| app | RUBY_DEBUG_PORT | worktree | `33007` (rails, hanami) |
| app / nvim / claude | DB_PASSWORD | secret | `••••••••` |
| app / nvim / claude | S3_SECRET_KEY | secret | `••••••••` |
| nvim, claude | GH_TOKEN | secret | `passed through from host` |
| app | DEV_HOSTS (first pack host var) | wt:share | `my-feature.tail1234.ts.net` |
| deps | RAILS_ENV | pack | `development` |
| deps | PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD | service | `1` |
| db | PGTZ | option | `{TZ}` |
| db | DB_PORT | worktree | `55439 (127.0.0.1 only)` |
| db | POSTGRES_PASSWORD | secret | `••••••••` |
| storage | RUSTFS_ADDRESS | option | `0.0.0.0:9000` |
| storage | RUSTFS_ACCESS_KEY | option | `rustfsadmin` |
| storage | RUSTFS_CHECK_UPDATE | option | `false` |
| storage | RUSTFS_TELEMETRY | option | `off` |
| storage | RUSTFS_SECRET_KEY | secret | `••••••••` |
| browser | HEADLESS_SYSTEM_TESTS | option | `0` |
| browser | CHROME_START_URL | option | `http://{worktree.host}` |
| browser | WORKTREE_HOST | worktree | `my-feature.localhost` |

The service tabs are, in order: `<app>` (app), `deps` (deps), `db`, `cache`, `storage` (data), `browser` (tool), `nvim` (tool), `claude` (tool). The cache service has only `TZ`.

## 4. CLI commands and step lists (verbatim)

**Create runs** (Review `:247-253`). With the defaults this reads "4 options" and "Build 4 images, pull 3".

1. `Clone the repository into master/`
2. `Write project.toml, stack.lock (every pack, option and image pinned), and secrets to mise.local.toml`
3. `mise run units:install: expand the <Pack> pack and <N> options into units`
4. `mise run doctor`
5. `Build <3 + browser?1:0> images, pull <pins.length>, then mise run up in master`

**Settings: when you apply** (`Settings.dc.html:206-210`):

1. `project.toml and stack.lock are written; new options are reviewed first if they come from a library.`
2. `units:install renders the changed units.`
3. Either `The <N> affected running worktrees restart now, one at a time.` or `Running worktrees keep their current containers until their next restart; the dashboard marks them as waiting for a restart.`

**Adopt** (markup `:114-116`): "Adopt writes `project.toml` and `stack.lock`, then runs `units:install`. Worktrees pick it up on their next restart; volumes and worktree IDs are untouched." Backing out: "Your current files stay in `.container-config/`, so backing out is deleting project.toml and running units:install again."

**Moving from one proxy per project** (`Machine.dc.html:156-162`):

1. `filial_proxy` stays as Filial’s proxy network, with the same subnet (10.214.0.0/24) and the first slot in the pool.
2. `filial-traefik`, `filial-dozzle` and `filial-dashboard` stop; `wt-traefik` starts on the same ports and joins `filial_proxy` at the address the old one had, 10.214.0.2.
3. Worktrees need no restart: their network, firewall annotation and `--add-host` entries are unchanged. Routes drop for the seconds between the two Traefiks.
4. With short names on for Filial, every current URL keeps working.

The button reads "Move to the shared proxy".

**Other commands and tasks named on the boards:**

- `mise run versions:set <ver> <choice> [--worktree <name>]` (Images `:546`). The example is `mise run versions:set postgres 17`.
- `db:dump` (Images PROVIDES).
- `clean`, `up`, `units:install`, `doctor`.
- `test:system` (Settings browser effect, Templates rails tasks).
- `wt:share`.
- `podman network connect --ip`, `allow-ports`, `podman.socket` (Machine checks).
- `Look up tags` (Images dialog).

## 5. Adopt board

### Scan header (`Adopt.dc.html:44-49`)

- "Scanned: filial, master and every linked worktree"
- "12 unit templates · 2 Containerfiles · 5 entrypoints · `.env` · `local.toml.template` · 2 firewall allowlists · `ports.registry`"
- Closest match: `rails@1`, `postgres@1`, `redis@1`, `rustfs@1`, `playwright@1`

### Mapping table (`R` `:144-159`)

The status categories (`:137-143`) are `match` = Matches #536907, `core` = Core #205EA6, `override` = Kept as override #5E409D, `local` = This machine only #8E6B01 and `change` = Changes #AF3029.

| found | what | becomes | category |
|---|---|---|---|
| `rails@.container` | bin/dev on :3000, routed by Traefik, worktree at /app | Service rails, role app, from the Rails pack | match |
| `deps@.container + entrypoint-deps.sh` | bundle install, npm install, foreman, retest | Role deps. The pack installs gems and npm; foreman and retest stay as your additions | override |
| `Dockerfile (targets rails, nvim, claude)` | Ruby + Node base, Neovim built from source, Claude Code | Containerfile.app. Differs from the pack’s, so it is kept as your edited copy | override |
| `Dockerfile.playwright + entrypoint-playwright.sh` | Playwright server, Xvfb, VNC, its own firewall | Containerfile.playwright from the Playwright option | match |
| `db@.container + initdb/restore-dump.sh` | postgres:16, fsync off, restore on an empty volume | database = postgres@1, pin 16 | match |
| `redis@.container` | redis:8-alpine, no persistence | cache = redis@1, pin 8 | match |
| `rustfs@ + rustfs-init@` | RustFS 1.0.0-alpha.83, restore from rustfs_data.tar.gz | storage = rustfs@1. rustfs-init becomes a volume seed (see decisions) | change |
| `.container-config/.env` | RAILS_ENV, DEV_HOSTS, PG*, REDIS_URL, PLAYWRIGHT_HOST, TZ, and 23 more | Split: pack (RAILS_ENV, DEV_HOSTS), options (PG*, REDIS_URL, PLAYWRIGHT_HOST), the rest as project env | override |
| `local.toml.template` | Ports 55432+ID, 13000+ID…, hosts, versions from .ruby-version, .nvmrc, Gemfile.lock | Worktree layer and version sources. Same formulas, same values | match |
| `firewall/deps.allow, agent.allow` | Registries for deps; agent allowlist | The pack’s deps.allow plus your extra entries; agent.allow stays core | override |
| `nvim@.container, podman-claude.sh` | Editor server and agent, SSH agent and git config mounts | Tools, from core | core |
| `Kitty socket and kitten mounts on nvim and claude` | %h/.local/state/kitty, /usr/bin/kitten | Local override in mise.local.toml. Never in a template | local |
| `traefik, dozzle, proxy.network, dashboard.service` | Proxy, logs, dashboard, run by this project alone | Replaced by the machine’s shared wt-traefik, wt-dozzle and wt-dashboard. filial_proxy stays as this project’s own network; wt-traefik joins it at 10.214.0.2 | change |
| `ports.registry, .home/, claude-memory/, git-hooks/` | Worktree IDs, agent homes, shared memory, hook backups | Unchanged | core |

Filter counts: All 14, Matches 5, Overrides 5 (4 override + 1 local), Changes 2, Core 2.

### Decisions (`DEC` `:168-173`)

The defaults are `pg: ''` (still open), `vols: 'keep'`, `init: 'seed'`, `hosts: 'keep'`. While any decision is open the button reads `Decide N more to adopt`; otherwise `Adopt`.

| id | title | why | choices (value: label, desc) |
|---|---|---|---|
| pg | The app reads PGHOST, not DATABASE_URL | Today the app gets PGHOST, PGPORT and PGUSER from .env. The postgres option gives DATABASE_URL. | `pg`: Also give the PG* variables, "The option sets PGHOST=db, PGPORT, PGUSER as well. Nothing in the app changes." · `url`: Switch the app to DATABASE_URL, "A change to database.yml in the app repo, made on a branch first." |
| vols | Volume names | Existing volumes are named filial-<slug>-db-data. The option names them by major version (db-data-pg16). | `keep`: Keep the existing names, "No data moves. Version-named volumes start with the next major version change." · `rename`: Rename on each worktree’s next restart, "Copies the data into the new volume, then keeps the old one until clean." |
| init | rustfs-init | A oneshot that untars rustfs_data.tar.gz into an empty volume. | `seed`: Replace it with a volume seed, "Same result: the tarball is unpacked when the volume is first created." · `keep`: Keep it as a oneshot, "Stays as a service of its own." |
| hosts | Proxy and hostnames | filial runs its own Traefik on :80, so no second project can. Adopting moves it to the machine’s shared proxy (see This machine), where every worktree is <slug>.filial.localhost. | `keep`: Shared proxy, keep short names, "Worktrees answer at <slug>.filial.localhost and still at <slug>.localhost, so current URLs and OAuth callbacks keep working." · `project`: Shared proxy, project names only, "Only <slug>.filial.localhost. Bookmarks, OAuth callbacks and DEV_HOSTS change." |

### Dry-run diff (`FILES` `:181-200`)

The header reads "What units:install would change".

**`filial-rustfs@.container`**

With `init=seed` the summary is "2 lines":

```
  [Unit]
  Description=RustFS S3 storage for worktree %i
- Requires=filial-net-network@%i.service filial-rustfs-init@%i.service
- After=filial-net-network@%i.service filial-rustfs-init@%i.service
+ Requires=filial-net-network@%i.service
+ After=filial-net-network@%i.service
+ # rustfs-data is seeded by `up` when the volume is first created
```

Note: "The ordering on rustfs-init goes; up seeds the volume instead."

With `init=keep` the summary is "header only": `Description=… %i` becomes `Description=RustFS S3 storage for worktree %i (rustfs@1)`.

**`filial-rustfs-init@.container`**

With `init=seed` the summary is "removed": every line of `[Unit]`, `Description=Restore RustFS data for worktree %i if a backup exists`, `…`, `Type=oneshot` and `RemainAfterExit=yes` is deleted. Note: "Removed when units:install runs. A worktree already running keeps its restored data." With `init=keep` it is "unchanged".

**`.container-config/.env`** (summary "split")

```
  RAILS_ENV=development           → from the Rails pack
  DEV_HOSTS=rails                → from the Rails pack
  PGHOST=db                      → from the postgres option[ (replaced by DATABASE_URL)]
  REDIS_URL=redis://redis:6379/1 → from the redis option
- REDIS_URL=redis://redis:6379/1
+ REDIS_URL=redis://cache:6379/1
  TZ=Asia/Bangkok                → project env
  TAILWINDCSS_DEBUG=1            → project env
```

Note: "The cache service is called cache in the new model. Keeping its old name, redis, is one line in project.toml if anything else refers to it."

**`filial-db@.container`**

With `vols=keep` the summary is "header only" and the unit is unchanged:

```
[Container]
Image=docker.io/library/postgres:16
Volume=filial-%i-db-data:/var/lib/postgresql/data
```

With `vols=rename` the summary is "1 line":

```
- Volume=filial-%i-db-data:/var/lib/postgresql/data
+ Volume=filial-%i-db-data-pg16:/var/lib/postgresql/data
```

**`filial-rails@.container`** (summary "header only")

```
+ # Rendered from project.toml (rails@1). Edit project.toml, not this file.
  [Unit]
  Description=Rails dev server for worktree %i
```

**`8 other units`** (summary "header only")

`deps@, redis@, playwright@, nvim@, net@.network,` and `traefik, dozzle, proxy.network`.

## 6. Machine board

### Projects (`:179-184`)

Each project's network is `<prefix>_proxy` on subnet `10.214.<n>.0/24`, with Traefik at `10.214.<n>.2`.

| name | prefix | worktrees | IDs | stack | network | subnet | Traefik |
|---|---|---|---|---|---|---|---|
| Filial Rails | filial | 6 | `1–6` | rails@1 | filial_proxy | 10.214.0.0/24 | 10.214.0.2 |
| Billing sandbox | billing | 2 | `7, 8` | rails@1 | billing_proxy | 10.214.1.0/24 | 10.214.1.2 |
| Phoenix demo | phx | 1 | `9` | phoenix@1 | phx_proxy | 10.214.2.0/24 | 10.214.2.2 |

### Hostnames

- Worktree hosts are `<slug>.<prefix>.localhost`.
- Short names (`:186-189`): `Filial Rails keeps <slug>.localhost too` or `No short names`. "Only one project can have short names."
- Example hosts (`:190-196`):
  - `master.filial.localhost`
  - `master.localhost` (short name)
  - `master.billing.localhost`
  - `master.phx.localhost`
  - `s3.master.filial.localhost` → Filial Rails · storage
  - `vnc.master.phx.localhost` → Phoenix demo · browser

### Proxy (`:100-105`)

| field | value |
|---|---|
| Unit | `wt-traefik.service` |
| Listens on | `127.0.0.1:80, API 127.0.0.1:8080` |
| Subnet pool | `10.214.0.0/16, one /24 per project · next free 10.214.3.0/24` |
| Logs | `logs.localhost (Dozzle, every project; filter by prefix)` |
| Routes | 27 from 3 projects |

Each container gets the label `traefik.docker.network=<prefix>_proxy` and `--add-host` entries that point at `.2`.

Checks:

- port 80 allowed for rootless podman (`allow-ports`)
- every pool subnet free or ours
- Traefik on every project network
- `podman.socket` on

### Ports (`:197-200`)

| what | formula |
|---|---|
| App, for wt:share | `13000 + ID` |
| Database | `55432 + ID` |
| Ruby debugger | `33000 + ID` |
| Neovim | `17000 + ID` |
| Playwright | `18888 + ID` |
| Storage, for wt:share | `19000 + ID` |

The registry is `~/.local/share/wt/ports.registry (project:slug:ID)` and the next free ID is `10`. Before an ID is handed out, its ports are checked against what is already listening.

### Dashboard (`:146-149`)

| field | value |
|---|---|
| Address | `http://wt.localhost:7700` |
| Unit | `wt-dashboard.service` |
| Worktree page | `/<prefix>/<slug>` |
| Start at login | On |

"Actions still run in the project’s own workspace root with its own mise tasks."

## 7. Settings board

### Current and staged values (`:225-233`)

| slot | current | staged |
|---|---|---|
| database | postgres | postgres |
| cache | redis | **valkey** |
| storage | rustfs | rustfs |
| worker | none | none |
| mail | none | **mailpit** |
| browser | playwright | playwright |

The worker options in Settings are `none`, `sidekiq`, `solid`, `goodjob`.

Scopes (`:244` and markup):

| value | select label | short label |
|---|---|---|
| `all` | Every worktree | every worktree |
| `trial` | Only pg17-trial, to try it | pg17-trial only |
| `patient-photos` | Only patient-photos, to try it | patient-photos only |

A staged card's title is `<Slot label>: <cur> → <new>`. Its scope line reads `Every worktree` or `Only <x> (override)`, and each card has "Undo this change".

### Data-impact labels (`LVL` `:263`)

| level | label | colour | border |
|---|---|---|---|
| safe | `No data affected` | #536907 | #CECDC3 |
| care | `Check before applying` | #8E6B01 | #D9B23A |
| data | `Data not carried over` | #AF3029 | #AF3029 |

### Effect text per transition (`effects` `:264-295`)

**cache**

- → none (care): "The cache container and REDIS_URL go." plus either "Sidekiq needs it: this change is blocked until jobs change too." or "Anything in the app using Action Cable or Rails.cache with Redis needs another adapter."
- from none (safe): "A <N> container starts; the app gets REDIS_URL."
- otherwise (safe): "<N> speaks the same protocol; REDIS_URL stays the same. The cache keeps nothing on disk, so there is nothing to move."

**mail**

- → mailpit (care): "A Mailpit container starts, routed at mail.<slug>.filial.localhost; the app gets SMTP_HOST and SMTP_PORT." and "The app uses letter_opener in development today, so it keeps opening mail itself until its delivery method is SMTP."
- → none (safe): "The Mailpit container and its route go; caught mail is discarded."

**database**

- → none (data): "The db container stops being started. Its volumes are kept until clean."
- → sqlite (data): "Each worktree starts with an empty SQLite file. Existing PostgreSQL volumes are kept until clean." and "The app needs the sqlite3 gem and an sqlite3 adapter in database.yml."
- other (data):
  - "Data is not converted. A new <N> volume starts empty, and db-dumps/ holds PostgreSQL dumps, which <N> cannot load."
  - "The app needs the <pg | mysql2 or trilogy> gem; the repository has pg."
  - "PostgreSQL volumes are kept until clean, so switching back finds your data."
  - "Try it on one worktree first."

**storage**

- → none (care): "The storage container and s3 routes go; Active Storage must use a local service."
- otherwise (data): "Objects are not copied. The new volume is seeded from db-dumps/rustfs_data.tar.gz when it is created, as RustFS was."

**browser**

- → none (care): "The playwright container, the vnc route and test:system go. The agent loses its browser tools."
- otherwise (safe): "A playwright container starts with VNC at vnc.<slug>.filial.localhost."

**worker**

- → none (safe): "The worker container goes; queued jobs stay in <the cache | the database>."
- otherwise (care):
  - "A worker container starts from the app image."
  - One of: "Needs a cache: choose Redis or Valkey." / "Uses the cache for its queue." / "Uses PostgreSQL for its queue." / "Uses the database for its queue."
  - "The app needs the <N> gem configured as its queue adapter."

The affected-worktree count is 3 if any change applies to every worktree, otherwise 1. The restart toggle reads "Restart the <N> affected running worktrees now".

### Updates (`:309-314`)

| ref | state |
|---|---|
| `rails@1` | Built in, locked to wt [version]. Up to date. |
| `postgres@1` | Built in. Up to date. |
| `rustfs@1` | Built in. Image pinned at 1.0.0-alpha.83; newer tags exist on the registry. (has "Review update") |
| `template v3` | rails-postgres-redis-rustfs: up to date. |

### Project panel (markup `:55-60`)

| field | value |
|---|---|
| Name | Filial Rails |
| Repository | `git@github.com:[ORG]/[REPO].git · base branch master` |
| Prefix | `filial` (fixed) |
| Hostnames | `<slug>.filial.localhost`, short names on |
| Started from | `rails-postgres-redis-rustfs v3` |
| Pack | `rails@1`, locked at `wt [version]` |

Remove project (`:178-183`) offers "Also delete volumes (databases, storage)" and "Also delete worktree folders without uncommitted changes".

## 8. Install board

Title: `Update django-celery from v2 to v3`.

| field | value |
|---|---|
| Library | `git@github.com:[ORG]/stack-library.git` |
| Commit | `3c7d012 → 9b1e4a7` (update mode) / `9b1e4a7` |
| Signed by | `A key you trusted on first install` |

The modes are `Changes since v2` and `Everything`.

### Capability checks (`:161-168`)

| label | verdict | text | kind |
|---|---|---|---|
| Firewall | 1 new host | deps.allow adds objects.githubusercontent.com, which is not a package registry. The agent allowlist is unchanged. | flag |
| Runs on this machine | 1 flagged | Containerfile.app downloads a script with curl and pipes it to sh during the build, outside any firewall. | flag |
| Host mounts | none | Mounts only the worktree and its own volumes. Nothing from your home directory or /. | ok |
| Published ports | none | Nothing is published beyond loopback; routing goes through Traefik. | ok |
| Privileges | none | No added capabilities, no privileged mode, no devices. | ok |
| Images | 2 pulled | python:3.12-slim and ghcr.io/astral-sh/uv; digests recorded on first pull. | ok |

The kinds `flag`, `stop` and `ok` have colours #8E6B01, #AF3029 and #536907. The ack checkbox reads "I’ve read the 2 flagged items", and the buttons are `Update and lock` / `Install and lock` and "Not now".

### Files (`F` `:137-158`)

| group | file | what | tag |
|---|---|---|---|
| django@1 | `firewall/deps.allow` | hosts deps@ may reach while installing | 1 flagged |
| django@1 | `Containerfile.app` | RUN lines run on this machine during the build | 1 flagged |
| django@1 | `entrypoints/entrypoint-app.sh` | runs in the app container at every start | changed |
| django@1 | `pack.toml` | install, server, tasks, version sources, env | unchanged |
| celery@1 | `option.toml` | image, command, needs, env | changed |

- `deps.allow` holds `pypi.org`, `files.pythonhosted.org`, `github.com` and the flagged line `objects.githubusercontent.com    # not a package registry`.
- The flagged `Containerfile.app` line is `RUN curl -fsSL https://[host]/install-tools.sh | sh    # downloads and runs a script`.
- `entrypoint-app.sh`: `#!/bin/sh`, `set -eu`, `uv run manage.py migrate --noinput`, then the added line `+ uv run manage.py collectstatic --noinput --clear`, then `exec "$@"`.
- The `option.toml` diff:

```
  slot    = "worker"
  image   = "app"
- command = "celery -A {project} worker"
+ command = "celery -A {project} worker --concurrency {CELERY_CONCURRENCY}"
  needs   = ["cache"]
+ ask     = { CELERY_CONCURRENCY = "2" }
```

### stack.lock preview (`:172-190`)

In update mode the commit and sha256 lines are highlighted as new.

```toml
# Written by wt. Do not edit; review updates in the dashboard.
[pack.django]
version = "1"
source  = "git@github.com:[ORG]/stack-library.git"
commit  = "9b1e4a7"
sha256  = "[content hash]"

[option.celery]
version = "1"
commit  = "9b1e4a7"
sha256  = "[content hash]"

[option.postgres]
source  = "built in (wt [version])"

[image.postgres]
ref     = "docker.io/library/postgres:16"
digest  = "sha256:[digest at first pull]"
```

Locking rules (markup):

- "Built-in packs and options are locked to the version of wt itself."
- "Ones you made on this host are locked by content hash."
- "An unsigned library, or a signer you have not trusted before, is shown but cannot be installed until you trust it."

### project.toml render (Review `:172-230`)

These are the default values (rails, postgres, redis, rustfs, jobs none, playwright). Keys are padded to 9 characters.

```toml
# Rendered by units:install. Edit here, then re-run it.
from = "rails-postgres-redis-rustfs@1"

[project]
name        = "Filial Rails"
prefix      = "filial"
repo        = "git@github.com:[ORG]/[REPO].git"
base_branch = "master"

[stack]
pack = "rails@1"
app  = "rails"   # the service with role app

[options]
database = "postgres@1"
cache    = "redis@1"
storage  = "rustfs@1"
jobs     = "none"
mail     = "none"
browser  = "playwright@1"

[versions]   # runtimes (ruby, node) come from the app’s files
postgres = "16"
redis    = "8"
rustfs   = "1.0.0-alpha.83"

# Only what differs from the pack and options:
[env]
TZ = "Asia/Bangkok"

[services.rails.env]
HEADLESS_SYSTEM_TESTS = "0"

[worktrees.postgres-trial.versions]
postgres = "17"

[secrets]   # values live in mise.local.toml
names = ["DB_PASSWORD", "S3_SECRET_KEY"]
```

The "Expanded" view inserts the following after `[versions]`. These lines are not stored.

```toml
# ── filled in by units:install from the pack and options ──
[services.rails]
role    = "app"
image   = "rails"   # Containerfile.app › rails
command = "bin/dev"
route   = "{worktree.host} -> 3000"

[services.deps]
role    = "deps"
command = "bundle install && npm install"

[services.db]   # from PostgreSQL
role   = "data"
image  = "docker.io/library/postgres:{postgres}"
health = "pg_isready"
volume = "db-data-pg{postgres.major}:…"

[services.cache]   # from Redis
role   = "data"
image  = "docker.io/library/redis:{redis}-alpine"
health = "redis-cli ping"

[services.storage]   # from RustFS
role   = "data"
image  = "docker.io/rustfs/rustfs:{rustfs}"

[services.worker]   # from <Job>   (only when jobs ≠ none/oban)
role  = "worker"
image = "<app>"

[services.browser]   # from Playwright
role  = "tool"
route = "vnc.{worktree.host} -> novnc"
```

Views (`:226-238`):

- `As project`
- `Expanded`: purple lines, not stored.
- `As template`: red `p` lines (`from`, `[project]` block, `[worktrees.*]`) are dropped; yellow `s` lines (`[options]`, `[versions]` pins, `TZ`) become questions.

Template name: `<stack>-<db>-<cache>-<storage>-<jobs>`, skipping `none`. The browser is not part of the name.

Save as a template (markup `:97-112`):

- Include: "Pack and options: <refs>" (locked on), "Your environment and overrides", "Containerfiles you edited, and options you added", "Secret names, as questions to answer", and "Seed list and Claude home template" (off).
- "Never included: secret values, project name, prefix, repository, ports registry, per-worktree overrides, db dumps."
- Save to: `This host’s library`, `Download as .stack.tar.gz`, `Push to a template repository`.

## 9. Images board

### Images table (`:422-433`)

| name | kind | from | versions | brought by | used by |
|---|---|---|---|---|---|
| `<app>` | built | `Containerfile.app › <app>` | runtime names | `<Pack> pack` | `<app>, deps` |
| `nvim` | built | `Containerfile.app › nvim` | runtime names | `<Pack> pack (tool target)` | nvim |
| `claude` | built | `Containerfile.app › claude` | runtime names | `<Pack> pack (tool target)` | claude |
| `playwright` | built | `Containerfile.playwright` | playwright | Playwright option | browser |
| `<ver>` per chosen db, cache or storage | registry | `<ref>:<fmt>` | `<ver> = <pin>` | `<Label> option` | db / cache / storage |

### Containerfiles (`:434-435`)

**`Containerfile.app`** (from the pack) builds `<app>`, `nvim` and `claude`, with the pack's args.

**`Containerfile.playwright`** (from the Playwright option): base `FROM mcr.microsoft.com/playwright:v${PLAYWRIGHT_VERSION}-noble`, arg `PLAYWRIGHT_VERSION ← playwright`. Full text (`:487`):

```
ARG PLAYWRIGHT_VERSION=1.60.0
FROM mcr.microsoft.com/playwright:v${PLAYWRIGHT_VERSION}-noble
RUN apt-get update -qq && apt-get install --no-install-recommends -y \
  xvfb x11vnc novnc websockify x11-utils socat iptables && rm -rf /var/lib/apt/lists/*
ARG PLAYWRIGHT_VERSION
RUN npm install -g playwright-core@${PLAYWRIGHT_VERSION}
EXPOSE 8888 8080 9223
```

It is the only stage, and the image's entrypoint is "entrypoint mounted by the unit".

The example user-built file `Containerfile.search` (`:488`):

```
FROM docker.io/getmeili/meilisearch:[version] AS search
COPY search-settings.json /meili/settings.json
CMD ["meilisearch", "--env", "development"]
```

Names `app` and `playwright` are taken. A new file is saved as `.container-config/Containerfile.<name>`.

### Versions table (`:437-450`)

| kind | colour | value | notes |
|---|---|---|---|
| Runtime: "app files, first found" | #5E409D | `per worktree` | chain plus fallback; note "Master: found in <file> (green)." |
| playwright: "app lockfile" | | `per worktree` | Phoenix: "pinned", value `1.60.0` |
| Option pin: "pinned by the <Label> option" | #205EA6 | `<pin>` | |

Only the database pin can be changed. Its note: "Data volume named for the major version (<vol>); a new major starts a fresh volume restored from the dump." Every other pin notes "Keeps no data that matters, so any change is safe."

### Built images per worktree (`:453-458`)

The columns are the runtimes plus playwright (except on Phoenix).

| worktree | cells | status |
|---|---|---|
| `master` | base values | Built |
| `patient-photos` | base values | Shares master’s |
| `<runtime0>-bump` (e.g. `ruby-bump`) | runtime0 = bump, e.g. `3.4.4` | Own <app>, nvim, claude tags |
| `legacy-import` | `<fallback> (fallback)` | No version file; shares master’s |

### Add-option dialog (`:182-330`, `:460-541`)

**"Offer it as"** values:

| value | label |
|---|---|
| `cache` | An option under Cache and queues |
| `database` | An option under Database |
| `storage` | An option under Object storage |
| `mail` | An option under Mail catcher |
| `worker` | An option under Background jobs |
| `service` | A new service on step 1 |
| `image` | An image only, to build other images on |

**Name**: lower-case `[a-z0-9-]`, default `memcached`.

**Source**: `registry` ("Pull from a registry") or `build` ("Build from a Containerfile").

**Registry fields**:

- Image on the registry: `docker.io/library/memcached`
- Pinned version: `1.6`
- Tag: `{memcached}-alpine`
- Resolves to: `docker.io/library/memcached:1.6-alpine`
- Sample tags: `latest`, `1.6`, `1.6-alpine`, `1.6.34`

Helper text: "The tag follows the pin… Use a digest for anything you share."

**Build fields**:

- Containerfile name (prefix `Containerfile.`)
- Content mode: `Current file` (edit only), `Paste`, `Upload`, `From a template`, `From app repo`
- Targets → images
- Build args, e.g. "linked to <ver>"
- Ack checkbox "I’ve read this file", with the warning "Every RUN line runs on this machine during the build, outside the containers’ firewall."

**"What it provides"** fields per offer (`PROVIDES` `:505-513`):

| offer | fields |
|---|---|
| cache | Port `11211`; Health check `nc -z localhost 11211`; Gives the app `MEMCACHE_SERVERS=cache:11211`; Works with `Rails, Django, Flask, Node` |
| database | Port `5432`; Health check `pg_isready`; Gives the app `DATABASE_URL={scheme}…@db/{db}`; Data volume `/var/lib/postgresql/data`; Dump `pg_dump -Fc {db}`; Restore `pg_restore -d {db}` |
| storage | Ports `9000 api, 9001 console` (routed at s3. and s3-ui.<slug>.); Health check `curl -f localhost:9000/health`; Gives the app `S3_ENDPOINT, S3_ACCESS_KEY`; Data volume `/data` |
| mail | Ports `1025 smtp, 8025 web`; Gives the app `SMTP_HOST=mail, SMTP_PORT=1025` |
| worker | Command `from the app image`; Needs `cache` |
| service | Port `7700`; Route `search.<slug>.localhost`; Health check `curl -f localhost:7700/health`; Gives the app `SEARCH_URL=http://search:7700` |
| image | (none) |

The buttons are `Add option` / `Save changes`, and the dialog title is `Edit <name>` / `Add an option`.

### Pin dialog (`:525-547`)

- Title: "Change the <Label> version".
- Now: `16`. Choices: `15`, `16.4 (exact)`, `17`, `Image digest…`.
- Apply to: "Every worktree" ("Changes the pin in project.toml.") or "One worktree, to try it" ("Adds an override for that worktree; the pin stays 16.").
- What happens: a new volume `db-data-pg17` is restored with `pg_restore`, and the old volume is kept until `clean`.
- Warning: "The dump is 9 days old." with the checkbox "Dump master first (pg_dump)".
- Command: `mise run versions:set postgres 17[ --worktree <name>]`.
- Buttons: `Change pin` / `Add override`.

## 10. Dashboard board

### Palette presets

From `Dashboard.dc.html:300-305`. These are light-mode values only.

| token | Flexoki | Paper | Solarized | High contrast |
|---|---|---|---|---|
| bg | #FFFCF0 | #FFFFFF | #FDF6E3 | #FFFFFF |
| bg2 | #F2F0E5 | #F4F4F5 | #EEE8D5 | #F0F0F0 |
| sel | #E6E4D9 | #E4E4E7 | #E4DCC4 | #DADADA |
| border | #CECDC3 | #D4D4D8 | #D3CBB2 | #7A7A7A |
| text | #100F0F | #18181B | #073642 | #000000 |
| muted | #6F6E69 | #62626B | #586E75 | #3D3D3D |
| accent | #BC5215 | #7C3AED | #B03F0F | #B3261E |
| blue | #205EA6 | #1D4ED8 | #1B6699 | #0040A8 |
| green | #536907 | #15803D | #5C6B00 | #1E5E00 |
| yellow | #8E6B01 | #A16207 | #7A5C00 | #6B4E00 |

The preset keys are `flexoki`, `paper`, `solar` and `contrast`.

**There are no dark-mode hex values in any file.** The markup at `:130` says only: "Dark mode keeps its own set: switch the system to dark and the same editor edits that one."

Editable tokens (`TOK` `:306-309`). The `sel` and `border` tokens are not editable.

| token | label | used for |
|---|---|---|
| bg | Background | page |
| bg2 | Panels | header, headings |
| text | Text | names, values |
| muted | Muted | secondary text |
| accent | Accent | ◆ needs you, selection |
| blue | Working | ● working, links |
| green | Running | ● services up |
| yellow | Your turn | warnings |

- Contrast check: any token other than bg/bg2 with a WCAG ratio below 4.5:1 against bg triggers a warning; saving still works.
- Saved to: `~/.config/wt/dashboard.toml`.
- TOML format (`:319`):

```toml
[colors.light]
base = "flexoki"
accent = "#......"   # one line per customised token
```

When any token is edited, the preset shows as "Custom".

### Agent states (`AG` `:230-237`)

The tuple is label, colour, weight, needs-input.

| state | label | colour | weight | needs input |
|---|---|---|---|---|
| permission | `◆ needs permission` | #BC5215 | 800 | yes |
| reply | `◆ waiting for reply` | #BC5215 | 800 | yes |
| working | `● working` | #205EA6 | 400 | no |
| turn | `· your turn` | #8E6B01 | 400 | no |
| idle | `· idle` | #575653 | 400 | no |
| off | `○ off` | #575653 | 400 | no |

### Service states (`SV` `:238`)

| state | symbol | colour | word |
|---|---|---|---|
| up | ● | #536907 | up |
| down | ○ | #575653 | stopped |
| part | ◐ | #8E6B01 | partly up |

### Switcher data (`P` `:239-251`)

Each worktree tuple is name, branch, PR, service state, agent state.

**Filial Rails** (`filial`, rails@1). Services: app, deps, db, cache, storage, browser.

| worktree | branch | PR | services | agent |
|---|---|---|---|---|
| master | master | — | up | idle |
| patient-photos | jb/patient-photos | #2141 | up | permission |
| billing-export | jb/billing-export | #2138 | up | working |
| pdf-reports | jb/pdf-reports | #2120 | part | turn |
| ui-refresh | jb/ui-refresh | — | down | off |

**Billing sandbox** (`billing`, rails@1). Services: app, deps, db, cache, mail.

| worktree | branch | PR | services | agent |
|---|---|---|---|---|
| main | main | — | up | reply |
| stripe-webhooks | stripe-webhooks | #88 | down | off |

**Phoenix demo** (`phx`, phoenix@1). Services: app, deps, db.

| worktree | branch | PR | services | agent |
|---|---|---|---|---|
| main | main | — | up | idle |

Switcher behaviour:

- A menu row shows `<n> worktrees · <up> running`, ◆ need-count, ● working-count, a stack chip, and keys `1`–`3`. The header shows `◆ <total> need input`.
- Search placeholder: "Go to a project, or a worktree in any project…". Worktree hits show the path `/<prefix>/<slug>`.
- The "Other projects need you" strip appears above the list.
- Footer actions: "New project…" and "Adopt a workspace…".

Worktree pages:

- URL: `wt.localhost:7700/<prefix>/<slug>`.
- Host: `<slug>.<prefix>.localhost`.
- Unit names: `<prefix>-<svc>@<slug>`.
- deps is a oneshot shown as `✓ done` / `○ not run`; other services show `● running` / `○ stopped`. A partly-up worktree has its first 3 services on.

Links per project (`:288-292`):

| project | links |
|---|---|
| filial | App, Letter opener `/letter_opener`, Storage console `s3-ui.`, Browser (VNC) `vnc.`, Admin `/admin`, Logs `logs.localhost` |
| billing | App, Mailpit `mail.`, Sidekiq `/sidekiq`, Logs |
| phx | App, LiveDashboard `/dev/dashboard`, Mailbox `/dev/mailbox`, Logs |

Header buttons: `New project`, `tasks :`, `logs`, `This machine`, and `⚙` (Colours).

## 11. Inconsistencies between boards

| # | topic | values |
|---|---|---|
| 1 | Install command format | Main: `bundle install · npm install`. Review: `bundle install && npm install`. Phoenix: Main `mix deps.get · npm install --prefix assets` vs Review `mix deps.get`. |
| 2 | Django/Flask server command | Main and Images `cmd`: `manage.py runserver 0.0.0.0:8000` / `flask run --host 0.0.0.0`. Review, Install `pack.toml` and Images APPTEXT `CMD`: prefixed with `uv run`. Templates: `runserver :8000`. |
| 3 | Version-file lists | Main rails `.ruby-version, .nvmrc`, but Images chains add `.tool-versions`, `Gemfile`, `Gemfile.lock` and `.node-version`. Main node `.nvmrc, .node-version`, Images also `.tool-versions:nodejs`. Phoenix: Main `.tool-versions (elixir, erlang)`, Images also `.elixir-version`. Adopt mentions `Gemfile.lock` as a version source. |
| 4 | Solid Queue id | `solid` (Main, Settings) vs `solid-queue` (Review, Templates). |
| 5 | Worker slot name | `worker` (Main, Settings, Install `option.toml` `slot = "worker"`) vs `jobs` (Review `[options]`, Templates refs "Jobs"). |
| 6 | GoodJob command | `bundle exec good_job start` (Main) vs `app image: good_job start` (Templates). GoodJob `needs` is `postgres` in Main. |
| 7 | Valkey image | Main `valkey/valkey:{valkey}` (no `-alpine`) vs Review, Images and Templates `docker.io/valkey/valkey:{valkey}-alpine`. |
| 8 | Image ref form | Main uses short names (`postgres:{postgres}`, `minio/minio:{minio}`, `axllent/mailpit:{mailpit}`). Others use full refs (`docker.io/library/…`, `quay.io/minio/minio`, `docker.io/axllent/mailpit`). Mailpit has no pin anywhere. |
| 9 | MariaDB major volume | Images `newVol: 'db-data-mariadb12'`, but the next pin is `11.8` (same major 11). The Review `vol` template `db-data-mariadb{mariadb.major}` would give `db-data-mariadb11` for both. |
| 10 | Trial worktree name | Review `[worktrees.postgres-trial.versions]` vs Settings scope `pg17-trial`. |
| 11 | Hostname form | Environment uses `my-feature.localhost` (no prefix) for WORKTREE_HOST. Main, Machine and Dashboard use `<slug>.<prefix>.localhost`. Main DETAIL texts say `mail.<slug>.localhost` / `vnc.<slug>.localhost`. Settings says `mail.<slug>.filial.localhost`. |
| 12 | Worktree ID in Environment | `RUBY_DEBUG_PORT 33007` and `DB_PORT 55439` imply ID 7, but Machine assigns ID 7 to Billing sandbox, not a Filial (rails) worktree. |
| 13 | DATABASE_URL shape | Main `<scheme>…@db/[db]`. Environment `<scheme><user>:${DB_PASSWORD}@db/[db]`. Images PROVIDES `DATABASE_URL={scheme}…@db/{db}` (`{db}` vs `[db]`). |
| 14 | Phoenix sqlite path | Main `path to dev.db (ecto_sqlite3)` vs Environment `dev.db`. |
| 15 | App unit/service name | Main units `filial-rails@<slug>`, Adopt `filial-rails@.container`, Review `[services.rails]`. The Dashboard lists the service as `app` → unit `filial-app@<slug>`. |
| 16 | Filial worktree count | Machine: 6 (IDs 1–6). Dashboard: 5 worktrees. Settings: "3 affected running worktrees", while the Dashboard has 4 non-down (3 up, 1 partly up). |
| 17 | Billing sandbox and template | Templates says Billing sandbox uses `rails-postgres-redis-rustfs` from v2. The Dashboard's billing services are app, deps, db, cache, **mail** (no storage) and its links include Sidekiq. |
| 18 | Storage routes | Main routes list only `s3.H → storage:9000`. Images PROVIDES and Settings and Dashboard links add `s3-ui.` (9001 console). |
| 19 | Storage health | Review gives no health for rustfs/minio. Images PROVIDES storage health is `curl -f localhost:9000/health`. |
| 20 | Template name vs refs | Review's `tplName` omits the browser (`rails-postgres-redis-rustfs`), but its refs and the Templates refs include `playwright@1`. |
| 21 | Django pack location | Templates: `where: 'stack-library'` (fetched, as in Install). Main presents Django alongside the built-in packs with no fetch or review step. |
| 22 | Secret names | The app gets `DB_PASSWORD` / `S3_SECRET_KEY` (Review `[secrets]`, Environment). Option-level secrets are `POSTGRES_PASSWORD` / `RUSTFS_SECRET_KEY` (Review `OPT.secret`, Environment db/storage). The relationship between them is not shown. |
| 23 | Install images check | "2 pulled: python:3.12-slim and ghcr.io/astral-sh/uv", but the `stack.lock` preview has only `[image.postgres]`. |
| 24 | Port 7700 | The dashboard is at `http://wt.localhost:7700`, and the example `search` service also uses `7700` (container-internal, so not a real clash but ambiguous). |
| 25 | Dark palettes | Referenced (`Dashboard.dc.html:130`), but no values exist. The TOML only shows `[colors.light]`. |