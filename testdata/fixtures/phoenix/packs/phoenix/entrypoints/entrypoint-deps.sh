#!/bin/sh
# deps one-shot: fetch and compile what mix.lock says, then exit. Every app
# start runs it again. A given command (railswitch deps -- <command>) runs
# instead.
set -eu
cd /app

if [ "$#" -gt 0 ]; then
  exec "$@"
fi

if [ -s mix.lock ]; then
  mix deps.get --check-locked   # refuses a mix.lock that needs changing
else
  mix deps.get                  # a fresh app: writes mix.lock; commit it
fi
mix deps.compile

# esbuild and tailwind download their binaries; do it here, in the one
# container whose firewall allows package hosts.
if mix help assets.setup >/dev/null 2>&1; then
  # tailwind's binary is ~110 MB. tailwind (hex) 0.5+ names it for its
  # version (_build/tailwind-<target>-<version>): keep one copy of each
  # version for every worktree. Older releases name it tailwind-<target>
  # with no version; caching that would hand out a stale binary after an
  # update, so only version-named files are kept.
  cache=/home/appuser/.cache/phoenix-assets
  mkdir -p _build
  for f in "$cache"/tailwind-*-[0-9]*; do
    if [ -f "$f" ] && [ ! -e "_build/${f##*/}" ]; then cp "$f" _build/; fi
  done
  # Erlang's HTTP client sets no timeout: a stalled download would hang until
  # systemd kills deps. Bound each try and retry twice.
  tries=0
  until timeout 300 mix assets.setup; do
    tries=$((tries + 1))
    if [ "$tries" -ge 3 ]; then
      echo "deps: mix assets.setup failed or stalled 3 times" >&2
      exit 1
    fi
    echo "deps: mix assets.setup failed or stalled; retrying ($tries/2)" >&2
  done
  for f in _build/tailwind-*-[0-9]*; do
    if [ -f "$f" ] && [ ! -e "$cache/${f##*/}" ]; then cp "$f" "$cache/"; fi
  done
fi
