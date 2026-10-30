package pack

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing/fstest"

	"github.com/BurntSushi/toml"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/pack/builtin"
	"github.com/jbigler/railswitch/internal/placeholder"
	"github.com/jbigler/railswitch/internal/version"
)

// Catalog is every package available to a project: the built-ins, plus the
// workspace-local packages (Ref.Local) and library packages (Ref.Library)
// loaded so far. A local or library package is read on demand, the first
// time its ref is looked up (D43), so a broken package no ref names breaks
// nothing.
type Catalog struct {
	Packs   map[Ref]*Pack
	Options map[Ref]*Option
	Tools   map[string]*Tool
	Agents  map[Ref]*Agent
	// AgentBase is the agent base image's directory (AgentBaseFile);
	// nil when the catalog has none.
	AgentBase fs.FS
	Root      string // the workspace local refs read from; "" for none
	// Libraries finds the verified checkouts library refs read from; nil
	// for none (a library ref then doesn't resolve).
	Libraries Libraries

	// overlay holds WithFile's packages, by package directory
	// ("options/<name>"): read from memory instead of Root.
	overlay map[string]fs.FS
}

// WithFile is a copy of c whose local package holding rel, a
// workspace-relative slash path "packs/<name>/<file>" or
// "options/<name>/<file>", is read from memory with rel set to data (nil:
// removed), so a change loads and renders before it is written. The
// package's files are read from disk once, now; the copy loads every local
// package again.
func (c *Catalog) WithFile(rel string, data []byte) (*Catalog, error) {
	top, rest, _ := strings.Cut(rel, "/")
	name, file, _ := strings.Cut(rest, "/")
	if c.Root == "" || (top != "packs" && top != "options" && top != "agents") || name == "" || !fs.ValidPath(rel) || file == "" {
		return nil, fmt.Errorf("%s is not a file of a workspace-local pack, option or agent", rel)
	}
	dir := top + "/" + name
	mem := fstest.MapFS{}
	err := fs.WalkDir(os.DirFS(c.Root), dir, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir():
			return nil
		case !d.Type().IsRegular():
			return fmt.Errorf("%s: not a regular file", p)
		}
		b, err := fs.ReadFile(os.DirFS(c.Root), p)
		if err != nil {
			return err
		}
		mem[p] = &fstest.MapFile{Data: b, Mode: 0o644}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if data == nil {
		delete(mem, rel)
	} else {
		mem[rel] = &fstest.MapFile{Data: data, Mode: 0o644}
	}
	n := &Catalog{Packs: builtinOnly(c.Packs), Options: builtinOnly(c.Options), Tools: c.Tools, Agents: builtinOnly(c.Agents), AgentBase: c.AgentBase, Root: c.Root, Libraries: c.Libraries, overlay: maps.Clone(c.overlay)}
	if n.overlay == nil {
		n.overlay = map[string]fs.FS{}
	}
	n.overlay[dir] = mem
	return n, nil
}

// builtinOnly is m without its local packages.
func builtinOnly[T any](m map[Ref]T) map[Ref]T {
	out := map[Ref]T{}
	for r, v := range m {
		if !r.Local {
			out[r] = v
		}
	}
	return out
}

// LoadBuiltins loads the packages embedded in railswitch.
func LoadBuiltins() (*Catalog, error) { return Load(builtin.FS) }

// LoadCatalog is a workspace's catalog: the built-ins, with local refs read
// from root/packs/, root/options/ and root/agents/ on demand. Every place
// that resolves a workspace's stack loads it through here; libs finds the
// library checkouts library refs read from (nil: none).
func LoadCatalog(root string, libs Libraries) (*Catalog, error) {
	c, err := LoadBuiltins()
	if err != nil {
		return nil, err
	}
	c.Root, c.Libraries = root, libs
	return c, nil
}

// Pack is the pack r names: a built-in (nil when there is none), for a
// local ref root/packs/<name>/, for a library ref its checkout's
// packs/<name>/ (Libraries); loaded once.
func (c *Catalog) Pack(r Ref) (*Pack, error) {
	if p, ok := c.Packs[r]; ok || builtinRef(r) {
		return p, nil
	}
	l, dir, origin, err := c.open(r, KindPack)
	if err != nil {
		return nil, err
	}
	p, err := l.pack(dir, r)
	if err != nil {
		return nil, err
	}
	p.Origin = origin
	c.Packs[r] = p
	return p, nil
}

