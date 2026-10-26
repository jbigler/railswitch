# fish completion for railswitch. Install:
#   railswitch completion fish > ~/.config/fish/completions/railswitch.fish
# railswitch __complete prints the candidates for the last word.
function __railswitch_complete
    set -l words (commandline -opc)
    set -e words[1]
    set -l cur (commandline -ct)
    command railswitch __complete $words "$cur" 2>/dev/null
end

complete -c railswitch -f -a '(__railswitch_complete)'
