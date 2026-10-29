package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/netip"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/detect"
	"github.com/jbigler/railswitch/internal/lockstore"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/podman"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/template"
	"github.com/jbigler/railswitch/internal/workspace"
	"github.com/jbigler/railswitch/internal/worktree"
)

const newUsage = "usage: railswitch project new <dir> --repo <url> --prefix <p> [--dry-run] [--up] [--name <n>] [--branch <base>] [--template <ref> [--answer key=value]...] [--pack rails@1] [--option slot=ref]... [--tool nvim] [--agent name=ref]... | [--agent none]"

const (
	gitCloneTimeout  = 10 * time.Minute // checks out the app's tree
	gitRemoteTimeout = 2 * time.Minute  // asks the remote for its HEAD
)

// workspaceGitignore is the workspace repo's allowlist (D38): worktrees,
// .railswitch/, mise.local.toml and db-dumps/ are never tracked; local agent
// packages under agents/ and git-guard's hook backups under githooks/ (D60)
// are.
const workspaceGitignore = `/*
!/.gitignore
!/project.toml
!/stack.lock
!/containerfiles/
!/entrypoints/
!/packs/
!/options/
!/agents/
!/githooks/
`

// railswitch project new: clone the app repo into a new workspace (its own git
// repo, D38), write project.toml, eject every default Containerfile the
// stack builds, generate the local passwords, lock, register and render;
// with --up, then up in the base worktree. --dry-run makes the same checks
// and writes nothing (D89).
func cmdNew(ctx context.Context, e *env, args []string) int {
	fset := e.flags("project new")
	repo := fset.String("repo", "", "the app's git `url`")
	prefix := fset.String("prefix", "", "the project `prefix`: unique on this machine, names every object")
	name := fset.String("name", "", "the project `name` (default: the dir's name)")
	branch := fset.String("branch", "", "the `base` branch (default: the remote's HEAD branch)")
	packRef := fset.String("pack", "rails@1", "the stack's pack `ref`")
	dryRun := fset.Bool("dry-run", false, "write nothing: print the project.toml and what the base worktree gets")
	acceptHash := fset.String("accept-install", "", "accept the install review of its library packages with this `hash` (project new --dry-run --json install.hash)")
	up := fset.Bool("up", false, "run up in the base worktree once it is created (exit 3: created, up failed)")
	tplRef := fset.String("template", "", "start from the template `ref` (template ls): its lines, the answers, then the other flags on top")
	noDetect := fset.Bool("no-detect", false, "don't read the repository for the pack and options: the pack's defaults")
	var opts, tools, agentFlags, answerFlags stringList
	fset.Var(&answerFlags, "answer", "answer a --template question: `key=value` (repeatable; an empty value sets it empty, no flag keeps the default)")
	fset.Var(&opts, "option", "fill an option slot: `slot=ref` or slot=none (repeatable)")
	fset.Var(&tools, "tool", "enable a developer `tool`: nvim (repeatable)")
	fset.Var(&agentFlags, "agent", "add an agent: `name=ref` (repeatable), or none; default: the built-in default agent")
	dir, code, ok := parseNamed(e, fset, args, newUsage)
	if !ok {
		return code
	}
	if *repo == "" || *prefix == "" {
		return e.usageErr("new: --repo and --prefix are required\n%s", newUsage)
	}
	var nvim *config.NvimTool
	for _, t := range tools {
		if t != "nvim" {
			return e.usageErr("new: unknown tool %q (tools: nvim)", t)
		}
		nvim = &config.NvimTool{}
	}
	agents, err := parseAgentFlags(agentFlags)
	if err != nil {
		return e.usageErr("new: %v\n%s", err, newUsage)
	}
	answers, err := parseAnswers(answerFlags)
	if err != nil {
		return e.usageErr("new: %v\n%s", err, newUsage)
	}
	if *tplRef == "" && answers != nil {
		return e.usageErr("new: --answer answers a --template question\n%s", newUsage)
	}
	if *tplRef != "" && !isSet(fset, "pack") {
		*packRef = "" // the template's
	}
	// Neither a pack nor a template: read the repository as the New project
	// page does (D124). The page passes --pack after its own detection.
	if *tplRef == "" && !isSet(fset, "pack") && !*noDetect {
		// What needs no network is checked before the repository is read.
		if err := newOffline(dir, *prefix); err != nil {
			return e.fail(err)
		}
		res, err := e.detectNew(ctx, *repo, *branch)
		if err != nil {
			return e.fail(err)
		}
		*packRef, *branch = res.Pack.Ref, res.Branch
		set := map[string]bool{}
		for _, o := range opts {
			k, _, _ := strings.Cut(o, "=")
			set[k] = true
		}
		out := e.stdout
		if e.json {
			out = e.stderr // stdout is the one JSON document
		}
		fmt.Fprintf(out, "detected pack %s (%s)\n", res.Pack.Ref, safefile.Printable(strings.Join(res.Pack.Evidence, ", ")))
		for _, o := range res.Options {
			if o.Source != "repo" || set[o.Key] {
				continue
			}
			opts = append(opts, o.Key+"="+o.Ref)
			line := fmt.Sprintf("detected %s %s (%s)", o.Key, o.Ref, safefile.Printable(o.Evidence))
			if len(o.Others) > 0 {
				line += "; also in the repository: " + strings.Join(o.Others, ", ")
			}
			fmt.Fprintln(out, line)
		}
	}
	pl, err := planNew(ctx, e.run, newArgs{dir: dir, repo: *repo, prefix: *prefix, name: *name, branch: *branch, packRef: *packRef, opts: opts, nvim: nvim, agents: agents, template: *tplRef, answers: answers})
	if err != nil {
		return e.fail(err)
	}
	if *dryRun {
		return e.newDryRun(ctx, pl, *up)
	}
	// Judged before the clone: a library package is installed only through
	// its review (D106), and the lock checks the hash again.
	accept := workspace.Accept{}
	install, refs, err := newInstall(ctx, pl)
	switch {
	case err != nil:
		return e.fail(err)
	case install == nil && *acceptHash != "":
		return e.fail(errors.New("--accept-install: the project installs no library package"))
	case install != nil && *acceptHash == "":
		return e.fail(fmt.Errorf("installing %s needs review: project new --dry-run shows the install review; accept it with --accept-install <hash>", pl.reviewed(refs)))
	case install != nil && install.Hash != *acceptHash:
		return e.fail(errInstallChanged)
	case install != nil:
		accept = workspace.Accept{Install: refs, IfHash: install.Hash, Run: e.run, Template: pl.libTemplate}
	}

	ws, slug := pl.ws, pl.slug
	before, err := entries(ws)
	if err != nil {
		return e.fail(err)
	}
	if err := clone(ctx, e.run, *repo, pl.p.Project.BaseBranch, filepath.Join(ws, slug)); err != nil {
		return e.fail(err)
	}
	secrets, id, err := e.createWorkspace(ctx, pl, accept)
	if err != nil {
		return e.failLeft("project new", err, ws, before)
	}
	fmt.Fprintf(e.stdout, "created workspace %s: project %s, base worktree %s (ID %d)\n", ws, *prefix, slug, id)
	fmt.Fprintf(e.stdout, "the workspace is a git repo: commit project.toml, stack.lock and containerfiles/ (git -C %s add -A && git -C %s commit)\n", shellQuote(ws), shellQuote(ws))
	e.printNext(ws, slug, secrets)
	if !*up {
		return 0
	}
	e.dir, e.slug = filepath.Join(ws, slug), slug
	l, slug, code := e.lifecycle(ctx, "up")
	if code == 0 {
		if err := l.Up(ctx, slug); err != nil {
			code = e.fail(err)
		}
	}
	if code != 0 {
		fmt.Fprintf(e.stderr, "railswitch: project %s was created in %s, but up failed; fix the cause and run: cd %s && railswitch up\n", *prefix, ws, shellQuote(filepath.Join(ws, slug)))
		return exitCreatedUpFailed
	}
	return 0
}

