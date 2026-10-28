package cli

import (
	"context"
	"embed"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/workspace"
)

//go:embed completion/railswitch.bash completion/_railswitch.zsh completion/railswitch.fish
var completionScripts embed.FS

// completionFiles is each shell's completion script.
var completionFiles = map[string]string{
	"bash": "completion/railswitch.bash",
	"zsh":  "completion/_railswitch.zsh",
	"fish": "completion/railswitch.fish",
}

// completionSubs is each command's subcommands. A new command with
// subcommands adds its entry here; a subcommand with its own entry
// ("dashboard colors") nests. A command with completionArgs too (agent)
// offers both for its first word.
var completionSubs = map[string][]string{
	"agent":            {"add", "ls", "stop"},
	"containerfile":    {"eject", "list", "new", "show", "write"},
	"dashboard":        {"colors", "serve"},
	"dashboard colors": {"reset", "set"},
	"db":               {"dump"},
	"fw":               {"allow", "show"},
	"hooks":            {"backup", "check", "restore"},
	"library":          {"index", "rotate", "sign", "trust", "verify"},
	"nvim":             {"reset-config", "update"},
	"pack":             {"eject", "file", "ls", "new", "show"},
	"pack file":        {"rm", "show", "write"},
	"pr":               {"ls"},
	"project":          {"detect", "init", "ls", "new", "rm", "show", "write"},
	"settings":         {"apply", "preview"},
	"secret":           {"add", "ls", "rm", "set"},
	"signer":           {"add", "ls", "revoke", "rm"},
	"task":             {"ls"},
	"wt":               {"add", "ls", "rm"},
}

// completionFlags is each command path's flags ("": the global ones) as
// typed: -x for a one-letter name, --name otherwise; a trailing = marks a
// flag that takes a value. Commands are never run to collect them;
// TestCompletionFlagsMatch fails when the code defines a flag missing here.
var completionFlags = map[string][]string{
	"":                       {"-C=", "-w=", "--json"},
	"agent":                  {"--new-term"},
	"agent add":              {"--run=", "--use="},
	"build":                  {"--force", "--pull"},
	"containerfile eject":    {"--force"},
	"containerfile show":     {"--default"},
	"containerfile write":    {"--dry-run", "--if-hash=", "--restart"},
	"dashboard colors reset": {"--dark"},
	"dashboard colors set":   {"--base=", "--dark"},
	"down":                   {"--yes"},
	"exec":                   {"--fresh"},
	"hooks restore":          {"--force"},
	"lock":                   {"--accept=", "--restart", "--review"},
	"library index":          {"--library=", "--serial="},
	"library rotate":         {"--new=", "--old-key="},
	"library sign":           {"--key="},
	"library trust":          {"--fingerprint=", "--new-signer", "--rotation"},
	"logs":                   {"-f"},
	"nvim reset-config":      {"--yes"},
	"pack eject":             {"--as="},
	"pack file rm":           {"--if-hash="},
	"pack file write":        {"--dry-run", "--if-hash=", "--restart"},
	"pack new":               {"--dir=", "--option="},
	"project detect":         {"--branch=", "--pack="},
	"project rm":             {"--purge", "--yes"},
	"project new":            {"--accept-install=", "--agent=", "--branch=", "--dry-run", "--name=", "--option=", "--pack=", "--prefix=", "--repo=", "--tool=", "--up"},
	"project write":          {"--if-hash="},
	"render":                 {"--check"},
	"secret add":             {"--env=", "--git-signing", "--machine", "--service="},
	"secret set":             {"--restart"},
	"settings apply":         {"--accept-install=", "--if-hash=", "--restart"},
	"task":                   {"--new-term"},
	"wt add":                 {"--name=", "--up"},
	"wt rm":                  {"--force"},
}

// completer returns the candidates for an argument; dir is -C. It never
// fails: nothing to offer (outside a workspace, say) is nil.
type completer func(ctx context.Context, dir string) []string

