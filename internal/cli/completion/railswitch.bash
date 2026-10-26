# bash completion for railswitch. Install:
#   railswitch completion bash > ~/.local/share/bash-completion/completions/railswitch
# railswitch __complete prints the candidates for the last word.
_railswitch() {
    local IFS=$'\n'
    COMPREPLY=($(command railswitch __complete "${COMP_WORDS[@]:1:COMP_CWORD-1}" "${COMP_WORDS[COMP_CWORD]}" 2>/dev/null))
}
complete -F _railswitch railswitch