// exitCreatedUpFailed is project new --up's exit code when the project was
// created and registered but up failed (docs/json.md).
const exitCreatedUpFailed = 3

// newArgs are project new's flags, parsed.
type newArgs struct {
	dir, repo, prefix, name, branch string
	packRef                         string // "": the template's
	opts                            []string
	nvim                            *config.NvimTool
	agents                          map[string]string // nil: the catalog's default agent
	template                        string            // a template ref; "" for none
	answers                         map[string]string // its questions' answers
}

// newPlan is a project new that passed every check: what the real run
// creates and the dry run shows.
type newPlan struct {
	ws, slug string
	p        *config.Project // base_branch set; writeProject adds [image] and the secrets
	cat      *pack.Catalog
	st       *pack.Stack
	dirs     machine.Dirs
	reg      *machine.Registry // read without the lock, never saved
	// tpl is the template the project starts from (nil: none), resolved
	// once: its files are what the review hashed and what is written.
	tpl     *template.Template
	answers map[string]string
	// libTemplate is tpl as an Install review shows it, for a library
	// template; nil otherwise.
	libTemplate *workspace.Template
	// bundles are tpl's bundled packages the stack uses (an answer or
	// --pack may pick another): what writeProject writes.
	bundles []template.Bundle
}

// usedBundles are tpl's bundles st's pack or options are, by directory.
func usedBundles(tpl *template.Template, st *pack.Stack) []template.Bundle {
	if tpl == nil {
		return nil
	}
	refs := []pack.Ref{st.PackRef}
	for _, ch := range st.Options {
		refs = append(refs, ch.Ref)
	}
	var out []template.Bundle
	for _, r := range refs {
		if b, ok := tpl.Bundled[r]; ok {
			out = append(out, b)
		}
	}
	slices.SortFunc(out, func(x, y template.Bundle) int { return strings.Compare(x.Dir, y.Dir) })
	return out
}

// reviewed names what an Install review of pl with refs covers.
func (pl *newPlan) reviewed(refs []pack.Ref) string {
	names := refList(refs)
	if t := pl.libTemplate; t != nil {
		if names != "" {
			names += ", "
		}
		names += "template " + t.Ref.String()
	}
	return names
}

