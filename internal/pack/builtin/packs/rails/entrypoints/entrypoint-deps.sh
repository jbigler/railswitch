#!/bin/sh
# deps one-shot: install exactly what this worktree's lockfiles say, then exit.
# The app unit requires it, and every app start runs it again, so a restart
# after a lockfile change installs the new packages.
#
# This is the only worktree container that fetches packages. Installs are
# lockfile-frozen (D35): BUNDLE_FROZEN=true comes from the service env, so
# bundler refuses to change Gemfile.lock, and the JavaScript install keeps
# its lockfile as written. To change a dependency, run
# `railswitch deps -- <command>`.
#
# Both checks below are offline, so an unchanged worktree starts the app
# without touching the network (yarn and pnpm come through corepack, which
# may fetch the version package.json names on first use).
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

# JavaScript: the lockfile picks the tool (D69), first match in this order.
# None of them has an offline "already satisfied" check like `bundle check`,
# so record what the last successful install was for. The node and tool
# versions are part of it: a bump means a new image, and native modules must
# be rebuilt for it.
if [ -f package.json ]; then
  if [ -f package-lock.json ]; then
    lock=package-lock.json tool=npm
    set -- npm ci --no-audit --no-fund
  elif [ -f bun.lock ] || [ -f bun.lockb ]; then
    lock=bun.lock tool=bun
    [ -f bun.lock ] || lock=bun.lockb
    set -- bun install --frozen-lockfile
  elif [ -f yarn.lock ]; then
    lock=yarn.lock tool=yarn
    set -- yarn install --immutable
  elif [ -f pnpm-lock.yaml ]; then
    lock=pnpm-lock.yaml tool=pnpm
    set -- pnpm install --frozen-lockfile
  else
    echo "deps: package.json has no lockfile: run railswitch deps -- npm install (or bun, yarn or pnpm install)" >&2
    exit 1
  fi
  if ! command -v "$tool" >/dev/null 2>&1; then
    case "$tool" in
    bun) fix='pin it in .bun-version or package.json "packageManager": "bun@<version>", then railswitch build (an older ejected containerfiles/Containerfile.base needs railswitch containerfile eject base --force)' ;;
    *) fix='set package.json "packageManager": "'"$tool"'@<version>" (an ejected containerfiles/Containerfile.base without corepack on Node 25 or later needs railswitch containerfile eject base --force)' ;;
    esac
    echo "deps: $lock needs $tool, which is not in the image: $fix" >&2
    exit 1
  fi
  version="$("$tool" --version)"
  # yarn 1 (no "packageManager", or "yarn@1…") ignores --immutable and would
  # rewrite yarn.lock; its frozen flag is --frozen-lockfile.
  case "$tool $version" in
  "yarn 1."*) set -- yarn install --frozen-lockfile ;;
  esac
  stamp=node_modules/.deps-stamp
  want="$({ node --version; echo "$version"; cat package.json "$lock"; } | sha256sum | cut -d' ' -f1)"
  if [ -f "$stamp" ] && [ "$(cat "$stamp")" = "$want" ]; then
    echo "deps: node_modules up to date"
  else
    "$@"
    mkdir -p node_modules
    printf '%s\n' "$want" > "$stamp"
  fi
fi
