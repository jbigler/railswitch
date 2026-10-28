package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/workspace"
)

const agentAddUsage = "usage: railswitch agent add <name> [--use <ref>] [--run on-demand|service]"

// railswitch agent add <name> [--use <ref>] [--run …]: add an agent to the
// project as project new --agent does (D63): [agents.<name>], its
// package's secrets (declared, unset), the agent added to every git-host
// secret's services, stack.lock, and the missing agent Containerfiles.
// Nothing is written until the new project validates and resolves.
func cmdAgentAdd(ctx context.Context, e *env, args []string) int {
	fset := e.flags("agent add")
	use := fset.String("use", "", "the agent package `ref` (default: the built-in package named <name>)")
	run := fset.String("run", "", "how the agent runs: on-demand or service (default: the package's)")
	name, code, ok := parseNamed(e, fset, args, agentAddUsage)
	if !ok {
		return code
	}
	root, err := workspace.Root(e.dir)
	if err != nil {
		return e.fail(err)
	}
	proj := filepath.Join(root, "project.toml")
	p, err := config.Load(proj)
	if err != nil {
		return e.fail(err)
	}
	if _, ok := p.Agents[name]; ok {
		return e.fail(fmt.Errorf("agent %s is already in project.toml ([agents.%s])", name, name))
	}
	cat, err := pack.LoadCatalog(root)
	if err != nil {
		return e.fail(err)
	}
	if *use == "" {
		ref, ok := builtinAgent(cat, name)
		if !ok {
			return e.usageErr("agent add %s: no built-in agent package %s; pass --use <ref> (built-in agents: %s)", name, name, strings.Join(builtinAgentRefs(cat), ", "))
		}
		*use = ref.String()
	}
	a := config.Agent{Use: *use, Run: *run}
	cand := *p
	cand.Agents = maps.Clone(p.Agents)
	if cand.Agents == nil {
		cand.Agents = map[string]config.Agent{}
	}
	cand.Agents[name] = a
	if err := cand.Validate(); err != nil {
		return e.fail(fmt.Errorf("%s with [agents.%s]: %w", proj, name, err))
	}
	st, err := cat.Resolve(&cand)
	if err != nil {
		return e.fail(err)
	}
	i := slices.IndexFunc(st.Agents, func(c *pack.ChosenAgent) bool { return c.Name == name })
	chosen := st.Agents[i]
	plan := planSecrets(p, []*pack.ChosenAgent{chosen}, []string{chosen.Service})
	cand.Secrets = plan.apply(p.Secrets)
	if err := cand.Validate(); err != nil {
		return e.fail(fmt.Errorf("%s with [agents.%s] and its secrets: %w", proj, name, err))
	}
	if _, err := cat.Resolve(&cand); err != nil {
		return e.fail(err)
	}

	restore, err := keep(root, proj)
	if err != nil {
		return e.fail(err)
	}
	// project.toml goes back unless it is locked.
	failed := func(err error) int {
		if rerr := restore(); rerr != nil {
			err = errors.Join(err, fmt.Errorf("restoring %s: %w", proj, rerr))
		}
		return e.fail(err)
	}
	if err := config.AddAgent(proj, name, a); err != nil {
		return failed(err)
	}
	secrets, err := plan.write(proj)
	if err != nil {
		return failed(err)
	}
	if err := workspace.Lock(ctx, root, workspace.Accept{}); err != nil {
		return failed(err)
	}
	fmt.Fprintf(e.stdout, "added [agents.%s] use = %q to %s\n", name, a.Use, proj)
	for _, s := range secrets {
		fmt.Fprintf(e.stdout, "declared secret %s\n", s)
	}
	for _, x := range plan.extend {
		fmt.Fprintf(e.stdout, "secret %s now goes to %s too\n", x.Name, chosen.Service)
	}
	fmt.Fprintf(e.stdout, "wrote %s\n", filepath.Join(root, "stack.lock"))
	// The ejects copy what the lock accepted (an agent the lock remembers
	// is locked at that copy, not at its current content).
	w, err := workspace.Load(ctx, root, "")
	if err != nil {
		return e.fail(err)
	}
	cat, st = w.Catalog, w.Stack
	i = slices.IndexFunc(st.Agents, func(c *pack.ChosenAgent) bool { return c.Name == name })
	chosen = st.Agents[i]
	for _, n := range []string{"agent-base", cfName(chosen.Agent.Image.File)} {
		rel := workspaceFile(n)
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return e.fail(err)
		}
		if err := Eject(root, cat, st, n, false); err != nil {
			return e.fail(fmt.Errorf("eject %s: %w", n, err))
		}
		fmt.Fprintf(e.stdout, "wrote %s\n", filepath.Join(root, rel))
	}
	for _, s := range secrets {
		fmt.Fprintf(e.stdout, "next: railswitch secret set %s\n", s)
	}
	fmt.Fprintf(e.stdout, "next: railswitch agent %s\n", name)
	return 0
}

// builtinAgent is the built-in agent package named name at its highest
// version.
func builtinAgent(cat *pack.Catalog, name string) (pack.Ref, bool) {
	var best pack.Ref
	for r := range cat.Agents {
		if !r.Local && r.Name == name && r.Version > best.Version {
			best = r
		}
	}
	return best, best.Version > 0
}

// builtinAgentRefs is every built-in agent package's ref, sorted.
func builtinAgentRefs(cat *pack.Catalog) []string {
	var refs []string
	for r := range cat.Agents {
		if !r.Local {
			refs = append(refs, r.String())
		}
	}
	slices.Sort(refs)
	return refs
}
