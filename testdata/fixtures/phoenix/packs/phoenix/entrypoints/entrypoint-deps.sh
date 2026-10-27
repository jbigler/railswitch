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
  mix assets.setup
fi