// Option is the option r names, as Pack.
func (c *Catalog) Option(r Ref) (*Option, error) {
	if o, ok := c.Options[r]; ok || builtinRef(r) {
		return o, nil
	}
	l, dir, origin, err := c.open(r, KindOption)
	if err != nil {
		return nil, err
	}
	o, err := l.option(dir, r)
	if err != nil {
		return nil, err
	}
	o.Origin = origin
	c.Options[r] = o
	return o, nil
}

// Agent is the agent package r names, as Pack.
func (c *Catalog) Agent(r Ref) (*Agent, error) {
	if a, ok := c.Agents[r]; ok || builtinRef(r) {
		return a, nil
	}
	l, dir, origin, err := c.open(r, KindAgent)
	if err != nil {
		return nil, err
	}
	a, err := l.agent(dir, r)
	if err != nil {
		return nil, err
	}
	a.Origin = origin
	c.Agents[r] = a
	return a, nil
}

func builtinRef(r Ref) bool { return !r.Local && r.Library == "" }

// open is a loader over the local or library package of kind r names, and
// its directory there; origin is set for a library package.
func (c *Catalog) open(r Ref, kind string) (*loader, string, *LibraryOrigin, error) {
	if r.Library != "" {
		return c.library(r, kind)
	}
	l, dir, err := c.local(r, kindDirs[kind])
	return l, dir, nil, err
}

// pack, option and agent load the one package of their kind at dir as r:
// its manifest checked, its version r's.
func (l *loader) pack(dir string, r Ref) (*Pack, error) {
	var p Pack
	if l.decode(dir, "pack.toml", &p) {
		p.Files = l.sub(dir)
		if l.checkPack(dir, &p) {
			l.version(dir+"/pack.toml", p.Version, r)
		}
	}
	return &p, errors.Join(l.errs...)
}

func (l *loader) option(dir string, r Ref) (*Option, error) {
	var o Option
	if l.decode(dir, "option.toml", &o) {
		o.Files = l.sub(dir)
		if l.checkOption(dir, &o) {
			l.version(dir+"/option.toml", o.Version, r)
		}
	}
	return &o, errors.Join(l.errs...)
}

func (l *loader) agent(dir string, r Ref) (*Agent, error) {
	var a Agent
	if l.decode(dir, "agent.toml", &a) {
		a.Files = l.sub(dir)
		if l.checkAgent(dir, &a) {
			l.version(dir+"/agent.toml", a.Version, r)
		}
	}
	return &a, errors.Join(l.errs...)
}

// tool loads the tool at dir, its manifest checked.
func (l *loader) tool(dir string) (*Tool, error) {
	var t Tool
	if l.decode(dir, "tool.toml", &t) {
		t.Files = l.sub(dir)
		if t.Service == "" {
			t.Service = t.Name
		}
		l.checkTool(dir, &t)
	}
	return &t, errors.Join(l.errs...)
}

// Package kinds, as stack.lock's tables and WithPackage name them. A tool
// (KindTool, by name) and the agent base image (KindBase, AgentBaseRef)
// are built in only, and carry no version (D107). A template
// (KindTemplate, D108) is no catalog package: a library's index lists it
// and LibraryTree reads it.
const (
	KindPack     = "pack"
	KindOption   = "option"
	KindAgent    = "agent"
	KindTool     = "tool"
	KindBase     = "base"
	KindTemplate = "template"
)

// AgentBaseRef names the agent base image's files (Catalog.AgentBase) as
// a package: kind KindBase.
var AgentBaseRef = Ref{Name: "agent-base"}

// AgentBaseFile is the agent base image's Containerfile in AgentBase.
const AgentBaseFile = "Containerfile.agent-base"

// WithPackage is a copy of c with files, loaded and checked as the package
// of kind at r, in place of the one r names: a built-in's, a local or a
// library package's accepted content (D97); for KindTool the tool r.Name,
// for KindBase the agent base image. origin, set for a library package,
// WithoutFiles is a copy of c in which the package of kind at r keeps its
// manifest but its files can't be read: every read fails with err. It is
// for a package whose content changed and whose accepted copy is gone, so
// copying its files out (an eject) fails instead of handing on unreviewed
// text, while the rest of the catalog serves as before.
func (c *Catalog) WithoutFiles(kind string, r Ref, err error) *Catalog {
	n := &Catalog{Packs: maps.Clone(c.Packs), Options: maps.Clone(c.Options), Tools: maps.Clone(c.Tools), Agents: maps.Clone(c.Agents), AgentBase: c.AgentBase, Root: c.Root, Libraries: c.Libraries, overlay: c.overlay}
	off := unreadable{err}
	switch kind {
	case KindPack:
		if p := n.Packs[r]; p != nil {
			cp := *p
			cp.Files = off
			n.Packs[r] = &cp
		}
	case KindOption:
		if o := n.Options[r]; o != nil {
			cp := *o
			cp.Files = off
			n.Options[r] = &cp
		}
	case KindAgent:
		if a := n.Agents[r]; a != nil {
			cp := *a
			cp.Files = off
			n.Agents[r] = &cp
		}
	case KindTool:
		if t := n.Tools[r.Name]; t != nil {
			cp := *t
			cp.Files = off
			n.Tools[r.Name] = &cp
		}
	case KindBase:
		n.AgentBase = off
	}
	return n
}

