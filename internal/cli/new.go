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
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/podman"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/workspace"
	"github.com/jbigler/railswitch/internal/worktree"
)

const newUsage = "usage: railswitch project new <dir> --repo <url> --prefix <p> [--name <n>] [--branch <base>] [--pack rails@1] [--option slot=ref]... [--tool nvim] [--agent name=ref]... | [--agent none]"

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
// stack builds, generate the local passwords, lock, register and render.
func cmdNew(ctx context.Context, e *env, args []string) int {
	fset := e.flags("project new")
	repo := fset.String("repo", "", "the app's git `url`")
	prefix := fset.String("prefix", "", "the project `prefix`: unique on this machine, names every object")
	name := fset.String("name", "", "the project `name` (default: the dir's name)")
	branch := fset.String("branch", "", "the `base` branch (default: the remote's HEAD branch)")
	packRef := fset.String("pack", "rails@1", "the stack's pack `ref`")
	var opts, tools, agentFlags stringList
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
	ws, err := filepath.Abs(dir)
	if err != nil {
		return e.fail(err)
	}
	if *name == "" {
		*name = filepath.Base(ws)
	}
	switch _, err := os.Lstat(filepath.Join(ws, "project.toml")); {
	case err == nil:
		return e.fail(fmt.Errorf("%s already exists: run railswitch project init there to join that project", filepath.Join(ws, "project.toml")))
	case !errors.Is(err, fs.ErrNotExist):
		return e.fail(err)
	}

	cat, err := pack.LoadCatalog(ws)
	if err != nil {
		return e.fail(err)
	}
	if agentFlags == nil {
		if ref, ok := cat.DefaultAgent(); ok {
			agents = map[string]string{ref.Name: ref.String()}
		}
	}
	p, st, err := newProject(cat, *packRef, *name, *prefix, *repo, opts, nvim, agents)
	if err != nil {
		return e.fail(err)
	}
	dirs, err := machine.DefaultDirs()
	if err != nil {
		return e.fail(err)
	}
	if err := prefixFree(dirs, *prefix); err != nil {
		return e.fail(err)
	}
	base := *branch
	if base == "" {
		if base, err = remoteHead(ctx, e.run, *repo); err != nil {
			return e.fail(err)
		}
	}
	if err := gitArg("--branch", base); err != nil {
		return e.fail(err)
	}
	slug, err := naming.Sanitize(base)
	if err != nil {
		return e.fail(fmt.Errorf("base branch %q: %w", base, err))
	}
	p.Project.BaseBranch = base

	before, err := entries(ws)
	if err != nil {
		return e.fail(err)
	}
	if err := clone(ctx, e.run, *repo, base, filepath.Join(ws, slug)); err != nil {
		return e.fail(err)
	}
	secrets, id, err := e.createWorkspace(ctx, dirs, ws, slug, p, cat, st)
	if err != nil {
		return e.failLeft("project new", err, ws, before)
	}
	fmt.Fprintf(e.stdout, "created workspace %s: project %s, base worktree %s (ID %d)\n", ws, *prefix, slug, id)
	fmt.Fprintf(e.stdout, "the workspace is a git repo: commit project.toml, stack.lock and containerfiles/ (git -C %s add -A && git -C %s commit)\n", shellQuote(ws), shellQuote(ws))
	e.printNext(ws, slug, secrets)
	return 0
}

