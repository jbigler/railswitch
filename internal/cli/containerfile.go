package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/jbigler/railswitch/internal/buildinfo"
	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/pack"
	builtinfs "github.com/jbigler/railswitch/internal/pack/builtin"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/settings"
	"github.com/jbigler/railswitch/internal/tomlfile"
	"github.com/jbigler/railswitch/internal/workspace"
)

// newNameRe is the name of a user addon (`containerfile new`).
var newNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

// cfNameRe is the <name> of any containerfiles/Containerfile.<name> (D31).
var cfNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

type defaultKind int

const (
	kindBase       defaultKind = iota // the pack's base image: [image] base
	kindAddon                         // an option's app addon: [image] addons
	kindTool                          // a tool's image, found by filename
	kindStandalone                    // an option's standalone build (D31), found by filename
	kindAgentBase                     // the agent base image, found by filename
	kindAgent                         // an agent package's image, found by filename
)

// agentBaseFile is the agent base image's built-in Containerfile in builtinfs.FS.
const agentBaseFile = "agentbase/Containerfile.agent-base"

// builtin is a built-in default Containerfile.
type builtin struct {
	ref   string // its package: "rails@1", "postgres@1", "local:probe@1", a tool's name or "agent-base"
	files fs.FS  // its package directory
	file  string // its name there, e.g. "Containerfile.pg-client"
	kind  defaultKind
	os    string // kindAddon: the OS family it is for
}

// cfName is the name in "Containerfile.<name>".
func cfName(file string) string { return strings.TrimPrefix(file, "Containerfile.") }

// workspaceFile is the workspace path of Containerfile.<name>.
func workspaceFile(name string) string { return "containerfiles/Containerfile." + name }

// stackBuiltins returns the defaults the stack st uses, by name: "base"
// (the pack's), each chosen option's addon and standalone build, each
// enabled tool's and, when it has agents, "agent-base" and each agent
// package's (built-in or workspace-local).
func stackBuiltins(st *pack.Stack) map[string]builtin {
	pk := st.Pack
	out := map[string]builtin{"base": {ref: st.PackRef.String(), files: pk.Files, file: pk.Image.File, kind: kindBase}}
	for _, ch := range st.Options {
		o := ch.Option
		ref := ch.Ref.String()
		if a := o.AppAddon; a != nil {
			out[cfName(a.File)] = builtin{ref: ref, files: o.Files, file: a.File, kind: kindAddon, os: a.OS}
		}
		if b := o.Image.Build; b != "" {
			out[cfName(b)] = builtin{ref: ref, files: o.Files, file: b, kind: kindStandalone}
		}
	}
	for _, t := range st.Tools {
		out[cfName(t.Image.File)] = builtin{ref: t.Name, files: t.Files, file: t.Image.File, kind: kindTool}
	}
	if len(st.Agents) > 0 {
		out["agent-base"] = builtin{ref: "agent-base", files: builtinfs.FS, file: agentBaseFile, kind: kindAgentBase}
	}
	for _, a := range st.Agents {
		out[cfName(a.Agent.Image.File)] = builtin{ref: a.Ref.String(), files: a.Agent.Files, file: a.Agent.Image.File, kind: kindAgent}
	}
	return out
}

// builtinNames is every name a built-in default has in any stack of cat,
// and the agent images' defaults: agent-base and each agent package's,
// built-in or workspace-local.
func builtinNames(cat *pack.Catalog) map[string]bool {
	names := map[string]bool{"base": true, "agent-base": true}
	for _, o := range cat.Options {
		if o.AppAddon != nil {
			names[cfName(o.AppAddon.File)] = true
		}
		if o.Image.Build != "" {
			names[cfName(o.Image.Build)] = true
		}
	}
	for _, t := range cat.Tools {
		names[cfName(t.Image.File)] = true
	}
	for _, a := range cat.Agents {
		names[cfName(a.Image.File)] = true
	}
	return names
}