// unreadable is a file system every read of which fails with err.
type unreadable struct{ err error }

func (u unreadable) Open(string) (fs.File, error) { return nil, u.err }

// is the library it was accepted from (stack.lock's entry).
func (c *Catalog) WithPackage(kind string, r Ref, files fs.FS, origin *LibraryOrigin) (*Catalog, error) {
	top := kindDirs[kind]
	if top == "" {
		return nil, fmt.Errorf("%s: unknown package kind %q", r, kind)
	}
	dir := top + "/" + r.Name
	if kind == KindBase {
		dir = top
	}
	mem := fstest.MapFS{}
	err := fs.WalkDir(files, ".", func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil || d.IsDir():
			return err
		case !d.Type().IsRegular():
			return fmt.Errorf("%s: not a regular file", p)
		}
		b, err := fs.ReadFile(files, p)
		mem[dir+"/"+p] = &fstest.MapFile{Data: b, Mode: 0o644}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", r, err)
	}
	l := &loader{fsys: mem, bases: map[int]string{}, names: map[string]string{}}
	n := &Catalog{Packs: maps.Clone(c.Packs), Options: maps.Clone(c.Options), Tools: maps.Clone(c.Tools), Agents: maps.Clone(c.Agents), AgentBase: c.AgentBase, Root: c.Root, Libraries: c.Libraries, overlay: c.overlay}
	switch kind {
	case KindPack:
		var p *Pack
		if p, err = l.pack(dir, r); err == nil {
			p.Origin = origin
		}
		n.Packs[r] = p
	case KindOption:
		var o *Option
		if o, err = l.option(dir, r); err == nil {
			o.Origin = origin
		}
		n.Options[r] = o
	case KindAgent:
		var a *Agent
		if a, err = l.agent(dir, r); err == nil {
			a.Origin = origin
		}
		n.Agents[r] = a
	case KindTool:
		var t *Tool
		if t, err = l.tool(dir); err == nil && t.Name != r.Name {
			err = fmt.Errorf("%s/tool.toml: name %q is not %s", dir, t.Name, r.Name)
		}
		n.Tools[r.Name] = t
	case KindBase:
		if _, err = fs.Stat(mem, dir+"/"+AgentBaseFile); err == nil {
			n.AgentBase = l.sub(dir)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", r, err)
	}
	return n, nil

}

// local is a loader over the workspace and the package directory of the
// local ref r under top ("packs", "options", "agents"). Its errors name
// files relative to the workspace (packs/x/pack.toml).
func (c *Catalog) local(r Ref, top string) (*loader, string, error) {
	if c.Root == "" {
		return nil, "", fmt.Errorf("%s: local packages need a workspace", r)
	}
	dir := top + "/" + r.Name
	fi, err := os.Stat(filepath.Join(c.Root, top, r.Name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", fmt.Errorf("%s: %s does not exist", r, filepath.Join(c.Root, top, r.Name))
	}
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", r, err)
	}
	if !fi.IsDir() {
		return nil, "", fmt.Errorf("%s: %s is not a directory", r, filepath.Join(c.Root, top, r.Name))
	}
	fsys, ok := c.overlay[dir]
	if !ok {
		fsys = os.DirFS(c.Root)
	}
	return &loader{fsys: fsys, bases: map[int]string{}, names: map[string]string{}}, dir, nil
}

// version checks a local package's version against the ref that names it.
func (l *loader) version(file string, v int, r Ref) {
	if v != r.Version {
		l.add(file, "version %d does not match the ref %s", v, r)
	}
}