// planNew makes every check project new makes before it clones, in order,
// and writes nothing. The base branch, when not given, is the remote's HEAD.
func planNew(ctx context.Context, r sys.Runner, a newArgs) (*newPlan, error) {
	ws, err := filepath.Abs(a.dir)
	if err != nil {
		return nil, err
	}
	if a.name == "" {
		a.name = filepath.Base(ws)
	}
	switch _, err := os.Lstat(filepath.Join(ws, "project.toml")); {
	case err == nil:
		return nil, fmt.Errorf("%s already exists: run railswitch project init there to join that project", filepath.Join(ws, "project.toml"))
	case !errors.Is(err, fs.ErrNotExist):
		return nil, err
	}
	cat, err := workspace.LoadCatalog(ws)
	if err != nil {
		return nil, err
	}
	dirs, err := machine.DefaultDirs()
	if err != nil {
		return nil, err
	}
	var tpl *template.Template
	var lines *config.Project
	var libTemplate *workspace.Template
	if a.template != "" {
		if tpl, lines, libTemplate, err = newTemplate(dirs, a.template, a.answers); err != nil {
			return nil, err
		}
		// Its bundled packages (D116) are the new workspace's own: the
		// stack resolves with them before anything is written.
		for _, r := range slices.SortedFunc(maps.Keys(tpl.Bundled), func(x, y pack.Ref) int { return strings.Compare(x.String(), y.String()) }) {
			b := tpl.Bundled[r]
			if cat, err = cat.WithPackage(b.Kind, r, b.Files, nil); err != nil {
				return nil, fmt.Errorf("template %s: %s/: %w", a.template, b.Dir, err)
			}
		}
	}
	if a.agents == nil {
		if ref, ok := cat.DefaultAgent(); ok {
			a.agents = map[string]string{ref.Name: ref.String()}
		}
	}
	p, st, err := newProject(cat, a.packRef, a.name, a.prefix, a.repo, a.opts, a.nvim, a.agents, lines)
	if err != nil {
		return nil, err
	}
	reg, err := readRegistry(dirs)
	if err != nil {
		return nil, err
	}
	if pe, ok := reg.Project(a.prefix); ok {
		return nil, prefixTaken(a.prefix, pe.Workspace)
	}
	base := a.branch
	if base == "" {
		if base, err = remoteHead(ctx, r, a.repo); err != nil {
			return nil, err
		}
	}
	if err := sys.GitArg("--branch", base); err != nil {
		return nil, err
	}
	slug, err := naming.Sanitize(base)
	if err != nil {
		return nil, fmt.Errorf("base branch %q: %w", base, err)
	}
	p.Project.BaseBranch = base
	if tpl != nil {
		personal := personalContainerfiles(cat, st)
		for _, n := range slices.Sorted(maps.Keys(tpl.Containerfiles)) {
			if personal[n] {
				return nil, fmt.Errorf("template %s holds Containerfile.%s, the editor's or an agent's image: a template never brings one (D112)", a.template, n)
			}
		}
	}
	bundles := usedBundles(tpl, st)
	if err := newPathsFree(ws, slug, st, tpl, bundles); err != nil {
		return nil, err
	}
	return &newPlan{ws: ws, slug: slug, p: p, cat: cat, st: st, dirs: dirs, reg: reg, tpl: tpl, answers: a.answers, libTemplate: libTemplate, bundles: bundles}, nil
}

// newTemplate resolves the template ref on this machine (D110) and applies
// answers: the template, the lines a new project starts from (recording
// it in [template]) and, for a library template, what its Install review
// shows. An answer to a question it doesn't ask is refused.
func newTemplate(dirs machine.Dirs, ref string, answers map[string]string) (*template.Template, *config.Project, *workspace.Template, error) {
	r, err := pack.ParseRef(ref)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("--template: %w", err)
	}
	libs := workspace.Libraries(dirs)
	t, sha, err := template.Resolve(ref, dirs, libs)
	if err != nil {
		return nil, nil, nil, err
	}
	base, err := template.Apply(t, answers)
	if err != nil {
		return nil, nil, nil, err
	}
	base.Template = &config.TemplateRef{Ref: r.String(), SHA256: sha}
	var lt *workspace.Template
	if r.Library != "" {
		co, err := libs.Checkout(r.Library)
		if err != nil {
			return nil, nil, nil, err
		}
		lt = &workspace.Template{Ref: r, SHA256: sha, Signer: co.Signer, Files: t.Files}
	}
	return t, base, lt, nil
}

// parseAnswers reads project new's --answer values: key=value each, a key
// at most once; nil for none.
func parseAnswers(flags []string) (map[string]string, error) {
	if flags == nil {
		return nil, nil
	}
	out := map[string]string{}
	for _, f := range flags {
		k, v, ok := strings.Cut(f, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("--answer %q is not key=value", f)
		}
		if _, dup := out[k]; dup {
			return nil, fmt.Errorf("--answer %s is given twice", k)
		}
		out[k] = v
	}
	return out, nil
}

// newPathsFree refuses a workspace dir that is not a directory, and each
// path project new creates in ws that already exists: the clone's dir,
// the files it writes and each Containerfile it ejects or takes from the
// template tpl (nil: none). Dry and real run refuse here, before the
// clone, with the same message.
func newPathsFree(ws, slug string, st *pack.Stack, tpl *template.Template, bundles []template.Bundle) error {
	switch fi, err := os.Stat(ws); {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case !fi.IsDir():
		return fmt.Errorf("%s is not a directory", ws)
	}
	rels := []string{slug, ".gitignore", "mise.local.toml", "stack.lock"}
	for _, n := range ejectNames(st) {
		rels = append(rels, workspaceFile(n))
	}
	if tpl != nil {
		for _, n := range slices.Sorted(maps.Keys(tpl.Containerfiles)) {
			rels = append(rels, workspaceFile(n))
		}
	}
	for _, b := range bundles {
		rels = append(rels, b.Dir)
	}
	for _, rel := range rels {
		p := filepath.Join(ws, filepath.FromSlash(rel))
		switch _, err := os.Lstat(p); {
		case err == nil:
			return fmt.Errorf("%s already exists, and project new creates it: remove it or pick another folder", p)
		case !errors.Is(err, fs.ErrNotExist):
			return err
		}
	}
	return nil
}