// createWorkspace is new after the clone: everything up to the render.
// It returns the declared secrets (all unset) and the base worktree's ID.
func (e *env) createWorkspace(ctx context.Context, dirs machine.Dirs, ws, slug string, p *config.Project, cat *pack.Catalog, st *pack.Stack) ([]string, int, error) {
	if _, err := e.run.Run(ctx, sys.Cmd{Name: "git", Args: []string{"init", ws}}); err != nil {
		return nil, 0, fmt.Errorf("git init %s: %w", ws, err)
	}
	if err := createFile(filepath.Join(ws, ".gitignore"), []byte(workspaceGitignore), 0o644); err != nil {
		return nil, 0, err
	}
	if err := prepare(ws, slug, st); err != nil {
		return nil, 0, err
	}
	proj := filepath.Join(ws, "project.toml")
	if err := createFile(proj, projectTOML(p), 0o644); err != nil {
		return nil, 0, err
	}
	for _, n := range ejectNames(st) {
		if err := Eject(ws, cat, st, n, false); err != nil {
			return nil, 0, fmt.Errorf("eject %s: %w", n, err)
		}
	}
	secrets, err := addNewSecrets(proj, p, st)
	if err != nil {
		return nil, 0, err
	}
	if err := writeGenerated(ws, st); err != nil {
		return nil, 0, err
	}
	if err := workspace.Lock(ctx, ws); err != nil {
		return nil, 0, err
	}
	w, err := workspace.Load(ctx, ws, "")
	if err != nil {
		return nil, 0, err
	}
	id, err := e.registerAndRender(ctx, dirs, w, slug)
	return secrets, id, err
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
		if err := gitArg(a[0], a[1]); err != nil {
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
		err = writeGenerated(root, w.Stack)
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

// newProject is the project.toml new writes, before [image] and secrets:
// the pack's defaults overlaid by opts (slot=ref), the chosen options'
// version pins and agents (name → package ref). It is validated as it will
// be once base is ejected.
func newProject(cat *pack.Catalog, packRef, name, prefix, repo string, opts []string, nvim *config.NvimTool, agents map[string]string) (*config.Project, *pack.Stack, error) {
	ref, err := pack.ParseRef(packRef)
	if err != nil {
		return nil, nil, fmt.Errorf("--pack: %w", err)
	}
	pk := cat.Packs[ref]
	if pk == nil {
		return nil, nil, fmt.Errorf("--pack: unknown pack %s", ref)
	}
	if err := gitArg("--repo", repo); err != nil {
		return nil, nil, err
	}
	p := &config.Project{
		Schema:  config.SchemaVersion,
		Project: config.Meta{Name: name, Prefix: prefix, Repo: repo},
		Stack:   config.StackRef{Pack: ref.String(), App: pk.App.Service},
		Options: maps.Clone(pk.Defaults),
		Tools:   config.Tools{Nvim: nvim},
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
		slot, val, ok := strings.Cut(o, "=")
		if !ok || !slices.Contains(config.Slots, slot) {
			return nil, nil, fmt.Errorf("--option %q is not slot=ref (slots: %s)", o, strings.Join(config.Slots, ", "))
		}
		p.Options[slot] = val
	}
	st, err := cat.Resolve(p)
	if err != nil {
		return nil, nil, err
	}
	for _, slot := range config.Slots {
		if ch := st.Options[slot]; ch != nil && ch.Option.Pin != nil {
			if p.Versions == nil {
				p.Versions = map[string]string{}
			}
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

// projectTOML renders p's schema, [project], [stack], [options],
// [versions], [tools] and [agents.<name>].
func projectTOML(p *config.Project) []byte {
	var b strings.Builder
	kv := func(k, v string) { fmt.Fprintf(&b, "%s = %s\n", k, config.QuoteString(v)) }
	fmt.Fprintf(&b, "schema = %d\n\n[project]\n", p.Schema)
	kv("name", p.Project.Name)
	kv("prefix", p.Project.Prefix)
	kv("repo", p.Project.Repo)
	kv("base_branch", p.Project.BaseBranch)
	b.WriteString("\n[stack]\n")
	kv("pack", p.Stack.Pack)
	kv("app", p.Stack.App)
	b.WriteString("\n[options]                  # one per slot; \"none\" turns a slot off\n")
	for _, slot := range config.Slots {
		if v, ok := p.Options[slot]; ok {
			kv(slot, v)
		}
	}
	if len(p.Versions) > 0 {
		b.WriteString("\n[versions]                 # the options' image pins\n")
		for _, k := range slices.Sorted(maps.Keys(p.Versions)) {
			kv(k, p.Versions[k])
		}
	}
	if p.Tools.Nvim != nil {
		b.WriteString("\n[tools.nvim]               # config_repo = \"<git url>\": cloned into the empty config volume (D54)\n")
	}
	for _, n := range slices.Sorted(maps.Keys(p.Agents)) {
		fmt.Fprintf(&b, "\n[agents.%s]\n", n)
		kv("use", p.Agents[n].Use)
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
	for _, slot := range config.Slots {
		if ch := st.Options[slot]; ch != nil && ch.Option.AppAddon != nil {
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
	for _, slot := range config.Slots {
		if ch := st.Options[slot]; ch != nil && ch.Option.Image.Build != "" {
			names = append(names, cfName(ch.Option.Image.Build))
		}
	}
	return names
}

// prefixFree refuses a prefix registered on this machine.
func prefixFree(dirs machine.Dirs, prefix string) error {
	reg, err := machine.Open(dirs.Data)
	if err != nil {
		return err
	}
	defer reg.Close()
	if pe, ok := reg.Project(prefix); ok {
		return prefixTaken(prefix, pe.Workspace)
	}
	return nil
}

func prefixTaken(prefix, ws string) error {
	return fmt.Errorf("prefix %q is already used by %s", prefix, ws)
}

// gitArg refuses a value git would read as an option, or one holding a
// line break.
func gitArg(what, v string) error {
	if v == "" || strings.HasPrefix(v, "-") || strings.ContainsAny(v, "\r\n\x00") {
		return fmt.Errorf("%s %q is not a usable git value", what, v)
	}
	return nil
}

// remoteHead is the branch the remote's HEAD names, from
// `git ls-remote --symref <repo> HEAD`.
func remoteHead(ctx context.Context, r sys.Runner, repo string) (string, error) {
	if err := gitArg("--repo", repo); err != nil {
		return "", err
	}
	env, err := sys.GitNetEnv(ctx, r, os.Environ())
	if err != nil {
		return "", err
	}
	res, err := r.Run(ctx, sys.Cmd{Name: "git", Args: []string{"ls-remote", "--symref", repo, "HEAD"}, Env: env, Timeout: gitRemoteTimeout})
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
	_, err = r.Run(ctx, sys.Cmd{Name: "git", Args: []string{"clone", "--branch", base, repo, dir}, Env: env, Timeout: gitCloneTimeout})
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

// writeGenerated writes <ws>/mise.local.toml (0600) with a fresh random
// value for each generated name of the chosen options (D17). The values
// are never printed.
func writeGenerated(ws string, st *pack.Stack) error {
	var names []string
	for _, ch := range st.Options {
		names = append(names, ch.Option.Generated...)
	}
	slices.Sort(names)
	var b strings.Builder
	b.WriteString("# Local values railswitch generated for the services (D17). Never commit this file.\n[env]\n")
	for _, n := range slices.Compact(names) {
		buf := make([]byte, 16)
		rand.Read(buf)
		fmt.Fprintf(&b, "%s = %s\n", n, config.QuoteString(hex.EncodeToString(buf)))
	}
	return createFile(filepath.Join(ws, "mise.local.toml"), []byte(b.String()), 0o600)
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
	if _, err := w.Render(ctx, e.run, false); err != nil {
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