// Load reads packs/<name>/pack.toml, options/<name>/option.toml,
// tools/<name>/tool.toml and agents/<name>/agent.toml from fsys. A missing
// top-level directory is empty.
// Every problem found is returned at once (errors.Join), each naming its file.
func Load(fsys fs.FS) (*Catalog, error) {
	c := &Catalog{Packs: map[Ref]*Pack{}, Options: map[Ref]*Option{}, Tools: map[string]*Tool{}, Agents: map[Ref]*Agent{}}
	l := &loader{fsys: fsys, bases: map[int]string{}, names: map[string]string{}}

	for _, dir := range l.dirs("packs") {
		var p Pack
		if !l.decode(dir, "pack.toml", &p) {
			continue
		}
		p.Files = l.sub(dir)
		if l.checkPack(dir, &p) {
			c.Packs[Ref{Name: p.Name, Version: p.Version}] = &p
		}
	}
	for _, dir := range l.dirs("options") {
		var o Option
		if !l.decode(dir, "option.toml", &o) {
			continue
		}
		o.Files = l.sub(dir)
		if l.checkOption(dir, &o) {
			c.Options[Ref{Name: o.Name, Version: o.Version}] = &o
		}
	}
	for _, dir := range l.dirs("tools") {
		var t Tool
		if !l.decode(dir, "tool.toml", &t) {
			continue
		}
		t.Files = l.sub(dir)
		if t.Service == "" {
			t.Service = t.Name
		}
		if l.checkTool(dir, &t) {
			c.Tools[t.Name] = &t
		}
	}
	for _, dir := range l.dirs("agents") {
		var a Agent
		if !l.decode(dir, "agent.toml", &a) {
			continue
		}
		a.Files = l.sub(dir)
		if l.checkAgent(dir, &a) {
			c.Agents[Ref{Name: a.Name, Version: a.Version}] = &a
		}
	}
	if fi, err := fs.Stat(fsys, kindDirs[KindBase]); err == nil && fi.IsDir() {
		c.AgentBase = l.sub(kindDirs[KindBase])
	}
	var defaults []string
	for r, a := range c.Agents {
		if a.Default {
			defaults = append(defaults, r.String())
		}
	}
	if len(defaults) > 1 {
		slices.Sort(defaults)
		l.errs = append(l.errs, fmt.Errorf("agents: %s all set default = true; at most one may", strings.Join(defaults, ", ")))
	}
	if err := errors.Join(l.errs...); err != nil {
		return nil, err
	}
	return c, nil
}

// DefaultAgent is the built-in agent package that sets default = true.
func (c *Catalog) DefaultAgent() (Ref, bool) {
	for r, a := range c.Agents {
		if a.Default && !r.Local {
			return r, true
		}
	}
	return Ref{}, false
}

var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type loader struct {
	fsys  fs.FS
	errs  []error
	bases map[int]string    // port base → "<kind> <name>" that declares it
	names map[string]string // port name → "<kind> <name>" that declares it
}

func (l *loader) add(file, format string, args ...any) {
	l.errs = append(l.errs, fmt.Errorf("%s: %s", file, fmt.Sprintf(format, args...)))
}

// dirs lists the package directories under top, sorted.
func (l *loader) dirs(top string) []string {
	entries, err := fs.ReadDir(l.fsys, top)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		l.errs = append(l.errs, err)
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			l.add(path.Join(top, e.Name()), "not a package directory")
			continue
		}
		out = append(out, path.Join(top, e.Name()))
	}
	return out
}

func (l *loader) sub(dir string) fs.FS {
	s, err := fs.Sub(l.fsys, dir)
	if err != nil { // only for an invalid path, which dirs never yields
		panic(err)
	}
	return s
}

// decode strictly decodes dir/name into v.
func (l *loader) decode(dir, name string, v any) bool {
	file := path.Join(dir, name)
	data, err := fs.ReadFile(l.fsys, file)
	if err != nil {
		l.errs = append(l.errs, err)
		return false
	}
	md, err := toml.Decode(string(data), v)
	if err != nil {
		l.add(file, "%v", err)
		return false
	}
	if und := md.Undecoded(); len(und) > 0 {
		for _, k := range und {
			l.add(file, "unknown key %s", k)
		}
		return false
	}
	return true
}

// head checks name and version against the directory; it reports whether
// they are usable as a catalog key.
func (l *loader) head(file, dir, name string, version int, versioned bool) bool {
	ok := true
	if !nameRe.MatchString(name) {
		l.add(file, "name %q must match %s", name, nameRe)
		ok = false
	} else if path.Base(dir) != name {
		l.add(file, "name %q does not match its directory %s", name, dir)
		ok = false
	}
	if versioned && version < 1 {
		l.add(file, "version must be a positive integer")
		ok = false
	}
	return ok
}