// createWorkspace is new after the clone: everything up to the render.
// It returns the declared secrets (all unset) and the base worktree's ID.
func (e *env) createWorkspace(ctx context.Context, pl *newPlan, accept workspace.Accept) ([]string, int, error) {
	ws := pl.ws
	if _, err := e.run.Run(ctx, sys.Cmd{Name: "git", Args: []string{"init", ws}}); err != nil {
		return nil, 0, fmt.Errorf("git init %s: %w", ws, err)
	}
	if err := createFile(filepath.Join(ws, ".gitignore"), []byte(workspaceGitignore), 0o644); err != nil {
		return nil, 0, err
	}
	if err := prepare(ws, pl.slug, pl.st); err != nil {
		return nil, 0, err
	}
	secrets, err := writeProject(ws, pl)
	if err != nil {
		return nil, 0, err
	}
	if err := writeGenerated(ws, pl.st, randomValue); err != nil {
		return nil, 0, err
	}
	if err := workspace.Lock(ctx, ws, accept); err != nil {
		return nil, 0, err
	}
	if err := storeTemplate(pl); err != nil {
		return nil, 0, err
	}
	w, err := workspace.Load(ctx, ws, "")
	if err != nil {
		return nil, 0, err
	}
	id, err := e.registerAndRender(ctx, pl.dirs, w, pl.slug)
	return secrets, id, err
}

// writeProject writes pl's project.toml into dir, ejects the stack's
// default Containerfiles there (which sets [image]), declares the
// template's secrets, puts the template's Containerfiles in place of the ejected ones
// (its user addons appended to [image] addons) and declares the secrets
// the agents and nvim need (addNewSecrets), returning the names of every
// declared secret. The real run writes into the workspace, the dry run into
// a temp dir: one path, the same bytes.
func writeProject(dir string, pl *newPlan) ([]string, error) {
	for _, b := range pl.bundles {
		if err := writeBundle(filepath.Join(dir, filepath.FromSlash(b.Dir)), b.Files); err != nil {
			return nil, fmt.Errorf("%s: %w", b.Dir, err)
		}
	}
	proj := filepath.Join(dir, "project.toml")
	if err := createFile(proj, projectTOML(pl.p), 0o644); err != nil {
		return nil, err
	}
	for _, n := range ejectNames(pl.st) {
		if err := Eject(dir, pl.cat, pl.st, n, false); err != nil {
			return nil, fmt.Errorf("eject %s: %w", n, err)
		}
	}
	declared := slices.Sorted(maps.Keys(pl.p.Secrets)) // the template's
	for _, n := range declared {
		if err := config.AddSecret(proj, n, pl.p.Secrets[n]); err != nil {
			return nil, err
		}
	}
	if t := pl.tpl; t != nil {
		for _, n := range slices.Sorted(maps.Keys(t.Containerfiles)) {
			if _, err := writeContainerfile(dir, workspaceFile(n), t.Containerfiles[n], true); err != nil {
				return nil, err
			}
		}
		for _, a := range t.Lines.Image.Addons {
			if err := config.AppendImageAddon(proj, a); err != nil {
				return nil, err
			}
		}
	}
	names, err := addNewSecrets(proj, pl.p, pl.st)
	return append(declared, names...), err
}

// writeBundle writes a template's bundled package files under dir, which
// newPathsFree found absent; scripts (.sh) are executable.
func writeBundle(dir string, files fs.FS) error {
	return fs.WalkDir(files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(files, p)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if path.Ext(p) == ".sh" {
			mode = 0o755
		}
		return writePackageFile(filepath.Join(dir, filepath.FromSlash(p)), data, mode)
	})
}

// storeTemplate stores pl's template in the store beside the packages
// (D96), so template check can diff an update against what was accepted.
func storeTemplate(pl *newPlan) error {
	if pl.tpl == nil {
		return nil
	}
	sha, err := lockstore.Store{Dir: lockstore.Dir(pl.dirs)}.Put(pl.tpl.Files)
	if err != nil {
		return fmt.Errorf("store template %s: %w", pl.p.Template.Ref, err)
	}
	if sha != pl.p.Template.SHA256 {
		return fmt.Errorf("template %s changed while the project was created: start again", pl.p.Template.Ref)
	}
	return nil
}