// completionArgs is what a command path's first argument completes to.
var completionArgs = map[string]completer{
	"completion":          func(context.Context, string) []string { return slices.Collect(maps.Keys(completionFiles)) },
	"wt rm":               slugCandidates,
	"share":               slugCandidates,
	"unshare":             slugCandidates,
	"logs":                serviceCandidates,
	"fw show":             serviceCandidates,
	"fw allow":            serviceCandidates,
	"secret set":          secretCandidates,
	"secret rm":           secretCandidates,
	"containerfile show":  func(_ context.Context, dir string) []string { return containerfileCandidates(dir, true) },
	"containerfile eject": func(_ context.Context, dir string) []string { return containerfileCandidates(dir, false) },
	"containerfile write": func(_ context.Context, dir string) []string { return containerfileCandidates(dir, true) },
	"project rm":          prefixCandidates,
	"pack eject":          packRefCandidates,
	"pack show":           packRefCandidates,
	"agent":               agentCandidates,
	"agent stop":          agentCandidates,
	"task":                taskCandidates,
}

// railswitch completion bash|zsh|fish: print the shell's completion script.
func cmdCompletion(_ context.Context, e *env, args []string) int {
	const usage = "usage: railswitch completion bash|zsh|fish"
	fs := e.flags("completion")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	file, ok := completionFiles[fs.Arg(0)]
	if fs.NArg() != 1 || !ok {
		return e.usageErr("%s", usage)
	}
	data, err := completionScripts.ReadFile(file)
	if err != nil {
		return e.fail(err)
	}
	e.stdout.Write(data)
	return 0
}

// printCompletions is the hidden `railswitch __complete <words…>`: it
// prints the candidates for the last word, one per line. It never writes to
// stderr and always exits 0.
func printCompletions(ctx context.Context, w io.Writer, words []string) int {
	for _, c := range completions(ctx, words) {
		fmt.Fprintln(w, c)
	}
	return 0
}

