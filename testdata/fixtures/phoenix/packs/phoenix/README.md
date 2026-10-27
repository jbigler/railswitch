# phoenix

Runs a Phoenix app (`mix phx.server` on :4000) on Elixir and Erlang/OTP from
the app's `.tool-versions` (or `mise.toml`), with Postgres from the
`postgres@1` option. Live reload works through the app's own route.

## A new app, without Elixir on the host

```sh
mkdir -p ~/tmp/rs-phx
podman run --rm -it --init --name rs-phx-new -v "$HOME/tmp/rs-phx":/w:Z -w /w docker.io/library/elixir:1.19-otp-28 \
  sh -c 'mix local.hex --force && mix archive.install --force hex phx_new && mix phx.new hello --inside-docker-env --no-install && cd hello && printf "elixir 1.19.6\nerlang 28\n" > .tool-versions && git init -q && git add -A && git -c user.email=t@t -c user.name=t commit -qm init'
```

Then copy this directory to `<workspace>/packs/phoenix/` and run
`railswitch project new <workspace> --repo ~/tmp/rs-phx/hello --prefix hello --pack local:phoenix@1`.

## App-side changes

- **Listen on every interface.** `mix phx.new --inside-docker-env` already
  does. Otherwise, in `config/dev.exs`, change the endpoint's
  `http: [ip: {127, 0, 0, 1}]` to `http: [ip: {0, 0, 0, 0}]`.
- **Database settings from the env.** In `config/dev.exs`, remove
  `username`, `password` and `hostname` from the Repo config (Postgrex then
  reads `PGUSER`, `PGPASSWORD`, `PGHOST` and `PGPORT`, which the postgres
  option gives) and set
  `database: System.get_env("PGDATABASE", "hello_dev")`, so the name comes
  from the pack (`{project.prefix}_dev`); each worktree has its own database
  container.
- **Versions.** `.tool-versions` names `elixir <version>` and
  `erlang <version>`: the image is
  `docker.io/library/elixir:<elixir>-otp-<erlang major>-slim`, so the Elixir
  version must be one that image has a tag for, written without asdf's
  `-otp-NN` suffix (`elixir 1.19.6`, not `elixir 1.19.6-otp-28`).
- **mix.lock.** The first `deps` run writes it; commit it. Later runs use
  `mix deps.get --check-locked`, which refuses a `mix.lock` that needs
  changing: change dependencies with `railswitch deps -- mix deps.get`.