// file checks that rel names a regular file in the package directory dir.
func (l *loader) file(file, field, dir, rel string) {
	if rel == "" {
		return
	}
	if !fs.ValidPath(rel) {
		l.add(file, "%s: %q is not a relative path inside the package", field, rel)
		return
	}
	fi, err := fs.Stat(l.fsys, path.Join(dir, rel))
	if err != nil {
		l.add(file, "%s: %s is missing from %s", field, rel, dir)
		return
	}
	if !fi.Mode().IsRegular() {
		l.add(file, "%s: %s is not a regular file", field, rel)
	}
}

func (l *loader) required(file, field, v string) {
	if v == "" {
		l.add(file, "%s is required", field)
	}
}

func (l *loader) ports(file, owner string, ports []Port) {
	for i, p := range ports {
		f := fmt.Sprintf("ports[%d]", i)
		if !nameRe.MatchString(p.Name) {
			l.add(file, "%s.name %q must match %s", f, p.Name, nameRe)
		}
		if !envNameRe.MatchString(p.Env) {
			l.add(file, "%s.env %q is not an env var name", f, p.Env)
		}
		if p.Base < 1024 || p.Base > 65535 {
			l.add(file, "%s.base %d is not a port above 1023", f, p.Base)
		}
		switch {
		case p.SamePort && p.Container != 0:
			l.add(file, "%s: same_port and container are exclusive (same_port: the container port is the host port)", f)
		case !p.SamePort && (p.Container < 1 || p.Container > 65535):
			l.add(file, "%s.container %d is not a port", f, p.Container)
		}
		// Host port = base + worktree ID, so a base belongs to one package
		// name (its versions may share it).
		if prev, ok := l.bases[p.Base]; ok && prev != owner {
			l.add(file, "%s.base %d is already used by %s", f, p.Base, prev)
		} else {
			l.bases[p.Base] = owner
		}
		// The registry records bases by port name, so a name belongs to one
		// package name too, and occurs once in its file.
		if j := slices.IndexFunc(ports[:i], func(q Port) bool { return q.Name == p.Name }); j >= 0 {
			l.add(file, "%s.name %q is already used by ports[%d]", f, p.Name, j)
		} else if prev, ok := l.names[p.Name]; ok && prev != owner {
			l.add(file, "%s.name %q is already used by %s", f, p.Name, prev)
		} else {
			l.names[p.Name] = owner
		}
	}
}

func (l *loader) volumes(file, field string, vs []Volume, seeds bool) {
	for i, v := range vs {
		f := fmt.Sprintf("%s[%d]", field, i)
		l.required(file, f+".name", v.Name)
		if !strings.HasPrefix(v.Mount, "/") {
			l.add(file, "%s.mount %q is not an absolute container path", f, v.Mount)
		}
		if v.Data != "" && v.Data != DataPersistent && v.Data != DataCache {
			l.add(file, "%s.data must be \"persistent\" or \"cache\", not %q", f, v.Data)
		}
		if v.Seed != "" && !seeds {
			l.add(file, "%s.seed: only an option's [[volumes]] can be seeded", f)
		}
		if v.Seed != "" && (!fs.ValidPath(v.Seed) || strings.Contains(v.Seed, "/")) {
			l.add(file, "%s.seed %q must be a file name in db-dumps/", f, v.Seed)
		}
	}
}

func (l *loader) env(file, field string, m map[string]string) {
	for _, k := range sortedKeys(m) {
		if !envNameRe.MatchString(k) {
			l.add(file, "%s: %q is not an env var name", field, k)
		}
	}
}

func (l *loader) links(file string, links []Link) {
	for i, k := range links {
		l.required(file, fmt.Sprintf("links[%d].label", i), k.Label)
		l.required(file, fmt.Sprintf("links[%d].url", i), k.URL)
		for _, n := range placeholder.Names(k.URL) {
			if !placeholder.LinkNames[n] {
				l.add(file, "links[%d].url: unknown placeholder {%s} (allowed: {worktree.host}, {worktree.slug}, {branch})", i, n)
			}
		}
	}
}

func (l *loader) health(file string, h *Health) {
	if h != nil {
		l.required(file, "health.cmd", h.Cmd)
	}
}

