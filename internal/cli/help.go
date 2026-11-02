package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

// synopses are each flag set's command line after "railswitch ", its
// arguments included: --help prints it above the flags. Every name passed
// to e.flags has one (TestEveryFlagSetHasSynopsis).
var synopses = map[string]string{
	"agent":                  "agent [--new-term] [--no-token | --watch] [<name>] [-- <args>...]",
	"agent add":              "agent add <name> [--use <ref>] [--run on-demand|service]",
	"agent default apply":    "agent default apply [<name>] [--all]",
	"agent default promote":  "agent default promote [<name>] [--all]",
	"agent stop":             "agent stop [<name>]",
	"agent screen":           "agent screen [<name>]",
	"agent send":             "agent send [<name>]  (the text on stdin)",
	"issue ls":               "issue ls",
	"notes ls":               "notes ls",
	"notes show":             "notes show <name>",
	"notes write":            "notes write <name> --if-hash <hash>  (the text on stdin)",
	"notes rm":               "notes rm <name> --if-hash <hash>",
	"whiteboard show":        "whiteboard show  (the worktree's office whiteboard)",
	"whiteboard write":       "whiteboard write --if-hash <hash>  ({\"scene\", \"png\"} on stdin)",
	"whiteboard rm":          "whiteboard rm --if-hash <hash>",
	"agent update":           "agent update [<name>]",
	"allow-ports":            "allow-ports",
	"build":                  "build [--force] [--pull]",
	"completion":             "completion bash|zsh|fish",
	"containerfile eject":    "containerfile eject <name> [--force]",
	"containerfile list":     "containerfile list",
	"containerfile new":      "containerfile new <name>",
	"containerfile show":     "containerfile show <name> [--default]",
	"containerfile write":    "containerfile write <name> --if-hash <hash> [--dry-run] [--restart]  (the file on stdin)",
	"dashboard":              "dashboard",
	"dashboard colors":       "dashboard colors [set|reset ...]",
	"dashboard colors reset": "dashboard colors reset [--dark]",
	"dashboard colors set":   "dashboard colors set [--dark] [--base <preset>] [<token>=#rrggbb...]",
	"dashboard serve":        "dashboard serve",
	"db restore":             "db restore [--from <dir>] [--yes] [<database>|<service>...]",
	"deps":                   "deps -- <command> [<args>...]",
	"doctor":                 "doctor",
	"down":                   "down [--yes]",
	"env":                    "env [<service>]",
	"exec":                   "exec [--fresh] -- <command> [<args>...]",
	"fw allow":               "fw allow <service> <host|ip>...",
	"fw show":                "fw show [<service>]",
	"gc":                     "gc [--dry-run] [--volumes] [--yes]",
	"hooks backup":           "hooks backup [<hook>...]",
	"hooks check":            "hooks check",
	"hooks restore":          "hooks restore [--force]",
	"library add":            "library add <url> --alias <alias> [--branch <branch>]",
	"library fetch":          "library fetch [<alias>]",
	"library index":          "library index <dir> --library <id> [--serial <n>]",
	"library ls":             "library ls",
	"library rm":             "library rm <alias>",
	"library rotate":         "library rotate <dir> --old-key <private-key-file> --new <public-key-file>",
	"library sign":           "library sign <dir> --key <private-key-file>",
	"library trust":          "library trust <alias|dir> --fingerprint <fp> [--rotation | --new-signer]",
	"library verify":         "library verify <dir>",
	"lock":                   "lock [--review | --accept <hash> [--restart]]",
	"logs":                   "logs [-f] [<service>]",
	"machine":                "machine",
	"nvim":                   "nvim [--new-term] [<file>[:<line>[:<col>]]]",
	"nvim reset-config":      "nvim reset-config [--yes]",
	"overview":               "overview",
	"pack eject":             "pack eject <ref> [--as <name>]",
	"pack export":            "pack export <ref> --to <dir>",
	"pack file rm":           "pack file rm <ref> <path> --if-hash <hash>",
	"pack file show":         "pack file show <ref> <path>",
	"pack file write":        "pack file write <ref> <path> --if-hash <hash> [--dry-run] [--restart]  (the file on stdin)",
	"pack import":            "pack import <dir> [--as <name>] [--accept <sha256>] [--restart]",
	"pack ls":                "pack ls",
	"pack new":               "pack new [--option <slot>] <name>",
	"pack show":              "pack show <ref>",
	"pr ls":                  "pr ls",
	"pr show":                "pr show [<number>]  (default: the worktree's PR)",
	"pr merge":               "pr merge [<number>] [--method merge|squash|rebase] [--auto]",
	"pr ready":               "pr ready [<number>]",
	"pr rerun":               "pr rerun [<number>]  (the failed jobs of its Actions runs)",
	"project detect":         "project detect <repo> [--branch <branch>] [--pack <ref>]",
	"project init":           "project init  (in a clone of the workspace repo)",
	"project ls":             "project ls",
	"project new":            "project new <dir> --repo <url> --prefix <prefix> [flags]",
	"project rm":             "project rm <prefix> [--yes] [--purge]",
	"project show":           "project show",
	"project write":          "project write --if-hash <hash>  (the whole project.toml on stdin)",
	"render":                 "render [--check]",
	"restart":                "restart [<service>]",
	"secret add":             "secret add <name> --service <id>... [--env <VAR>] [--machine] [--git-signing]",
	"secret ls":              "secret ls",
	"secret rm":              "secret rm <name>",
	"secret set":             "secret set <name> [--restart]  (the value on stdin, or asked for)",
	"settings apply":         "settings apply --if-hash <hash> [--accept-install <review-hash>] [--restart]  (the edit list on stdin)",
	"settings preview":       "settings preview  (the edit list on stdin)",
	"share":                  "share [<worktree>]",
	"signer add":             "signer add <library> <public-key-file|->",
	"signer ls":              "signer ls",
	"signer revoke":          "signer revoke <library> <fingerprint>",
	"signer rm":              "signer rm <library> <fingerprint>",
	"stack ls":               "stack ls",
	"stack switch":           "stack switch <branch>",
	"status":                 "status",
	"stop":                   "stop",
	"task":                   "task [--new-term] <name> [<args>...] | task ls",
	"template check":         "template check [--accept <sha256>]",
	"template export":        "template export [--name <name>] [--version <N>] [--description <text>] [--to <dir>]",
	"template import":        "template import <dir> [--accept <sha256>]",
	"template ls":            "template ls",
	"template show":          "template show <ref>",
	"term":                   "term",
	"unshare":                "unshare [<worktree>]",
	"up":                     "up",
	"wt add":                 "wt add [--name <name>] [--up] <branch|PR number|new branch>",
	"wt ls":                  "wt ls",
	"wt rm":                  "wt rm [--force] <path|branch|name>",
}

// printHelp is a flag set's --help: its command line, then its flags with
// the "--" the docs use (one dash still parses).
func printHelp(w io.Writer, name string, fs *flag.FlagSet) {
	fmt.Fprintf(w, "usage: railswitch %s\n", synopses[name])
	printFlags(w, "flags", fs)
}

// printFlags lists fs's flags under heading, long names with "--".
func printFlags(w io.Writer, heading string, fs *flag.FlagSet) {
	first := true
	fs.VisitAll(func(f *flag.Flag) {
		if first {
			fmt.Fprintf(w, "\n%s:\n", heading)
			first = false
		}
		arg, usage := flag.UnquoteUsage(f)
		dash := "--"
		if len(f.Name) == 1 {
			dash = "-"
		}
		line := "  " + dash + f.Name
		if arg != "" {
			line += " " + arg
		}
		if def := f.DefValue; def != "" && def != "false" && def != "0" && def != "[]" {
			usage += fmt.Sprintf(" (default %q)", def)
		}
		fmt.Fprintf(w, "%s\n      %s\n", line, strings.ReplaceAll(usage, "\n", "\n      "))
	})
}
