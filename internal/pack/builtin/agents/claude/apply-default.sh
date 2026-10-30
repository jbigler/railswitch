#!/bin/sh
# apply-default.sh <default dir> <home>: puts the project's default Claude
# config (railswitch agent default promote, D135) into home. Each item the
# default holds under .claude/ (and each of its .claude/plugins/ records)
# replaces the home's; .claude.json is merged into the home's instead, the
# default's keys winning and the home's projects (trust, per-project
# state) kept. What it replaces goes to <home>/.railswitch-default-backup.
set -eu

default=$1
home=$2
backup="$home/.railswitch-default-backup"
rm -rf "$backup"
mkdir -p "$backup/.claude/plugins"

replace() { # $1: a path relative to home
  if [ -e "$home/$1" ] || [ -L "$home/$1" ]; then
    mv "$home/$1" "$backup/$1"
  fi
  mkdir -p "$(dirname "$home/$1")"
  cp -R "$default/$1" "$home/$1"
}

if [ -d "$default/.claude" ]; then
  for p in "$default"/.claude/* "$default"/.claude/.[!.]*; do
    if [ ! -e "$p" ]; then
      continue
    fi
    name=${p##*/}
    if [ "$name" = plugins ]; then
      for f in "$p"/*; do
        if [ -e "$f" ]; then
          replace ".claude/plugins/${f##*/}"
        fi
      done
    else
      replace ".claude/$name"
    fi
  done
fi

if [ -f "$default/.claude.json" ]; then
  tmp="$home/.claude.json.tmp.$$"
  if [ -f "$home/.claude.json" ]; then
    cp "$home/.claude.json" "$backup/.claude.json"
    jq -s '.[0] * (.[1] | del(.projects))' "$home/.claude.json" "$default/.claude.json" > "$tmp"
  else
    jq 'del(.projects)' "$default/.claude.json" > "$tmp"
  fi
  mv "$tmp" "$home/.claude.json"
fi