// lookup returns the stack's default name, or an error naming the ones it has.
func lookup(cat *pack.Catalog, st *pack.Stack, name string) (builtin, error) {
	defs := stackBuiltins(st)
	if b, ok := defs[name]; ok {
		return b, nil
	}
	have := slices.Sorted(maps.Keys(defs))
	if builtinNames(cat)[name] {
		return builtin{}, fmt.Errorf("the built-in Containerfile.%s belongs to a package this project's stack does not use (it uses: %s)", name, strings.Join(have, ", "))
	}
	return builtin{}, fmt.Errorf("no built-in Containerfile.%s (this project's stack has: %s)", name, strings.Join(have, ", "))
}

// Eject writes the default Containerfile name of the stack st to
// <root>/containerfiles/Containerfile.<name>, under a header naming its
// package and sha256, and for base sets [image] base, for an option addon
// appends it to [image] addons (tools, standalone builds and the agent images
// are found by filename). An existing file is refused unless force. It needs no
// stack.lock: railswitch project new ejects before it locks.
func Eject(root string, cat *pack.Catalog, st *pack.Stack, name string, force bool) error {
	b, err := lookup(cat, st, name)
	if err != nil {
		return err
	}
	if b.kind == kindAddon && b.os != st.Pack.Image.OS {
		return fmt.Errorf("addon %s of %s is for os %q, but pack %s builds on os %q", name, b.ref, b.os, st.PackRef, st.Pack.Image.OS)
	}
	def, err := fs.ReadFile(b.files, b.file)
	if err != nil {
		return err
	}
	rel := workspaceFile(name)
	header := fmt.Sprintf("# Ejected from %s Containerfile.%s (sha256 %x) by railswitch %s. Edit freely: railswitch builds this file.\n", b.ref, name, sha256.Sum256(def), buildinfo.Version)
	created, err := writeContainerfile(root, rel, append([]byte(header), def...), force)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%w: pass --force to replace it with the default", err)
	}
	if err != nil {
		return err
	}
	proj := filepath.Join(root, "project.toml")
	switch b.kind {
	case kindBase:
		err = config.SetImageBase(proj, rel)
	case kindAddon:
		var p *config.Project
		if p, err = config.Load(proj); err == nil && !slices.Contains(p.Image.Addons, rel) {
			err = config.AppendImageAddon(proj, rel)
		}
	}
	return undoCreate(root, rel, created, err)
}

