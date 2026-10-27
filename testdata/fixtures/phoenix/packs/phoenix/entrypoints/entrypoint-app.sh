#!/bin/sh
# Before the server: create and migrate the dev database (both no-ops when
# done), then run the server command.
set -eu
cd /app

if mix help ecto.create >/dev/null 2>&1; then
  mix ecto.create
  mix ecto.migrate
fi

exec "$@"