func (l *loader) checkPack(dir string, p *Pack) bool {
	file := path.Join(dir, "pack.toml")
	n := len(l.errs)
	ok := l.head(file, dir, p.Name, p.Version, true)

	if !nameRe.MatchString(p.App.Service) {
		l.add(file, "app.service %q must match %s", p.App.Service, nameRe)
	}
	l.required(file, "app.server", p.App.Server)
	if p.App.Port < 1 || p.App.Port > 65535 {
		l.add(file, "app.port %d is not a port", p.App.Port)
	}
	l.file(file, "app.entrypoint", dir, p.App.Entrypoint)
	l.file(file, "app.deps_entrypoint", dir, p.App.DepsEntrypoint)
	if p.App.Install == "" && p.App.DepsEntrypoint == "" {
		l.add(file, "app.install or app.deps_entrypoint is required: the deps service would install nothing")
	}
	l.env(file, "deps.frozen_env", p.Deps.FrozenEnv)

	l.required(file, "image.file", p.Image.File)
	l.file(file, "image.file", dir, p.Image.File)
	l.required(file, "image.tag", p.Image.Tag)
	l.required(file, "image.os", p.Image.OS)

	// A runtime may have no fallback: an app without a version source then
	// gets an empty value (D69).
	for _, name := range sortedKeys(p.Runtimes) {
		if !nameRe.MatchString(name) {
			l.add(file, "runtimes.%s: name must match %s", name, nameRe)
		}
		l.sources(file, "runtimes."+name, p.Runtimes[name].From)
	}
	l.sources(file, "db_name", p.DBName.From)
	l.env(file, "env", p.Env)
	l.env(file, "share.env", p.Share.Env)
	l.volumes(file, "caches", p.Caches, false)
	l.volumes(file, "worktree_volumes", p.WorktreeVolumes, false)
	l.ports(file, "pack "+p.Name, p.Ports)
	for _, slot := range sortedKeys(p.Defaults) {
		if !slices.Contains(config.Slots, slot) {
			l.add(file, "defaults.%s: unknown slot (slots: %s)", slot, strings.Join(config.Slots, ", "))
		}
		if ref := p.Defaults[slot]; ref != "none" {
			if _, err := ParseRef(ref); err != nil {
				l.add(file, "defaults.%s: %v", slot, err)
			}
		}
	}
	l.links(file, p.Links)
	for i, f := range p.Detect.Files {
		l.detectPath(file, fmt.Sprintf("detect.files[%d]", i), f)
	}
	for _, name := range sortedKeys(p.Tasks) {
		if slices.Contains(config.ReservedTasks, name) {
			l.add(file, "tasks.%s: %q is a railswitch task subcommand, not a task name", name, name)
		}
	}
	return ok && len(l.errs) == n
}