// railswitch project init: join a project from a clone of its workspace repo (D38).
func cmdInit(ctx context.Context, e *env, args []string) int {
	fset := e.flags("project init")
	if code, ok := parse(fset, args); !ok {
		return code
	}
	if fset.NArg() != 0 {
		return e.usageErr("project init takes no arguments")
	}
	root, err := workspace.Root(e.dir)
	if err != nil {
		return e.fail(err)
	}
	w, err := workspace.Load(ctx, root, "")
	if err != nil {
		return e.fail(err)
	}
	if err := w.CheckLock(); err != nil {
		return e.fail(err)
	}
	pm := w.Project.Project
	if pm.Repo == "" || pm.BaseBranch == "" {
		return e.fail(fmt.Errorf("%s: init needs [project] repo and base_branch", filepath.Join(root, "project.toml")))
	}
	for _, a := range [][2]string{{"[project] repo", pm.Repo}, {"[project] base_branch", pm.BaseBranch}} {
		if err := sys.GitArg(a[0], a[1]); err != nil {
			return e.fail(err)
		}
	}
	slug, err := naming.Sanitize(pm.BaseBranch)
	if err != nil {
		return e.fail(fmt.Errorf("[project] base_branch %q: %w", pm.BaseBranch, err))
	}
	if err := prefixFree(w.Dirs, pm.Prefix); err != nil {
		return e.fail(err)
	}
	before, err := entries(root)
	if err != nil {
		return e.fail(err)
	}
	if err := clone(ctx, e.run, pm.Repo, pm.BaseBranch, filepath.Join(root, slug)); err != nil {
		return e.fail(err)
	}
	err = prepare(root, slug, w.Stack)
	if err == nil {
		err = writeGenerated(root, w.Stack, randomValue)
	}
	id := 0
	if err == nil {
		id, err = e.registerAndRender(ctx, w.Dirs, w, slug)
	}
	if err != nil {
		return e.failLeft("project init", err, root, before)
	}
	fmt.Fprintf(e.stdout, "joined project %s in %s: base worktree %s (ID %d)\n", pm.Prefix, w.Root, slug, id)

	p := podman.Podman{R: e.run}
	var unset []string
	for _, name := range slices.Sorted(maps.Keys(w.Project.Secrets)) {
		set, err := p.SecretExists(ctx, render.SecretName(pm.Prefix, name, w.Project.Secrets[name]))
		if err != nil {
			fmt.Fprintf(e.stderr, "railswitch: secret %s: %v\n", name, err)
		}
		if !set {
			unset = append(unset, name)
		}
	}
	e.printNext(w.Root, slug, unset)
	return 0
}

// printNext prints the next commands: setting each unset secret, else up.
func (e *env) printNext(ws, slug string, unset []string) {
	if len(unset) == 0 {
		fmt.Fprintf(e.stdout, "next: cd %s && railswitch up\n", shellQuote(filepath.Join(ws, slug)))
		return
	}
	fmt.Fprintf(e.stdout, "next: cd %s\n", shellQuote(ws))
	for _, n := range unset {
		fmt.Fprintf(e.stdout, "next: railswitch secret set %s\n", n)
	}
}

// newOffline is what planNew checks that needs no network: the prefix's
// form, that no project has it, and that dir holds no project yet.
func newOffline(dir, prefix string) error {
	if err := naming.ValidPrefix(prefix); err != nil {
		return err
	}
	dirs, err := machine.DefaultDirs()
	if err != nil {
		return err
	}
	if err := prefixFree(dirs, prefix); err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(dir, "project.toml")); err == nil {
		return fmt.Errorf("%s already exists: run railswitch project init there to join that project", filepath.Join(dir, "project.toml"))
	}
	return nil
}

// detectNew reads repo (at branch, else the remote's HEAD) for the pack and
// options, as project detect does (D88); no pack matching is an error that
// names --pack.
func (e *env) detectNew(ctx context.Context, repo, branch string) (*detect.Result, error) {
	if err := sys.GitArg("--repo", repo); err != nil {
		return nil, err
	}
	cat, err := pack.LoadBuiltins()
	if err != nil {
		return nil, err
	}
	if branch == "" {
		if branch, err = remoteHead(ctx, e.run, repo); err != nil {
			return nil, err
		}
	}
	if err := sys.GitArg("--branch", branch); err != nil {
		return nil, err
	}
	files, err := detect.Fetch(ctx, e.run, repo, branch, detect.Paths(cat))
	if err != nil {
		return nil, fmt.Errorf("reading the repository: %w (--pack or --no-detect skips it)", err)
	}
	res, err := detect.Choose(cat, files, "")
	if err != nil {
		return nil, err
	}
	if res.Pack == nil {
		return nil, errors.New("no pack's files are all in the repository: pass --pack")
	}
	res.Branch = branch
	return &res, nil
}

// newProject is the project.toml new writes, before [image] and secrets:
// the pack's defaults, or a template's lines (base), overlaid by opts
// (slot=ref), packRef ("": base's pack), nvim and agents (name → package
// ref; a template has neither: D112), and the chosen options' version pins it lacks. It is
// validated as it will be once base is ejected.
func newProject(cat *pack.Catalog, packRef, name, prefix, repo string, opts []string, nvim *config.NvimTool, agents map[string]string, base *config.Project) (*config.Project, *pack.Stack, error) {
	if packRef == "" && base != nil {
		packRef = base.Stack.Pack
	}
	ref, err := pack.ParseRef(packRef)
	if err != nil {
		return nil, nil, fmt.Errorf("--pack: %w", err)
	}
	pk, err := cat.Pack(ref)
	if err != nil {
		return nil, nil, fmt.Errorf("--pack: %w", err)
	}
	if pk == nil {
		return nil, nil, fmt.Errorf("--pack: unknown pack %s", ref)
	}
	if err := sys.GitArg("--repo", repo); err != nil {
		return nil, nil, err
	}
	p := &config.Project{Schema: config.SchemaVersion, Options: maps.Clone(pk.Defaults)}
	if base != nil {
		q := *base
		p = &q
		p.Options, p.Versions = maps.Clone(base.Options), maps.Clone(base.Versions)
	}
	p.Project = config.Meta{Name: name, Prefix: prefix, Repo: repo}
	if base == nil || p.Stack.Pack != ref.String() {
		p.Stack = config.StackRef{Pack: ref.String(), App: pk.App.Service}
	}
	if nvim != nil && p.Tools.Nvim == nil {
		p.Tools.Nvim = nvim
	}
	for n, use := range agents {
		if p.Agents == nil {
			p.Agents = map[string]config.Agent{}
		}
		p.Agents[n] = config.Agent{Use: use}
	}
	if p.Options == nil {
		p.Options = map[string]string{}
	}
	for _, o := range opts {
		key, val, ok := strings.Cut(o, "=")
		if !ok || key == "" {
			return nil, nil, fmt.Errorf("--option %q is not key=ref (a slot: %s; or a service name for an extra service)", o, strings.Join(config.Slots, ", "))
		}
		p.Options[key] = val
	}
	st, err := cat.Resolve(p)
	if err != nil {
		return nil, nil, err
	}
	// A pin the pack also resolves as a runtime stays out of [versions]: it
	// would beat the app's own file (rails: playwright from Gemfile.lock).
	for _, ch := range st.Options {
		if ch.Option.Pin == nil {
			continue
		}
		if _, ok := st.Pack.Runtimes[ch.Option.Pin.Name]; ok {
			continue
		}
		if p.Versions == nil {
			p.Versions = map[string]string{}
		}
		if _, ok := p.Versions[ch.Option.Pin.Name]; !ok {
			p.Versions[ch.Option.Pin.Name] = ch.Option.Pin.Default
		}
	}
	q := *p
	q.Project.BaseBranch = "x"
	q.Image.Base = workspaceFile("base")
	if err := q.Validate(); err != nil {
		return nil, nil, err
	}
	return p, st, nil
}

