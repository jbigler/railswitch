#!/bin/sh
# App entrypoint: prepare the database, then run the app's command.
# Gems and node packages are installed by the deps service before this
# container starts; nothing here fetches packages.
set -e

# Clear a stale server pid left by an unclean shutdown: /app is bind-mounted,
# so the pid file survives container restarts and blocks "rails server".
rm -f tmp/pids/server.pid

# Create, load or migrate the development database. An app without
# Active Record has no config/database.yml and nothing to prepare.
if [ -f config/database.yml ]; then
  bin/rails db:prepare
fi

exec "$@"