func (l *loader) checkOption(dir string, o *Option) bool {
	file := path.Join(dir, "option.toml")
	n := len(l.errs)
	ok := l.head(file, dir, o.Name, o.Version, true)

	if !slices.Contains(config.Slots, o.Slot) {
		l.add(file, "slot %q is not one of %s", o.Slot, strings.Join(config.Slots, ", "))
	}
	if !nameRe.MatchString(o.Service) {
		l.add(file, "service %q must match %s", o.Service, nameRe)
	}
	for _, s := range o.Needs {
		if !slices.Contains(config.Slots, s) {
			l.add(file, "needs: %q is not a slot", s)
		}
	}
	for _, g := range o.Generated {
		if !envNameRe.MatchString(g) {
			l.add(file, "generated: %q is not an env var name", g)
		}
	}
	if o.Pin != nil {
		if !nameRe.MatchString(o.Pin.Name) {
			l.add(file, "pin.name %q must match %s", o.Pin.Name, nameRe)
		}
		l.required(file, "pin.default", o.Pin.Default)
	}

	img := o.Image
	kinds := 0
	for _, s := range []string{img.Pull, img.Use, img.Build} {
		if s != "" {
			kinds++
		}
	}
	switch {
	case kinds != 1:
		l.add(file, "image: set exactly one of pull, use or build")
	case img.Use != "" && img.Use != "app":
		l.add(file, "image.use: %q is not \"app\"", img.Use)
	case img.Build != "":
		l.file(file, "image.build", dir, img.Build)
		l.required(file, "image.tag", img.Tag)
	}
	if img.Build == "" && (img.Tag != "" || len(img.Args) > 0) {
		l.add(file, "image: tag and args go with build only")
	}

	if o.AppAddon != nil {
		l.required(file, "app_addon.file", o.AppAddon.File)
		l.file(file, "app_addon.file", dir, o.AppAddon.File)
		l.required(file, "app_addon.os", o.AppAddon.OS)
	}
	l.health(file, o.Health)
	l.file(file, "entrypoint", dir, o.Entrypoint)
	l.env(file, "gives", o.Gives)
	l.env(file, "deps_env", o.DepsEnv)
	l.env(file, "agent_gives", o.AgentGives)
	l.env(file, "env", o.Env)
	l.env(file, "share.gives", o.Share.Gives)
	if d := o.Dump; d != nil {
		if len(d.Command) == 0 || slices.Contains(d.Command, "") {
			l.add(file, "dump.command must be a non-empty list of non-empty arguments")
		}
		l.required(file, "dump.file", d.File)
		if d.File != "" && (strings.Contains(d.File, "/") || d.File == "." || d.File == "..") {
			l.add(file, "dump.file %q must be a bare file name", d.File)
		}
		ph := "{" + DumpDB + "}"
		perDB := strings.Contains(d.File, ph) && slices.ContainsFunc(d.Command, func(a string) bool { return strings.Contains(a, ph) })
		switch {
		case d.List != nil && (len(d.List) == 0 || slices.Contains(d.List, "")):
			l.add(file, "dump.list must be a non-empty list of non-empty arguments")
		case d.List != nil && !perDB:
			l.add(file, "dump.command and dump.file must both use %s: dump.list dumps each database it prints to its own file", ph)
		case d.List == nil && (strings.Contains(d.File, ph) || slices.ContainsFunc(d.Command, func(a string) bool { return strings.Contains(a, ph) })):
			l.add(file, "%s needs dump.list (the command that prints the database names)", ph)
		}
		for _, c := range []struct {
			key  string
			argv []string
		}{{"dump.prepare", d.Prepare}, {"dump.restore", d.Restore}} {
			if c.argv != nil && (len(c.argv) == 0 || slices.Contains(c.argv, "")) {
				l.add(file, "%s must be a non-empty list of non-empty arguments", c.key)
			}
		}
		if d.Prepare != nil && d.Restore == nil {
			l.add(file, "dump.prepare needs dump.restore")
		}
		if d.Restore != nil && o.Health == nil {
			// up restores into a new database once its unit reports healthy (D122).
			l.add(file, "dump.restore needs [health]: up restores a new database once it is healthy")
		}
		// Each file goes back into its own database: with a list, both
		// name it; without one, there is no name to give.
		for _, c := range []struct {
			key  string
			argv []string
		}{{"dump.prepare", d.Prepare}, {"dump.restore", d.Restore}} {
			uses := slices.ContainsFunc(c.argv, func(a string) bool { return strings.Contains(a, ph) })
			switch {
			case c.argv == nil:
			case d.List != nil && !uses:
				l.add(file, "%s must use %s: each dump goes back into its own database", c.key, ph)
			case d.List == nil && uses:
				l.add(file, "%s uses %s, which needs dump.list", c.key, ph)
			}
		}
	}
	l.volumes(file, "volumes", o.Volumes, true)
	l.mounts(file, dir, o.Mounts)
	l.ports(file, "option "+o.Name, o.Ports)
	for i, r := range o.Routes {
		f := fmt.Sprintf("routes[%d]", i)
		if !nameRe.MatchString(r.Name) {
			l.add(file, "%s.name %q must match %s", f, r.Name, nameRe)
		}
		l.required(file, f+".host", r.Host)
		if r.Port < 1 || r.Port > 65535 {
			l.add(file, "%s.port %d is not a port", f, r.Port)
		}
		if r.RootRedirect != "" && !strings.HasPrefix(r.RootRedirect, "/") {
			l.add(file, "%s.root_redirect %q must start with /", f, r.RootRedirect)
		}
	}
	l.links(file, o.Links)
	for i := range o.Detect {
		d := &o.Detect[i]
		f := fmt.Sprintf("detect[%d]", i)
		l.detectPath(file, f+".file", d.File)
		re, err := regexp.Compile(d.Match)
		switch {
		case d.Match == "":
			l.add(file, "%s.match is required", f)
		case err != nil:
			l.add(file, "%s.match: %v", f, err)
		default:
			d.re = re
		}
	}
	return ok && len(l.errs) == n
}

// detectPathRe is a detect path: project detect passes "/"+path to git
// sparse-checkout as a no-cone pattern, so it must hold no pattern syntax
// (glob, negation, comment, escape) or space.
var detectPathRe = regexp.MustCompile(`^[A-Za-z0-9._+-]+(/[A-Za-z0-9._+-]+)*$`)

