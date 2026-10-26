// Package cli implements the railswitch command-line interface.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/jbigler/railswitch/internal/buildinfo"
	"github.com/jbigler/railswitch/internal/sys"
)

// env carries the I/O streams and global flags for one invocation.
type env struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	dir    string // -C: start dir for workspace discovery
	slug   string // -w: worktree slug
	json   bool   // --json: machine-readable output (D40)
	run    sys.Runner
}

// procRoot is the procfs root machine.Listening reads the host's listening
// TCP ports from; tests point it elsewhere.
var procRoot = "/proc"

type command func(ctx context.Context, e *env, args []string) int

var commands = map[string]command{
	"version":       cmdVersion,
	"new":           cmdNew,
	"init":          cmdInit,
	"lock":          cmdLock,
	"render":        cmdRender,
	"env":           cmdEnv,
	"containerfile": cmdContainerfile,
	"fw":            cmdFw,
	"secret":        cmdSecret,
	"wt":            cmdWt,
	"up":            cmdUp,
	"stop":          cmdStop,
	"down":          cmdDown,
	"restart":       cmdRestart,
	"status":        cmdStatus,
	"share":         cmdShare,
	"unshare":       cmdUnshare,
	"logs":          cmdLogs,
	"exec":          cmdExec,
	"build":         cmdBuild,
	"deps":          cmdDeps,
	"doctor":        cmdDoctor,
	"allow-ports":   cmdAllowPorts,
	"completion":    cmdCompletion,
}

// Main runs railswitch with args (without the program name) and returns the exit code.
func Main(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return dispatch(ctx, sys.Exec{}, args, stdin, stdout, stderr)
}

// dispatch is Main with the Runner every external command goes through.
func dispatch(ctx context.Context, r sys.Runner, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "__complete" { // hidden: shell completion
		return printCompletions(ctx, stdout, args[1:])
	}
	e := &env{stdin: stdin, stdout: stdout, stderr: stderr, run: r}
	fs := flag.NewFlagSet("railswitch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&e.dir, "C", "", "start `dir` for workspace discovery")
	fs.StringVar(&e.slug, "w", "", "worktree `slug`")
	fs.BoolVar(&e.json, "json", false, "print machine-readable JSON")
	fs.Usage = func() { usage(stderr, fs) }
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) == 0 {
		usage(stderr, fs)
		return 2
	}
	cmd, ok := commands[rest[0]]
	if !ok {
		fmt.Fprintf(stderr, "railswitch: unknown command %q\n", rest[0])
		return 2
	}
	return cmd(ctx, e, rest[1:])
}

func usage(w io.Writer, fs *flag.FlagSet) {
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Fprintf(w, "usage: railswitch [-C dir] [-w slug] [--json] <command> [args]\n\ncommands: %s\n\nglobal flags:\n", strings.Join(names, ", "))
	fs.PrintDefaults()
}

func cmdVersion(_ context.Context, e *env, args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(e.stderr, "railswitch: version takes no arguments")
		return 2
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(struct {
			Version string `json:"version"`
		}{buildinfo.Version}); err != nil {
			fmt.Fprintf(e.stderr, "railswitch: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(e.stdout, "railswitch %s\n", buildinfo.Version)
	return 0
}