// writeContainerfile writes rel under root; an existing file is refused
// (fs.ErrExist) unless replace. created reports whether the file is new.
func writeContainerfile(root, rel string, data []byte, replace bool) (created bool, err error) {
	path := filepath.Join(root, filepath.FromSlash(rel))
	switch _, err := os.Lstat(path); {
	case err == nil:
		if !replace {
			return false, fmt.Errorf("%s: %w", rel, fs.ErrExist)
		}
	case errors.Is(err, fs.ErrNotExist):
		created = true
	default:
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return created, tomlfile.WriteAtomic(path, data, 0o644)
}

// undoCreate removes rel when it was just created and the project.toml
// edit that goes with it (err) failed, so a retry starts clean.
func undoCreate(root, rel string, created bool, err error) error {
	if err != nil && created {
		if rerr := os.Remove(filepath.Join(root, filepath.FromSlash(rel))); rerr != nil {
			return errors.Join(err, rerr)
		}
	}
	return err
}

// projectStack loads root's project.toml and resolves its stack. It needs
// no stack.lock and does not Validate: eject is how a project lacking
// [image] base gets one.
func projectStack(root string) (*config.Project, *pack.Catalog, *pack.Stack, error) {
	p, err := config.Load(filepath.Join(root, "project.toml"))
	if err != nil {
		return nil, nil, nil, err
	}
	cat, err := pack.LoadCatalog(root)
	if err != nil {
		return nil, nil, nil, err
	}
	st, err := cat.Resolve(p)
	if err != nil {
		return nil, nil, nil, err
	}
	return p, cat, st, nil
}

// railswitch containerfile list|show|write|eject|new (D32, D91).
func cmdContainerfile(ctx context.Context, e *env, args []string) int {
	const usage = "usage: railswitch containerfile list | show <name> [--default] | write <name> --if-hash <hash> [--dry-run] [--restart] | eject <name> [--force] | new <name>"
	if len(args) == 0 {
		return e.usageErr(usage)
	}
	switch args[0] {
	case "list":
		return containerfileList(ctx, e, args[1:])
	case "show":
		return containerfileShow(e, args[1:])
	case "write":
		return containerfileWrite(ctx, e, args[1:])
	case "eject":
		return containerfileEject(e, args[1:])
	case "new":
		return containerfileNew(e, args[1:])
	}
	return e.usageErr("containerfile: unknown subcommand %q\n%s", args[0], usage)
}

// parseNamed parses args holding one name, with fset's flags before or
// after it; ok false means exit with code.
func parseNamed(e *env, fset *flag.FlagSet, args []string, usage string) (name string, code int, ok bool) {
	a, code, ok := parseArgs(e, fset, args, 1, usage)
	if !ok {
		return "", code, false
	}
	return a[0], 0, true
}

// parseArgs parses args holding n positional arguments, with fset's flags
// before, between or after them; ok false means exit with code.
func parseArgs(e *env, fset *flag.FlagSet, args []string, n int, usage string) (pos []string, code int, ok bool) {
	for {
		if code, ok := parse(fset, args); !ok {
			return nil, code, false
		}
		if fset.NArg() == 0 {
			break
		}
		pos = append(pos, fset.Arg(0))
		args = fset.Args()[1:]
	}
	if len(pos) != n {
		return nil, e.usageErr("%s", usage), false
	}
	return pos, 0, true
}

// buildStep is one element of `containerfile list --json` steps.
type buildStep struct {
	Name string   `json:"name"`
	File string   `json:"file"` // workspace-relative
	Tag  string   `json:"tag"`
	From []string `json:"from"`
}

// railswitch containerfile list: the current worktree's image builds, in
// build order, then the model's warnings.
func containerfileList(ctx context.Context, e *env, args []string) int {
	fset := e.flags("containerfile list")
	if code, ok := parse(fset, args); !ok {
		return code
	}
	if fset.NArg() != 0 {
		return e.usageErr("containerfile list takes no arguments")
	}
	_, m, in, code := e.instance(ctx, "containerfile list")
	if code != 0 {
		return code
	}
	doc := struct {
		Steps    []buildStep `json:"steps"`
		Warnings []string    `json:"warnings"`
	}{Steps: []buildStep{}, Warnings: orEmpty(m.Warnings)}
	for _, b := range in.Builds {
		rel, err := filepath.Rel(m.Inputs.Workspace, b.File)
		if err != nil {
			return e.fail(err)
		}
		doc.Steps = append(doc.Steps, buildStep{Name: b.Name, File: filepath.ToSlash(rel), Tag: b.Tag, From: orEmpty(b.From)})
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(doc); err != nil {
			return e.fail(err)
		}
		return 0
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 0, 2, ' ', 0)
	for _, s := range doc.Steps {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", s.Name, s.File, s.Tag)
	}
	if err := tw.Flush(); err != nil {
		return e.fail(err)
	}
	for _, w := range doc.Warnings {
		fmt.Fprintf(e.stdout, "warning: %s\n", w)
	}
	return 0
}

// orEmpty is s, or an empty slice for nil: a JSON array, never null.
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// kindNames are the kinds of `containerfile show --json`; a file without a
// default is "user".
var kindNames = map[defaultKind]string{
	kindBase:       "base",
	kindAddon:      "addon",
	kindTool:       "tool",
	kindStandalone: "standalone",
	kindAgentBase:  "agent-base",
	kindAgent:      "agent",
}

// ejectHeaderRe is the first line Eject writes.
var ejectHeaderRe = regexp.MustCompile(`^# Ejected from (\S+) Containerfile\.[a-z][a-z0-9-]* \(sha256 ([0-9a-f]{64})\)`)

// parseEjectHeader reads Eject's header line: the default's package and
// the sha256 of the default it copied.
func parseEjectHeader(line string) (ref, sha string, ok bool) {
	m := ejectHeaderRe.FindStringSubmatch(line)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// cfShowDoc is `containerfile show --json` (docs/json.md).
type cfShowDoc struct {
	Name           string     `json:"name"`
	File           string     `json:"file"`
	Kind           string     `json:"kind"`
	Text           string     `json:"text"`
	Hash           string     `json:"hash"`
	Default        *cfDefault `json:"default"`
	Ejected        *cfEjected `json:"ejected"`
	DefaultChanged bool       `json:"default_changed"`
	Diff           string     `json:"diff"`
}

type cfDefault struct {
	Ref  string `json:"ref"`
	Text string `json:"text"`
}

type cfEjected struct {
	Ref    string `json:"ref"`
	SHA256 string `json:"sha256"`
}

// readContainerfileText reads root's Containerfile.<name> as a hostile
// file; a missing one says how a Containerfile comes to be.
func readContainerfileText(root, name string) ([]byte, error) {
	rel := workspaceFile(name)
	data, err := safefile.Read(filepath.Join(root, filepath.FromSlash(rel)), safefile.Limit)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s does not exist: run railswitch containerfile eject %s (a default) or containerfile new %s (an addon)", rel, name, name)
	}
	return data, err
}

// cfShowJSON is the workspace file name with its default, the eject header
// and the diff from the default to the file without that header.
func cfShowJSON(root, name string) (*cfShowDoc, error) {
	data, err := readContainerfileText(root, name)
	if err != nil {
		return nil, err
	}
	_, _, st, err := projectStack(root)
	if err != nil {
		return nil, err
	}
	rel := workspaceFile(name)
	text := string(data)
	doc := &cfShowDoc{Name: name, File: rel, Kind: "user", Text: text, Hash: workspace.Hash(data)}
	first, rest, _ := strings.Cut(text, "\n")
	body := text
	if ref, sha, ok := parseEjectHeader(first); ok {
		doc.Ejected, body = &cfEjected{Ref: ref, SHA256: sha}, rest
	}
	if b, ok := stackBuiltins(st)[name]; ok {
		def, err := fs.ReadFile(b.files, b.file)
		if err != nil {
			return nil, err
		}
		doc.Kind = kindNames[b.kind]
		doc.Default = &cfDefault{Ref: b.ref, Text: string(def)}
		doc.DefaultChanged = doc.Ejected != nil && fmt.Sprintf("%x", sha256.Sum256(def)) != doc.Ejected.SHA256
		doc.Diff = settings.UnifiedDiff("default", rel, string(def), body)
	}
	return doc, nil
}

// railswitch containerfile show <name> [--default]: the workspace file, or
// the built-in default with --default or when there is no workspace file.
// With --json: the file, its default and the diff between them.
func containerfileShow(e *env, args []string) int {
	fset := e.flags("containerfile show")
	def := fset.Bool("default", false, "print the built-in default, not the workspace file")
	name, code, ok := parseNamed(e, fset, args, "usage: railswitch containerfile show <name> [--default]")
	if !ok {
		return code
	}
	if !cfNameRe.MatchString(name) {
		return e.usageErr("containerfile show: %q is not a Containerfile name (%s)", name, cfNameRe)
	}
	root, err := workspace.Root(e.dir)
	if err != nil {
		return e.fail(err)
	}
	if e.json {
		if *def {
			return e.usageErr("containerfile show: --json holds the default; drop --default")
		}
		doc, err := cfShowJSON(root, name)
		if err != nil {
			return e.fail(err)
		}
		if err := json.NewEncoder(e.stdout).Encode(doc); err != nil {
			return e.fail(err)
		}
		return 0
	}
	if !*def {
		data, err := safefile.Read(filepath.Join(root, filepath.FromSlash(workspaceFile(name))), safefile.Limit)
		if err == nil {
			e.stdout.Write(data)
			return 0
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return e.fail(err)
		}
	}
	_, cat, st, err := projectStack(root)
	if err != nil {
		return e.fail(err)
	}
	b, err := lookup(cat, st, name)
	if err != nil {
		return e.fail(err)
	}
	data, err := fs.ReadFile(b.files, b.file)
	if err != nil {
		return e.fail(err)
	}
	e.stdout.Write(data)
	return 0
}

// railswitch containerfile write <name> --if-hash <h> [--dry-run]
// [--restart]: replace a Containerfile the project builds with stdin, after
// rendering every worktree with it in memory (the D31/D36 checks).
func containerfileWrite(ctx context.Context, e *env, args []string) int {
	const usage = "usage: railswitch containerfile write <name> --if-hash <hash> [--dry-run] [--restart]  (the new text on stdin)"
	fset := e.flags("containerfile write")
	hash := fset.String("if-hash", "", "the hash of the file the edit started from (containerfile show --json; required)")
	dry := fset.Bool("dry-run", false, "check the new text and print what it changes; write nothing")
	restart := fset.Bool("restart", false, "restart the running worktrees whose units change")
	name, code, ok := parseNamed(e, fset, args, usage)
	if !ok {
		return code
	}
	if *hash == "" {
		return e.usageErr("%s", usage)
	}
	if !cfNameRe.MatchString(name) {
		return e.usageErr("containerfile write: %q is not a Containerfile name (%s)", name, cfNameRe)
	}
	text, err := e.readStdin()
	if err != nil {
		return e.fail(err)
	}
	se, _, err := e.settingsEnv(ctx)
	if err != nil {
		return e.fail(err)
	}
	rel := workspaceFile(name)
	if !slices.Contains(render.Containerfiles(se.WS.Project, se.WS.Stack), rel) {
		return e.fail(fmt.Errorf("%s is not built by this project; use containerfile new or eject", rel))
	}
	cur, err := readContainerfileText(se.WS.Root, name)
	if err != nil {
		return e.fail(err)
	}
	if workspace.Hash(cur) != *hash {
		return e.fail(fmt.Errorf("%s changed on disk since you opened it", rel))
	}
	ch, err := se.Compare(se.WS, se.WS.WithFile(rel, text))
	w := judgedWrite{hash: workspace.Hash(text), change: ch, err: err}
	if *dry {
		return e.printJudged(w)
	}
	return e.commitJudged(ctx, se.WS.Root, w, []fileChange{{Rel: rel, Data: text, Mode: 0o644}}, false, *restart)
}

// railswitch containerfile eject <name> [--force]: see Eject.
func containerfileEject(e *env, args []string) int {
	fset := e.flags("containerfile eject")
	force := fset.Bool("force", false, "replace an existing workspace file with the default")
	name, code, ok := parseNamed(e, fset, args, "usage: railswitch containerfile eject <name> [--force]")
	if !ok {
		return code
	}
	root, err := workspace.Root(e.dir)
	if err != nil {
		return e.fail(err)
	}
	_, cat, st, err := projectStack(root)
	if err != nil {
		return e.fail(err)
	}
	if err := Eject(root, cat, st, name, *force); err != nil {
		return e.fail(err)
	}
	fmt.Fprintf(e.stdout, "wrote %s\n", workspaceFile(name))
	return 0
}

// railswitch containerfile new <name>: a skeleton addon, appended to
// [image] addons.
func containerfileNew(e *env, args []string) int {
	fset := e.flags("containerfile new")
	name, code, ok := parseNamed(e, fset, args, "usage: railswitch containerfile new <name>")
	if !ok {
		return code
	}
	if !newNameRe.MatchString(name) {
		return e.usageErr("containerfile new: %q must match %s", name, newNameRe)
	}
	root, err := workspace.Root(e.dir)
	if err != nil {
		return e.fail(err)
	}
	p, cat, st, err := projectStack(root)
	if err != nil {
		return e.fail(err)
	}
	// The built-ins' names, and the stack's own (its local packages load on
	// demand, so builtinNames alone never sees them).
	if _, ok := stackBuiltins(st)[name]; ok || builtinNames(cat)[name] {
		return e.usageErr("containerfile new: %s is a built-in Containerfile; use railswitch containerfile eject %s", name, name)
	}
	proj := filepath.Join(root, "project.toml")
	rel := workspaceFile(name)
	skeleton := "# Addon for " + p.Project.Name + `. railswitch builds it on the previous image in the chain (see: railswitch containerfile list).
ARG BASE_IMAGE
FROM ${BASE_IMAGE}
USER root
# RUN apt-get update && apt-get install -y --no-install-recommends <packages> && rm -rf /var/lib/apt/lists/*
USER appuser
`
	created, err := writeContainerfile(root, rel, []byte(skeleton), false)
	if err == nil {
		err = undoCreate(root, rel, created, config.AppendImageAddon(proj, rel))
	}
	if err != nil {
		return e.fail(err)
	}
	fmt.Fprintf(e.stdout, "wrote %s\n", rel)
	return 0
}
