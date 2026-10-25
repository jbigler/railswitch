#!/bin/sh
# deps one-shot: install exactly what this worktree's lockfiles say, then exit.
# The app unit requires it, and every app start runs it again, so a restart
# after a lockfile change installs the new packages.
#
# This is the only worktree container that fetches packages. Installs are
# lockfile-frozen (D35): BUNDLE_FROZEN=true comes from the service env, so
# bundler refuses to change Gemfile.lock, and npm ci installs package-lock.json
# as written. To change a dependency, run `railswitch deps -- <command>`.
#
# Both checks below are offline, so an unchanged worktree starts the app
# without touching the network.
#
# Given a command (a pack's [app] install, rendered as the unit's Exec=), it
# runs that instead, after the setup.
set -eu
cd /app

if [ "$#" -gt 0 ]; then
  exec "$@"
fi

if bundle check >/dev/null 2>&1; then
  echo "deps: gems up to date"
else
  bundle install
fi

# bin/dev installs foreman on its own when Procfile.dev exists and foreman is
# missing. Doing it here keeps that fetch out of the app container. Same check
# bin/dev makes, so bin/dev then skips it.
if [ -f Procfile.dev ]; then
  gem list foreman -i --silent || gem install foreman --no-document
fi

# npm has no offline "already satisfied" check like `bundle check`, so record
# what the last successful install was for. The node version is part of it:
# a node bump means a new image, and native modules must be rebuilt for it.
if [ -f package.json ]; then
  if [ ! -f package-lock.json ]; then
    echo "no package-lock.json: run railswitch deps -- npm install" >&2
    exit 1
  fi
  stamp=node_modules/.deps-stamp
  want="$({ node --version; cat package.json package-lock.json; } | sha256sum | cut -d' ' -f1)"
  if [ -f "$stamp" ] && [ "$(cat "$stamp")" = "$want" ]; then
    echo "deps: node_modules up to date"
  else
    npm ci --no-audit --no-fund
    printf '%s\n' "$want" > "$stamp"
  fi
fi
