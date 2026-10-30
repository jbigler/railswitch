#!/bin/sh
# memory.sh <package CLAUDE.md> <~/.claude>: refreshes railswitch's
# instructions as <~/.claude>/railswitch.md and keeps one line
# "@railswitch.md" in <~/.claude>/CLAUDE.md, which stays the user's: other
# tools' lines there (rtk init's @RTK.md) survive every start.
#
# A home from before this (no railswitch.md yet) holds a CLAUDE.md the old
# entrypoint copied over on every start: only its @-import lines (one a
# setup command just added) are kept.
set -eu

src=$1
dir=$2
mkdir -p "$dir"
memory="$dir/CLAUDE.md"
tmp="$memory.tmp.$$"

if [ ! -e "$dir/railswitch.md" ] && [ -f "$memory" ]; then
  grep '^@' "$memory" > "$tmp" || true
  mv "$tmp" "$memory"
fi

# A plain copy, never a bind mount: tools rewrite these files via temp + rename.
cp "$src" "$dir/railswitch.md"

if [ ! -f "$memory" ]; then
  echo '@railswitch.md' > "$memory"
elif ! grep -qx '@railswitch.md' "$memory"; then
  { echo '@railswitch.md'; cat "$memory"; } > "$tmp"
  mv "$tmp" "$memory"
fi