// projectTOML renders p's schema, hide_links, [project], its lines
// (config.LinesTOML) and [template]: not [image] and [secrets], which
// writeProject adds.
func projectTOML(p *config.Project) []byte {
	var b strings.Builder
	kv := func(k, v string) { fmt.Fprintf(&b, "%s = %s\n", k, config.QuoteString(v)) }
	fmt.Fprintf(&b, "schema = %d\n", p.Schema)
	b.WriteString(config.HideLinksTOML(p))
	b.WriteString("\n[project]\n")
	kv("name", p.Project.Name)
	kv("prefix", p.Project.Prefix)
	kv("repo", p.Project.Repo)
	kv("base_branch", p.Project.BaseBranch)
	b.WriteString(config.LinesTOML(p))
	if t := p.Template; t != nil {
		b.WriteString("\n[template]                 # the template this project started from (D111): never applied again\n")
		kv("ref", t.Ref)
		kv("sha256", t.SHA256)
	}
	return []byte(b.String())
}

// parseAgentFlags reads project new's --agent values: name=ref each, or a
// lone none (no agents: an empty map). No flag is nil: the caller adds
// the catalog's default agent.
func parseAgentFlags(flags []string) (map[string]string, error) {
	if flags == nil {
		return nil, nil
	}
	agents := map[string]string{}
	for _, f := range flags {
		if f == "none" {
			if len(flags) > 1 {
				return nil, errors.New("--agent none takes no other --agent")
			}
			return agents, nil
		}
		name, ref, ok := strings.Cut(f, "=")
		if !ok || name == "" || ref == "" {
			return nil, fmt.Errorf("--agent %q is not name=ref or none", f)
		}
		if _, dup := agents[name]; dup {
			return nil, fmt.Errorf("--agent %s is given twice", name)
		}
		agents[name] = ref
	}
	return agents, nil
}

// addNewSecrets declares the secrets a new project needs, in project.toml
// at proj, and returns their names (all unset): see planSecrets, with
// every agent and nvim as the git services.
func addNewSecrets(proj string, p *config.Project, st *pack.Stack) ([]string, error) {
	var gitServices []string
	for _, a := range st.Agents {
		gitServices = append(gitServices, a.Service)
	}
	if p.Tools.Nvim != nil {
		gitServices = append(gitServices, "nvim")
	}
	return planSecrets(p, st.Agents, gitServices).write(proj)
}

// secretPlan is the secret edits that adding agents (and nvim) makes to
// project.toml: secrets to declare, in order, and services to add to
// declared secrets.
type secretPlan struct {
	declare []namedSecret
	extend  []namedSecret // Services: the services to add to secret Name
}

type namedSecret struct {
	Name string
	config.Secret
}

// planSecrets is the one rule project new and agent add share: each
// agent's package [[secrets]] as <agent>_<name> ("-" in the agent's name
// becomes "_"), received by that agent, unless p declares it; then git
// over HTTPS for gitServices: every declared secret with git_host gains
// those it lacks, or, when none has git_host and the repo is on
// github.com, github_token is declared for them.
func planSecrets(p *config.Project, agents []*pack.ChosenAgent, gitServices []string) secretPlan {
	var sp secretPlan
	for _, a := range agents {
		for _, s := range a.Agent.Secrets {
			name := strings.ReplaceAll(a.Name, "-", "_") + "_" + s.Name
			if _, ok := p.Secrets[name]; !ok {
				sp.declare = append(sp.declare, namedSecret{name, config.Secret{Services: []string{a.Service}, Env: s.Env}})
			}
		}
	}
	hasGitHost := false
	for _, n := range slices.Sorted(maps.Keys(p.Secrets)) {
		s := p.Secrets[n]
		if s.GitHost == "" {
			continue
		}
		hasGitHost = true
		var add []string
		for _, id := range gitServices {
			if !slices.Contains(s.Services, id) {
				add = append(add, id)
			}
		}
		if len(add) > 0 {
			sp.extend = append(sp.extend, namedSecret{n, config.Secret{Services: add}})
		}
	}
	if !hasGitHost && len(gitServices) > 0 && gitHub(p.Project.Repo) {
		sp.declare = append(sp.declare, namedSecret{"github_token", config.Secret{Services: gitServices, GitHost: "github.com"}})
	}
	return sp
}