// detectPath checks a detect rule's repository path: relative, clean,
// inside the repository.
func (l *loader) detectPath(file, field, p string) {
	if !fs.ValidPath(p) || p == "." || !detectPathRe.MatchString(p) {
		l.add(file, "%s: %q is not a clean relative path inside the repository (letters, digits, . _ + - and /)", field, p)
	}
}

func (l *loader) mounts(file, dir string, ms []Mount) {
	for i, m := range ms {
		f := fmt.Sprintf("mounts[%d]", i)
		l.required(file, f+".source", m.Source)
		if !strings.HasPrefix(m.Target, "/") {
			l.add(file, "%s.target %q is not an absolute container path", f, m.Target)
		}
		if m.Source != "" && !strings.HasPrefix(m.Source, "/") && !strings.HasPrefix(m.Source, "{") {
			l.file(file, f+".source", dir, m.Source)
		}
	}
}

var (
	hostRe       = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)
	secretNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,30}$`)
)

func (l *loader) checkAgent(dir string, a *Agent) bool {
	file := path.Join(dir, "agent.toml")
	n := len(l.errs)
	ok := l.head(file, dir, a.Name, a.Version, true)
	switch a.Run {
	case "", "on-demand", "service":
	default:
		l.add(file, "run %q must be \"on-demand\" or \"service\"", a.Run)
	}
	l.required(file, "image.file", a.Image.File)
	l.file(file, "image.file", dir, a.Image.File)
	l.file(file, "entrypoint", dir, a.Entrypoint)
	for i, h := range a.Allow {
		// meta:<name> is a published range set the firewall hook fetches (meta:github).
		if !hostRe.MatchString(strings.TrimPrefix(h, "meta:")) {
			l.add(file, "allow[%d]: %q is not a host name or meta:<name>", i, h)
		}
	}
	if len(a.Unit.AddCapability) > 0 {
		// Agents drop every capability (D34); NET_ADMIN would let one drop
		// its own firewall (D55).
		l.add(file, "unit.add_capability is not allowed for an agent (agents run with every capability dropped)")
	}
	l.volumes(file, "shared", a.Shared, false)
	l.mounts(file, dir, a.Mounts)
	l.env(file, "env", a.Env)
	for i, s := range a.Secrets {
		if !secretNameRe.MatchString(s.Name) {
			l.add(file, "secrets[%d].name %q must match %s", i, s.Name, secretNameRe)
		}
		if !envNameRe.MatchString(s.Env) {
			l.add(file, "secrets[%d].env %q is not an env var name", i, s.Env)
		}
	}
	for i, p := range a.Config.Paths {
		switch {
		case !configPathRe.MatchString(p) || !fs.ValidPath(p) || p == "." || p == AgentDefaultRequest:
			l.add(file, "config.paths[%d]: %q is not a path inside the home", i, p)
		case slices.Index(a.Config.Paths, p) != i:
			l.add(file, "config.paths[%d]: %q is listed twice", i, p)
		}
	}
	if len(a.Config.Paths) > 0 && a.Entrypoint == "" {
		l.add(file, "config.paths: an agent without an entrypoint can't apply a default")
	}
	return ok && len(l.errs) == n
}

// configPathRe is an agent [config] path: plain names, hidden ones too.
var configPathRe = regexp.MustCompile(`^[A-Za-z0-9._+-]+(/[A-Za-z0-9._+-]+)*$`)

func (l *loader) checkTool(dir string, t *Tool) bool {
	file := path.Join(dir, "tool.toml")
	n := len(l.errs)
	ok := l.head(file, dir, t.Name, 0, false)
	if !nameRe.MatchString(t.Service) {
		l.add(file, "service %q must match %s", t.Service, nameRe)
	}
	l.required(file, "image.file", t.Image.File)
	l.file(file, "image.file", dir, t.Image.File)
	l.file(file, "entrypoint", dir, t.Entrypoint)
	l.volumes(file, "shared", t.Shared, false)
	l.env(file, "env", t.Env)
	l.ports(file, "tool "+t.Name, t.Ports)
	l.links(file, t.Links)
	for i, h := range t.Allow {
		if !hostRe.MatchString(h) {
			l.add(file, "allow[%d]: %q is not a host name", i, h)
		}
	}
	return ok && len(l.errs) == n
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// sources checks each version-chain source of field against the forms
// version.Resolve reads.
func (l *loader) sources(file, field string, from []string) {
	for i, src := range from {
		if !version.KnownSource(src) {
			l.add(file, "%s.from[%d]: %q is not a known version source", field, i, src)
		}
	}
}
