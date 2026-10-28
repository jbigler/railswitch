package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/workspace"
)

// newDryRunDoc is project new --dry-run's JSON (docs/json.md).
type newDryRunDoc struct {
	ProjectTOML string      `json:"project_toml"`
	Worktree    dryWorktree `json:"worktree"`
	Warnings    []string    `json:"warnings"`
	Steps       []string    `json:"steps"`
	// Install is the Install review (D106) of the library packages the
	// project installs; nil when it installs none.
	Install *reviewDoc `json:"install"`
	// Template is the template the project starts from; nil without
	// --template.
	Template *dryTemplate `json:"template"`
}

// dryTemplate is the dry run's template: its ref, content hash and each
// question with the value the project gets (answer: the --answer given,
// else the default).
type dryTemplate struct {
	Ref    string      `json:"ref"`
	SHA256 string      `json:"sha256"`
	Ask    []dryAnswer `json:"ask"`
}

type dryAnswer struct {
	Key     string `json:"key"`
	Prompt  string `json:"prompt"`
	Default string `json:"default"`
	Answer  string `json:"answer"`
}

type dryWorktree struct {
	Slug       string         `json:"slug"`
	Routes     []dryRoute     `json:"routes"`
	Containers []dryContainer `json:"containers"`
	Env        []string       `json:"env"`
}

type dryRoute struct {
	Host    string `json:"host"`
	Service string `json:"service"`
}

type dryContainer struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

// newDryRun prints what project new would write and what its base worktree
// gets, and writes nothing in the workspace, the registry or the units.
func (e *env) newDryRun(ctx context.Context, pl *newPlan, up bool) int {
	doc, err := dryRunDoc(ctx, pl, up)
	if err != nil {
		return e.fail(err)
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(doc); err != nil {
			return e.fail(err)
		}
		return 0
	}
	w := doc.Worktree
	fmt.Fprintf(e.stdout, "# project.toml\n%s\n# worktree %s\n", doc.ProjectTOML, w.Slug)
	for _, r := range w.Routes {
		fmt.Fprintf(e.stdout, "route %s -> %s\n", r.Host, r.Service)
	}
	for _, c := range w.Containers {
		fmt.Fprintf(e.stdout, "container %s (%s)\n", c.Name, c.Role)
	}
	fmt.Fprintf(e.stdout, "env %s\n", strings.Join(w.Env, " "))
	for _, msg := range doc.Warnings {
		fmt.Fprintf(e.stdout, "warning: %s\n", msg)
	}
	for i, s := range doc.Steps {
		fmt.Fprintf(e.stdout, "%d. %s\n", i+1, s)
	}
	if t := doc.Template; t != nil {
		fmt.Fprintf(e.stdout, "template %s (sha256 %s)\n", safefile.Printable(t.Ref), t.SHA256)
		for _, a := range t.Ask {
			fmt.Fprintf(e.stdout, "answer %s = %s\n", a.Key, safefile.Printable(strconv.Quote(a.Answer)))
		}
	}
	if doc.Install != nil {
		fmt.Fprintf(e.stdout, "install review (create with --accept-install %s):\n", doc.Install.Hash)
		printReview(e.stdout, doc.Install)
	}
	return 0
}

