#!/bin/sh
# App entrypoint: prepare the database, then run the app's command until it
# exits or the container is stopped.
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

# Run the command and pass a stop on to everything it started. As PID 1 a
# shell ignores SIGTERM, and a bin/dev that runs foreman as a child instead
# of exec-ing it would never see one: podman stop then kills everything
# after 10 s and the unit ends failed (exit 137). The container's processes
# share PID 1's process group (dash has no job control without a tty), so
# the stop goes to the group, kill 0, with this shell ignoring it.
"$@" &
pid=$!
stopping=
stop() {
  trap '' TERM INT
  stopping=1
  kill -"$1" 0 2>/dev/null || true
}
trap 'stop TERM' TERM
trap 'stop INT' INT
status=0
wait "$pid" || status=$?
# wait returns when a trap runs: wait again until the command has exited.
while kill -0 "$pid" 2>/dev/null; do
  status=0
  wait "$pid" || status=$?
done
if [ -z "$stopping" ]; then
  exit "$status"
fi
# A stop: as the container's PID 1, let the rest (foreman's servers, which
# outlive a bin/dev shell) finish, then end as a stopped unit, not a failed
# one.
others() {
  for p in /proc/[0-9]*; do
    [ "${p#/proc/}" = "$$" ] && continue
    state=$(sed 's/.*) //' "$p/stat" 2>/dev/null | cut -d' ' -f1) || continue
    [ -n "$state" ] && [ "$state" != Z ] && return 0
  done
  return 1
}
if [ "$$" = 1 ]; then
  while others; do
    sleep 0.1
  done
fi
exit 0
