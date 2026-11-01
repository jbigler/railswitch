#!/bin/sh
# railswitch-statusline: Claude Code's status line, set by the entrypoint
# in a home whose settings.json has none (a status line of your own wins).
# Prints the worktree (and the folder under it), its git branch, the model
# and how full the context window is. Input: the session's JSON on stdin
# (code.claude.com/docs/en/statusline).
set -u

IFS='	' read -r dir model pct <<EOF2
$(jq -r '[.workspace.current_dir // .cwd // "/app", .model.display_name // "", (.context_window.used_percentage // "" | tostring)] | @tsv')
EOF2

where=${WORKTREE_SLUG:-$(basename "$dir")}
case "$dir" in /app/*) where="$where/${dir#/app/}" ;; esac

branch=$(git -C "$dir" --no-optional-locks branch --show-current 2>/dev/null)
[ -n "$branch" ] || branch=$(git -C "$dir" --no-optional-locks rev-parse --short HEAD 2>/dev/null)

line="$where"
[ -n "$branch" ] && line="$line  $branch"
[ -n "$model" ] && line="$line  $model"
if [ -n "$pct" ]; then
  pct=${pct%%.*}
  if [ "$pct" -ge 80 ]; then colour=31; elif [ "$pct" -ge 50 ]; then colour=33; else colour=32; fi
  line="$line  $(printf '\033[%sm' "$colour")ctx ${pct}%$(printf '\033[0m')"
else
  line="$line  ctx -"
fi
printf '%s\n' "$line"