// newInstall is the Install review of what project new writes for pl
// (D106), worked out in a temp dir as the dry run does; nil when it
// installs no library package.
func newInstall(ctx context.Context, pl *newPlan) (*reviewDoc, []pack.Ref, error) {
	tmp, err := os.MkdirTemp("", "railswitch-new-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(tmp)
	if _, err := writeProject(tmp, pl); err != nil {
		return nil, nil, err
	}
	p, err := config.Load(filepath.Join(tmp, "project.toml"))
	if err != nil {
		return nil, nil, err
	}
	return installReview(ctx, tmp, p, pl.cat, pl.libTemplate)
}

// dryRunDoc writes pl's files into a temp dir with the real run's
// writeProject, then renders them in memory: the base worktree as the only
// worktree, on a registry entry that is never saved. The temp dir is
// removed; the rendered paths that name it are not reported.
func dryRunDoc(ctx context.Context, pl *newPlan, up bool) (*newDryRunDoc, error) {
	tmp, err := os.MkdirTemp("", "railswitch-new-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if _, err := writeProject(tmp, pl); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(tmp, "project.toml"))
	if err != nil {
		return nil, err
	}
	p, err := config.Parse(data)
	if err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("project.toml: %w", err)
	}
	install, _, err := installReview(ctx, tmp, p, pl.cat, pl.libTemplate)
	if err != nil {
		return nil, err
	}
	// Placeholders: a real run generates the passwords, and only names are shown.
	if err := writeGenerated(tmp, pl.st, func() string { return "dry-run" }); err != nil {
		return nil, err
	}
	w, err := (&workspace.Workspace{Root: tmp, Catalog: pl.cat, Lock: &config.Lock{}, Dirs: pl.dirs}).WithProject(p)
	if err != nil {
		return nil, err
	}
	prefix, slug := p.Project.Prefix, pl.slug
	reg := *pl.reg
	reg.Projects, reg.Worktrees = slices.Clone(reg.Projects), slices.Clone(reg.Worktrees)
	if _, err := reg.AddProject(prefix, tmp, nil); err != nil {
		return nil, err
	}
	if _, err := reg.AddWorktree(prefix, slug, nil); err != nil {
		return nil, err
	}
	m, err := w.Model(&reg, workspace.LocalHost())
	if err != nil {
		return nil, err
	}
	if _, err := m.Plan(); err != nil {
		return nil, err
	}
	in, err := m.Instance(m.Inputs.Worktrees[0])
	if err != nil {
		return nil, err
	}
	routes, err := render.Routes(in)
	if err != nil {
		return nil, err
	}

	doc := &newDryRunDoc{
		ProjectTOML: string(data),
		Worktree:    dryWorktree{Slug: slug, Routes: []dryRoute{}, Containers: []dryContainer{}, Env: []string{}},
		Warnings:    append(slices.Clone(m.Warnings), in.Warnings...),
		Install:     install,
		Steps: []string{
			fmt.Sprintf("Clone %s (%s) into %s", p.Project.Repo, p.Project.BaseBranch, filepath.Join(pl.ws, slug)),
			"Write project.toml and stack.lock, generate local passwords",
			fmt.Sprintf("Eject %d Containerfiles", len(ejectNames(pl.st))),
			"Register, lock and render",
		},
	}
	if doc.Warnings == nil {
		doc.Warnings = []string{}
	}
	if t := pl.tpl; t != nil {
		doc.Template = &dryTemplate{Ref: p.Template.Ref, SHA256: p.Template.SHA256, Ask: []dryAnswer{}}
		for _, a := range t.Ask {
			v, ok := pl.answers[a.Key]
			if !ok {
				v = a.Default
			}
			doc.Template.Ask = append(doc.Template.Ask, dryAnswer{Key: a.Key, Prompt: a.Prompt, Default: a.Default, Answer: v})
		}
		if n := len(t.Containerfiles); n > 0 {
			doc.Steps = slices.Insert(doc.Steps, 3, fmt.Sprintf("Write the template's %d Containerfiles", n))
		}
		for _, b := range pl.bundles {
			doc.Steps = slices.Insert(doc.Steps, 1, fmt.Sprintf("Write the template's bundled package %s/ as this workspace's own", b.Dir))
		}
	}
	if up {
		doc.Steps = append(doc.Steps, fmt.Sprintf("Start %s (up)", slug))
	}
	for _, r := range routes {
		doc.Worktree.Routes = append(doc.Worktree.Routes, dryRoute{Host: r.Host, Service: r.Service})
	}
	for _, s := range m.Services {
		doc.Worktree.Containers = append(doc.Worktree.Containers, dryContainer{Name: naming.Container(prefix, slug, s.ID), Role: s.Role})
	}
	for _, v := range in.Env[p.Stack.App] {
		doc.Worktree.Env = append(doc.Worktree.Env, v.Name)
	}
	slices.Sort(doc.Worktree.Env)
	doc.Worktree.Env = slices.Compact(doc.Worktree.Env)
	return doc, nil
}