// apply returns p's secrets with the plan applied, for validation.
func (sp secretPlan) apply(secrets map[string]config.Secret) map[string]config.Secret {
	out := maps.Clone(secrets)
	if out == nil {
		out = map[string]config.Secret{}
	}
	for _, d := range sp.declare {
		out[d.Name] = d.Secret
	}
	for _, x := range sp.extend {
		s := out[x.Name]
		s.Services = append(slices.Clone(s.Services), x.Services...)
		out[x.Name] = s
	}
	return out
}

// write makes the plan's edits to project.toml at proj and returns the
// declared secrets' names (all unset).
func (sp secretPlan) write(proj string) ([]string, error) {
	var names []string
	for _, d := range sp.declare {
		if err := config.AddSecret(proj, d.Name, d.Secret); err != nil {
			return nil, err
		}
		names = append(names, d.Name)
	}
	for _, x := range sp.extend {
		for _, id := range x.Services {
			if err := config.AddSecretService(proj, x.Name, id); err != nil {
				return nil, err
			}
		}
	}
	return names, nil
}

// ejectNames are the defaults the stack builds, in eject order: base
// first (it sets [image] base, which Validate requires), then each chosen
// option's app addon, each enabled tool, agent-base and each distinct agent
// package (when the stack has agents) and each option's standalone build.
func ejectNames(st *pack.Stack) []string {
	names := []string{"base"}
	for _, ch := range st.Options {
		if ch.Option.AppAddon != nil {
			names = append(names, cfName(ch.Option.AppAddon.File))
		}
	}
	for _, t := range st.Tools {
		names = append(names, cfName(t.Image.File))
	}
	if len(st.Agents) > 0 {
		names = append(names, "agent-base")
	}
	for _, a := range st.Agents {
		if n := cfName(a.Agent.Image.File); !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	for _, ch := range st.Options {
		if ch.Option.Image.Build != "" {
			names = append(names, cfName(ch.Option.Image.Build))
		}
	}
	return names
}

// prefixFree refuses a prefix registered on this machine.
func prefixFree(dirs machine.Dirs, prefix string) error {
	reg, err := readRegistry(dirs)
	if err != nil {
		return err
	}
	if pe, ok := reg.Project(prefix); ok {
		return prefixTaken(prefix, pe.Workspace)
	}
	return nil
}

// readRegistry reads the machine registry without the lock and without
// creating it (none yet: empty). registerAndRender checks the prefix again
// under the lock.
func readRegistry(dirs machine.Dirs) (*machine.Registry, error) {
	reg, err := machine.Read(dirs.Data)
	if errors.Is(err, fs.ErrNotExist) {
		return &machine.Registry{Schema: machine.SchemaVersion}, nil
	}
	return reg, err
}

func prefixTaken(prefix, ws string) error {
	return fmt.Errorf("prefix %q is already used by %s", prefix, ws)
}

// remoteHead is the branch the remote's HEAD names, from
// `git ls-remote --symref <repo> HEAD`.
func remoteHead(ctx context.Context, r sys.Runner, repo string) (string, error) {
	if err := sys.GitArg("--repo", repo); err != nil {
		return "", err
	}
	env, err := sys.GitNetEnv(ctx, r, os.Environ())
	if err != nil {
		return "", err
	}
	res, err := r.Run(ctx, sys.Cmd{Name: "git", Args: append(slices.Clone(sys.GitProtocols), "ls-remote", "--symref", repo, "HEAD"), Env: env, Timeout: gitRemoteTimeout})
	if err != nil {
		return "", fmt.Errorf("git ls-remote %s: %w", repo, err)
	}
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		ref, ok := strings.CutPrefix(line, "ref: refs/heads/")
		if !ok {
			continue
		}
		if b, ok := strings.CutSuffix(ref, "\tHEAD"); ok && b != "" {
			return b, nil
		}
	}
	if strings.TrimSpace(string(res.Stdout)) == "" {
		return "", fmt.Errorf("%s has no commits yet: commit there first, or pass --branch <base>", repo)
	}
	return "", fmt.Errorf("%s has a detached HEAD, so it names no branch: pass --branch <base>", repo)
}

// clone checks branch base of repo out into dir.
func clone(ctx context.Context, r sys.Runner, repo, base, dir string) error {
	env, err := sys.GitNetEnv(ctx, r, os.Environ())
	if err != nil {
		return err
	}
	_, err = r.Run(ctx, sys.Cmd{Name: "git", Args: append(slices.Clone(sys.GitProtocols), "clone", "--branch", base, repo, dir), Env: env, Timeout: gitCloneTimeout})
	if err != nil {
		return fmt.Errorf("git clone --branch %s %s: %w", base, repo, err)
	}
	return nil
}

// prepare creates <ws>/db-dumps/ (0755: the database container mounts it
// and runs its initdb restore as the image's own non-root user, which must
// list it; up's preflight needs every bind source) and, in the base worktree, the mount
// points of the pack's worktree volumes, as wt add does.
func prepare(ws, slug string, st *pack.Stack) error {
	dumps := filepath.Join(ws, "db-dumps")
	switch err := os.Mkdir(dumps, 0o755); {
	case err == nil:
		if err := os.Chmod(dumps, 0o755); err != nil {
			return err
		}
	case !errors.Is(err, fs.ErrExist):
		return err
	}
	return worktree.PrecreateVolumes(filepath.Join(ws, slug), worktree.VolumeDirs(st.Pack))
}