// completions returns the sorted candidates starting with the last of
// words, given the words before it (without the program name).
func completions(ctx context.Context, words []string) []string {
	cur := ""
	if len(words) > 0 {
		cur, words = words[len(words)-1], words[:len(words)-1]
	}
	var (
		dir       string // -C
		path      string // command path: "", "wt", "wt rm"…
		subDone   bool   // path needs no further subcommand
		nargs     int    // arguments after path
		pending   string // the flag whose value comes next
		flagsDone bool   // after --
	)
	// bash splits at = (COMP_WORDBREAKS): -w=f arrives as -w, =, f. A lone
	// = after a flag that takes a value separates it from the value.
	for _, w := range words {
		if pending != "" {
			if w == "=" {
				continue
			}
			if path == "" && pending == "C" {
				dir = w
			}
			pending = ""
			continue
		}
		if !flagsDone && strings.HasPrefix(w, "-") && w != "-" {
			if w == "--" {
				flagsDone = true
				continue
			}
			name, val, inline := strings.Cut(strings.TrimLeft(w, "-"), "=")
			switch {
			case inline && path == "" && name == "C":
				dir = val
			case !inline && takesValue(path, name):
				pending = name
			}
			continue
		}
		switch {
		case path == "":
			path, subDone = w, len(completionSubs[w]) == 0
		case !subDone:
			path = path + " " + w
			subDone = len(completionSubs[path]) == 0
		default:
			nargs++
		}
	}
	if pending != "" && cur == "=" {
		cur = ""
	}
	var cands []string
	switch {
	case pending != "":
		if path == "" && pending == "w" {
			cands = slugCandidates(ctx, dir)
		}
	case !flagsDone && strings.HasPrefix(cur, "-"):
		for _, f := range completionFlags[path] {
			cands = append(cands, strings.TrimSuffix(f, "="))
		}
	case path == "":
		cands = slices.Collect(maps.Keys(commands))
	case !subDone:
		cands = slices.Clone(completionSubs[path])
		if c := completionArgs[path]; c != nil {
			cands = append(cands, c(ctx, dir)...)
		}
	case nargs == 0 && completionArgs[path] != nil:
		cands = completionArgs[path](ctx, dir)
	}
	var out []string
	for _, c := range cands {
		if strings.HasPrefix(c, cur) {
			out = append(out, c)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// takesValue reports whether path's flag name takes a value.
func takesValue(path, name string) bool {
	return slices.ContainsFunc(completionFlags[path], func(f string) bool {
		return strings.TrimLeft(f, "-") == name+"="
	})
}

// completionProject is the project.toml of the workspace dir is in.
func completionProject(dir string) (root string, p *config.Project, ok bool) {
	root, err := workspace.Root(dir)
	if err != nil {
		return "", nil, false
	}
	p, err = config.Load(filepath.Join(root, "project.toml"))
	if err != nil {
		return "", nil, false
	}
	return root, p, true
}

// completionRegistry reads the registry without locking or creating it.
func completionRegistry() (*machine.Registry, bool) {
	dirs, err := machine.DefaultDirs()
	if err != nil {
		return nil, false
	}
	reg, err := machine.Read(dirs.Data)
	return reg, err == nil
}

// slugCandidates is the current workspace's registered worktree slugs.
func slugCandidates(_ context.Context, dir string) []string {
	_, p, ok := completionProject(dir)
	if !ok {
		return nil
	}
	reg, ok := completionRegistry()
	if !ok {
		return nil
	}
	var out []string
	for _, wt := range reg.WorktreesOf(p.Project.Prefix) {
		out = append(out, wt.Slug)
	}
	return out
}

// serviceCandidates is the current workspace model's service ids.
func serviceCandidates(ctx context.Context, dir string) []string {
	w, err := workspace.Load(ctx, dir, "")
	if err != nil {
		return nil
	}
	reg, ok := completionRegistry()
	if !ok {
		return nil
	}
	m, err := w.Model(reg, render.Host{})
	if err != nil {
		return nil
	}
	var out []string
	for _, s := range m.Services {
		out = append(out, s.ID)
	}
	return out
}

// taskCandidates is the tasks task ls lists: the pack's with project.toml's
// [tasks] over them.
func taskCandidates(ctx context.Context, dir string) []string {
	w, err := workspace.Load(ctx, dir, "")
	if err != nil {
		return nil
	}
	reg, ok := completionRegistry()
	if !ok {
		return nil
	}
	m, err := w.Model(reg, render.Host{})
	if err != nil {
		return nil
	}
	var out []string
	for _, t := range m.Tasks() {
		out = append(out, t.Name)
	}
	return out
}

// agentCandidates is the agents project.toml declares.
func agentCandidates(_ context.Context, dir string) []string {
	_, p, ok := completionProject(dir)
	if !ok {
		return nil
	}
	return slices.Collect(maps.Keys(p.Agents))
}

// secretCandidates is the secrets project.toml declares.
func secretCandidates(_ context.Context, dir string) []string {
	_, p, ok := completionProject(dir)
	if !ok {
		return nil
	}
	return slices.Collect(maps.Keys(p.Secrets))
}

// containerfileCandidates is the built-in Containerfile names the stack
// uses (what eject takes); withWorkspace adds the workspace's own
// containerfiles/Containerfile.<name> (show prints those too).
func containerfileCandidates(dir string, withWorkspace bool) []string {
	root, err := workspace.Root(dir)
	if err != nil {
		return nil
	}
	var out []string
	if _, _, st, err := projectStack(root); err == nil {
		out = slices.Collect(maps.Keys(stackBuiltins(st)))
	}
	if withWorkspace {
		ents, _ := os.ReadDir(filepath.Join(root, "containerfiles"))
		for _, ent := range ents {
			if name, ok := strings.CutPrefix(ent.Name(), "Containerfile."); ok && cfNameRe.MatchString(name) {
				out = append(out, name)
			}
		}
	}
	return out
}

// prefixCandidates is every registered project prefix.
func prefixCandidates(context.Context, string) []string {
	reg, ok := completionRegistry()
	if !ok {
		return nil
	}
	var out []string
	for _, pe := range reg.Projects {
		out = append(out, pe.Prefix)
	}
	return out
}
