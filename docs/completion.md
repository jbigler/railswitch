# Shell completion

`railswitch completion bash|zsh|fish` prints a completion script. Each script calls `railswitch __complete <words…>` (hidden), which prints one candidate per line for the last word; the scripts hold no command list of their own.

## Install

bash (needs the bash-completion package):

```sh
mkdir -p ~/.local/share/bash-completion/completions
railswitch completion bash > ~/.local/share/bash-completion/completions/railswitch
```

zsh: write `_railswitch` into a directory on `$fpath`, before `compinit` runs:

```sh
mkdir -p ~/.zfunc
railswitch completion zsh > ~/.zfunc/_railswitch
# in ~/.zshrc, before compinit:
fpath=(~/.zfunc $fpath)
```

fish:

```sh
railswitch completion fish > ~/.config/fish/completions/railswitch.fish
```

## What completes

- Commands, subcommands (`wt add|rm|ls`, `project new|init|rm|ls`, …) and each command's flags. Flags come from a table in `internal/cli/completion.go`; a test fails when a command defines a flag the table lacks.
- After `-w`, and for `wt rm`, `share` and `unshare`: the current workspace's registered worktree slugs.
- `logs` and `fw show|allow`: the workspace's service ids.
- `secret set|rm`: the secrets `project.toml` declares.
- `agent`: its subcommand `ls` together with the agents `project.toml` declares (`[agents.<name>]`).
- `containerfile show|eject`: the stack's built-in Containerfile names (`show` also the workspace's own `containerfiles/Containerfile.<name>`).
- `project rm`: every registered project prefix.
- `project new --agent` takes `name=ref` or `none`; the flag itself completes, its value does not.

Workspace candidates honour `-C <dir>` on the command line. Outside a workspace, or on any error, completion offers nothing and prints nothing to stderr. It reads the registry without locking or creating it, and never runs podman, git or any other command.

bash and fish pass words to `__complete` as typed: a quoted or `~`-prefixed `-C` path (`-C "my dir"`, `-C ~/app`) finds no workspace, so nothing is offered. zsh unquotes the words first.
