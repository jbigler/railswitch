#compdef railswitch
# zsh completion for railswitch. Install into a directory on $fpath:
#   railswitch completion zsh > ~/.zfunc/_railswitch   # with fpath=(~/.zfunc $fpath) before compinit
# railswitch __complete prints the candidates for the last word.
_railswitch() {
  local -a candidates
  candidates=("${(@f)$(command railswitch __complete "${(@Q)words[2,CURRENT-1]}" "${(Q)words[CURRENT]}" 2>/dev/null)}")
  candidates=(${candidates:#})
  compadd -a candidates
}

if [[ "${funcstack[1]}" == _railswitch ]]; then
  _railswitch "$@"
else
  compdef _railswitch railswitch
fi
