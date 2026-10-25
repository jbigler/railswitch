#!/bin/sh
# nvim tool entrypoint: report the state of the shared config and plugin
# volumes, then run the server.
#
# No agent software and no project-specific language servers are installed
# here (D30, D42): the editor config installs what it needs (e.g. with
# Mason), into the shared volumes, so it happens once and not per start.
set -eu
[ -n "${ENTRYPOINT_DEBUG:-}" ] && set -x

mkdir -p ~/.config ~/.local

# ~/.config/nvim is the <prefix>_nvim_config volume, shared by every
# worktree's nvim: empty on first run, and whatever you configure persists.
nvim_entries=$(ls -A ~/.config/nvim 2>/dev/null | wc -l)
if [ "$nvim_entries" -eq 0 ]; then
	echo "nvim config: shared volume, empty. Configure nvim in here and it" >&2
	echo "             persists across every worktree." >&2
elif [ ! -f ~/.config/nvim/init.lua ] && [ ! -f ~/.config/nvim/init.vim ]; then
	echo "WARN: nvim config has no init.lua or init.vim." >&2
	echo "      Contents: $(ls -A ~/.config/nvim | tr '\n' ' ')" >&2
else
	echo "nvim config: shared volume, ${nvim_entries} entries" >&2
fi

# Plugins are a separate volume: ~/.local/share/nvim.
if [ -z "$(ls -A ~/.local/share/nvim 2>/dev/null)" ]; then
	echo "note: ~/.local/share/nvim is empty, so no plugins are installed yet." >&2
	echo "      Your plugin manager installs them when the server first loads the" >&2
	echo "      config; a headless server may need it driven explicitly, e.g." >&2
	echo "      nvim --headless '+Lazy! sync' +qa in this container." >&2
fi

exec "$@"