// writeGenerated writes <ws>/mise.local.toml (0600) with a value from value
// for each generated name of the chosen options (D17). The values are never
// printed.
func writeGenerated(ws string, st *pack.Stack, value func() string) error {
	var names []string
	for _, ch := range st.Options {
		names = append(names, ch.Option.Generated...)
	}
	slices.Sort(names)
	var b strings.Builder
	b.WriteString("# Local values railswitch generated for the services (D17). Never commit this file.\n[env]\n")
	for _, n := range slices.Compact(names) {
		fmt.Fprintf(&b, "%s = %s\n", n, config.QuoteString(value()))
	}
	return createFile(filepath.Join(ws, "mise.local.toml"), []byte(b.String()), 0o600)
}

// randomValue is a fresh local password: 32 hex digits.
func randomValue() string {
	buf := make([]byte, 16)
	rand.Read(buf)
	return hex.EncodeToString(buf)
}

// createFile writes a new file; an existing one is refused.
func createFile(path string, data []byte, perm fs.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	return errors.Join(err, f.Close())
}

// registerAndRender registers w's project, its port bases and its base
// worktree slug, then renders. The project's subnet skips every subnet an
// existing podman network uses; the worktree's ID skips every ID with a
// port another program listens on. A failed render unregisters the project
// again: the shared Traefik unit names every registered project's network.
func (e *env) registerAndRender(ctx context.Context, dirs machine.Dirs, w *workspace.Workspace, slug string) (int, error) {
	prefix := w.Project.Project.Prefix
	nets, err := podman.Podman{R: e.run}.NetworkSubnets(ctx)
	if err != nil {
		return 0, fmt.Errorf("listing podman networks to pick a free subnet: %w", err)
	}
	var taken []netip.Prefix
	for _, subnets := range nets {
		taken = append(taken, subnets...)
	}
	busy, err := machine.Listening(procRoot)
	if err != nil {
		return 0, fmt.Errorf("listing listening ports to pick a free worktree ID: %w", err)
	}
	reg, err := machine.Open(dirs.Data)
	if err != nil {
		return 0, err
	}
	if pe, ok := reg.Project(prefix); ok {
		reg.Close()
		return 0, prefixTaken(prefix, pe.Workspace)
	}
	_, err = reg.AddProject(prefix, w.Root, taken)
	var m *render.Model
	if err == nil {
		// The bases only: the host values change no port.
		m, err = w.Model(reg.Registry, render.Host{})
	}
	if err == nil {
		err = reg.SetPorts(prefix, m.PortBases())
	}
	var wt machine.WorktreeEntry
	if err == nil {
		wt, err = reg.AddWorktree(prefix, slug, busy)
	}
	if err == nil {
		err = reg.Save()
	}
	reg.Close() // Render opens the registry itself
	if err != nil {
		return 0, err
	}
	if _, err := e.render(ctx, w, false); err != nil {
		return 0, errors.Join(err, unregister(dirs, prefix))
	}
	return wt.ID, nil
}

// unregister drops prefix from the registry and refreshes the machine-wide
// units a partial render may have written, so the shared Traefik unit
// stops naming the project's network.
func unregister(dirs machine.Dirs, prefix string) error {
	reg, err := machine.Open(dirs.Data)
	if err != nil {
		return fmt.Errorf("unregistering project %s: %w", prefix, err)
	}
	defer reg.Close()
	if err := reg.RemoveProject(prefix); err != nil {
		return fmt.Errorf("unregistering project %s: %w", prefix, err)
	}
	if err := reg.Save(); err != nil {
		return fmt.Errorf("unregistering project %s: %w", prefix, err)
	}
	if err := machine.RefreshProxyUnits(reg.Registry, dirs.Quadlet); err != nil {
		return fmt.Errorf("unregistered project %s, but rewriting %s failed (railswitch render in another project rewrites it): %w", prefix, filepath.Join(dirs.Quadlet, machine.ProxyUnitName), err)
	}
	return nil
}

// entries are the names in dir; a missing dir has none.
func entries(dir string) (map[string]bool, error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, e := range ents {
		out[e.Name()] = true
	}
	return out, nil
}

// failLeft reports err of a failed new or init and what it left in ws
// (everything not in before; all of ws when ws did not exist), with the
// command that removes it. Nothing is deleted: it holds the user's clone.
func (e *env) failLeft(cmd string, err error, ws string, before map[string]bool) int {
	fmt.Fprintf(e.stderr, "railswitch: %s: %v\n", cmd, err)
	var left []string
	if before == nil {
		left = []string{ws}
	} else {
		now, lerr := entries(ws)
		if lerr != nil {
			fmt.Fprintf(e.stderr, "railswitch: %v\n", lerr)
		}
		for _, n := range slices.Sorted(maps.Keys(now)) {
			if !before[n] {
				left = append(left, filepath.Join(ws, n))
			}
		}
	}
	if len(left) == 0 {
		return 1
	}
	fmt.Fprintf(e.stderr, "railswitch %s left these on disk (nothing is deleted for you):\n", cmd)
	quoted := make([]string, len(left))
	for i, p := range left {
		fmt.Fprintf(e.stderr, "  %s\n", p)
		quoted[i] = shellQuote(p)
	}
	fmt.Fprintf(e.stderr, "remove them with: rm -rf %s\n", strings.Join(quoted, " "))
	return 1
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_./=:@%+,-]+$`)

// shellQuote quotes s for a POSIX shell when it needs it.
func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// gitHub reports whether repo is on github.com.
func gitHub(repo string) bool {
	h, ok := naming.GitHost(repo)
	return ok && h == "github.com"
}
