#!/bin/sh
# Primes as an agent does at start (D136): the shared memory's context goes
# to ~/.railswitch-prime.md, where a real agent would read it into its own.
if [ -x /usr/local/bin/railswitch-prime ]; then
  /usr/local/bin/railswitch-prime > "$HOME/.railswitch-prime.md" 2>"$HOME/.railswitch-prime.err" ||
    echo "shell agent: railswitch-prime failed (see ~/.railswitch-prime.err)" >&2
fi
[ $# -eq 0 ] && exec sh
exec "$@"
