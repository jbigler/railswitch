package cli

import (
	"context"
	"encoding/json"
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

	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/workspace"
)

// packNameRe is a package name: its directory under packs/ or options/.
var packNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// manifestNameRe is a manifest's top-level name line.
var manifestNameRe = regexp.MustCompile(`(?m)^name\s*=\s*"[^"]*"`)

// railswitch pack eject|new|show (D43, D70).
func cmdPack(_ context.Context, e *env, args []string) int {
	const usage = "usage: railswitch pack eject <ref> [--as <name>] | new <name> | show <ref>"
	if len(args) == 0 {
		return e.usageErr(usage)
	}
	switch args[0] {
	case "eject":
		return packEject(e, args[1:])
	case "new":
		return packNew(e, args[1:])
	case "show":
		return packShow(e, args[1:])
	}
	return e.usageErr("pack: unknown subcommand %q\n%s", args[0], usage)
}

// railswitch pack eject <ref> [--as <name>]: copy a built-in pack or option
// to the workspace's packs/<name>/ or options/<name>/, a local package the
// project uses once its ref says local:<name>@N.
func packEject(e *env, args []string) int {
	fset := e.flags("pack eject")
	as := fset.String("as", "", "the local package's name (default: the built-in's)")
	arg, code, ok := parseNamed(e, fset, args, "usage: railswitch pack eject <ref> [--as <name>]")
	if !ok {
		return code
	}
	ref, err := pack.ParseRef(arg)
	if err != nil {
		return e.usageErr("pack eject: %v", err)
	}
	if ref.Local {
		return e.fail(fmt.Errorf("pack eject: %s is already local", ref))
	}
	name := ref.Name
	if *as != "" {
		name = *as
	}
	if !packNameRe.MatchString(name) {
		return e.usageErr("pack eject: --as %q must match %s", name, packNameRe)
	}
	root, err := workspace.Root(e.dir)
	if err != nil {
		return e.fail(err)
	}
	cat, err := pack.LoadBuiltins()
	if err != nil {
		return e.fail(err)
	}
	var top, manifest, use string
	var files fs.FS
	if p := cat.Packs[ref]; p != nil {
		top, manifest, files = "packs", "pack.toml", p.Files
		use = fmt.Sprintf("[stack] pack = %q", fmt.Sprintf("local:%s@%d", name, ref.Version))
	} else if o := cat.Options[ref]; o != nil {
		top, manifest, files = "options", "option.toml", o.Files
		use = fmt.Sprintf("[options] %s = %q", o.Slot, fmt.Sprintf("local:%s@%d", name, ref.Version))
	} else {
		return e.fail(fmt.Errorf("pack eject: no built-in pack or option %s (packs: %s; options: %s)", ref, refNames(cat.Packs), refNames(cat.Options)))
	}
	dir := filepath.Join(root, top, name)
	if err := copyPackage(dir, files, manifest, name); err != nil {
		return e.fail(fmt.Errorf("pack eject: %w", err))
	}
	fmt.Fprintf(e.stdout, "wrote %s/%s/: use it with %s, then railswitch lock\n", top, name, use)
	return 0
}

// copyPackage writes every file of files under dir, which must not exist,
// with the manifest's name line set to name. Scripts (.sh) are executable.
func copyPackage(dir string, files fs.FS, manifest, name string) error {
	if _, err := os.Lstat(dir); err == nil {
		return fmt.Errorf("%s already exists: a local package is yours to edit; remove it to eject again", dir)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	err := fs.WalkDir(files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(files, p)
		if err != nil {
			return err
		}
		if p == manifest {
			loc := manifestNameRe.FindIndex(data)
			if loc == nil {
				return fmt.Errorf("%s has no name line", manifest)
			}
			data = slices.Concat(data[:loc[0]], []byte(fmt.Sprintf("name = %q", name)), data[loc[1]:])
		}
		mode := os.FileMode(0o644)
		if path.Ext(p) == ".sh" {
			mode = 0o755
		}
		return writePackageFile(filepath.Join(dir, filepath.FromSlash(p)), data, mode)
	})
	if err != nil {
		return errors.Join(err, os.RemoveAll(dir))
	}
	return nil
}

func writePackageFile(file string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, data, mode)
}

func refNames[T any](m map[pack.Ref]T) string {
	var out []string
	for r := range m {
		out = append(out, r.String())
	}
	slices.Sort(out)
	return strings.Join(out, ", ")
}

// railswitch pack new <name>: a skeleton pack under packs/<name>/.
func packNew(e *env, args []string) int {
	fset := e.flags("pack new")
	into := fset.String("dir", "", "a workspace `dir` project new has not created yet")
	name, code, ok := parseNamed(e, fset, args, "usage: railswitch pack new [--dir <workspace>] <name>")
	if !ok {
		return code
	}
	if !packNameRe.MatchString(name) {
		return e.usageErr("pack new: %q must match %s", name, packNameRe)
	}
	// A new project's pack comes before its workspace: project new --pack
	// local:<name>@1 reads it from <dir>/packs/<name>/.
	var root string
	var err error
	if *into == "" {
		root, err = workspace.Root(e.dir)
	} else {
		root, err = filepath.Abs(*into)
	}
	if err != nil {
		return e.fail(err)
	}
	if _, err := os.Stat(filepath.Join(root, "project.toml")); *into != "" && err == nil {
		return e.usageErr("pack new: %s is already a workspace: run railswitch -C %s pack new %s", root, root, name)
	}
	dir := filepath.Join(root, "packs", name)
	if _, err := os.Lstat(dir); err == nil {
		return e.fail(fmt.Errorf("pack new: %s already exists", dir))
	} else if !errors.Is(err, fs.ErrNotExist) {
		return e.fail(err)
	}
	files := []struct {
		rel  string
		data string
		mode os.FileMode
	}{
		{"pack.toml", strings.ReplaceAll(packSkeleton, "<name>", name), 0o644},
		{"Containerfile.base", strings.ReplaceAll(baseSkeleton, "<name>", name), 0o644},
		{"entrypoints/entrypoint-app.sh", appEntrypointSkeleton, 0o755},
		{"README.md", strings.ReplaceAll(readmeSkeleton, "<name>", name), 0o644},
	}
	for _, f := range files {
		if err := writePackageFile(filepath.Join(dir, filepath.FromSlash(f.rel)), []byte(f.data), f.mode); err != nil {
			return e.fail(errors.Join(err, os.RemoveAll(dir)))
		}
	}
	if *into != "" {
		fmt.Fprintf(e.stdout, "wrote %s: fill in pack.toml (see docs/packs.md), then railswitch project new %s --pack local:%s@1 --repo <url> --prefix <prefix>\n", dir, root, name)
		return 0
	}
	fmt.Fprintf(e.stdout, "wrote packs/%s/: fill in pack.toml (see docs/packs.md), then use it with [stack] pack = \"local:%s@1\"\n", name, name)
	return 0
}

const packSkeleton = `# Pack <name> (docs/packs.md). Uncomment and fill in; every field is listed.
# name = "<name>"            # must match the directory
# version = 1                # the N of local:<name>@N

# [app]
# service = "web"            # the app's service id
# server = "..."             # the dev server command; it must listen on 0.0.0.0
# port = 4000                # the container port the app route sends to
# install = "..."            # the deps command, unless deps_entrypoint runs one
# health = "curl -fsS -o /dev/null http://localhost:4000/"
# health_start_period = "120s"
# entrypoint = "entrypoints/entrypoint-app.sh"
# deps_entrypoint = "entrypoints/entrypoint-deps.sh"

# [deps]
# frozen_env = { }           # env on the deps service only (a frozen install)

# [image]                    # the base image (D31); app, deps and workers run it
# file = "Containerfile.base"
# tag = "<runtime>{<runtime>}"
# os = "debian"              # debian is needed for agents and the option addons
# args = { RUNTIME_VERSION = "{<runtime>}" }

# [runtimes.<runtime>]       # a version chain: first source found wins
# from = [".tool-versions:<runtime>", "mise.toml:<runtime>"]
# fallback = "1.0.0"         # optional: without it, no source means ""

# [db_name]                  # the database name ({db})
# from = []
# fallback = "{project.prefix}_dev"

# [db_scheme]                # DATABASE_URL scheme per database option
# postgres = "postgres://"

# [env]                      # the app's env; {worktree.host} and the like expand
# APP_ENV = "development"

# [share]                    # env while the worktree is shared on the tailnet
# env = { }

# [[caches]]                 # shared by every worktree: <prefix>_<name>
# name = "deps_<runtime>{<runtime>}"
# mount = "/home/appuser/.cache/x"
# chown = true

# [[worktree_volumes]]       # one per worktree: <prefix>-<slug>-<name>
# name = "build"
# mount = "/app/_build"
# chown = true

# [[ports]]                  # published on 127.0.0.1:<base + worktree ID>
# name = "app"
# env = "APP_PORT"
# base = 13000
# container = 4000
# share = true               # served on the tailnet by railswitch share

# [defaults]                 # options project new picks: slot = ref or "none"
# database = "postgres@1"

# [tasks]
# console = "..."

# [[links]]
# label = "..."
# url = "{worktree.host}/..."

# [unit]
# memory = "4g"
`

const baseSkeleton = `# Base image of pack <name>: railswitch builds it (railswitch containerfile list).
# ARG RUNTIME_VERSION
FROM docker.io/library/debian:trixie-slim
RUN groupadd -g 1000 appuser && useradd -m -u 1000 -g 1000 appuser && \
  mkdir -p /app && chown appuser:appuser /app
WORKDIR /app
USER appuser
`

const appEntrypointSkeleton = `#!/bin/sh
# Runs before the app's server command (its arguments).
set -eu
exec "$@"
`

const readmeSkeleton = `# <name>

What this pack runs, and what it expects of the app.

## App-side changes

Changes the app needs to run under railswitch (railswitch pack show prints this file).
`

// packDoc is railswitch --json pack show (docs/json.md).
type packDoc struct {
	Ref      string       `json:"ref"`
	Kind     string       `json:"kind"`   // "pack" or "option"
	Source   string       `json:"source"` // "built in" or "local"
	Path     string       `json:"path"`   // the local package's directory; "" for a built-in
	App      *packAppDoc  `json:"app,omitempty"`
	Slot     string       `json:"slot,omitempty"`
	Service  string       `json:"service,omitempty"`
	Runtimes []runtimeDoc `json:"runtimes"`
	Ports    []portDoc    `json:"ports"`
	Pin      *pinDoc      `json:"pin"`
	Gives    []string     `json:"gives"`
	Readme   string       `json:"readme"`
}

type packAppDoc struct {
	Service string `json:"service"`
	Server  string `json:"server"`
	Port    int    `json:"port"`
	Health  string `json:"health"`
}

type runtimeDoc struct {
	Name     string   `json:"name"`
	From     []string `json:"from"`
	Fallback string   `json:"fallback"`
}

type pinDoc struct {
	Name    string `json:"name"`
	Default string `json:"default"`
}

type portDoc struct {
	Name      string `json:"name"`
	Base      int    `json:"base"`
	Container int    `json:"container"` // 0 with same_port
}

// railswitch pack show <ref>: a pack's or option's manifest summary and its
// README.md. Built-ins need no workspace.
func packShow(e *env, args []string) int {
	fset := e.flags("pack show")
	arg, code, ok := parseNamed(e, fset, args, "usage: railswitch pack show <ref>")
	if !ok {
		return code
	}
	ref, err := pack.ParseRef(arg)
	if err != nil {
		return e.usageErr("pack show: %v", err)
	}
	root, err := workspace.Root(e.dir)
	if err != nil {
		root = "" // built-ins only
	}
	cat, err := pack.LoadCatalog(root)
	if err != nil {
		return e.fail(err)
	}
	doc, err := showDoc(cat, ref)
	if err != nil {
		return e.fail(err)
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(doc); err != nil {
			return e.fail(err)
		}
		return 0
	}
	printPackDoc(e, doc)
	return 0
}

// showDoc loads ref as a pack or, failing that, an option. A local ref is a
// pack when the workspace has packs/<name>/.
func showDoc(cat *pack.Catalog, ref pack.Ref) (*packDoc, error) {
	doc := &packDoc{Ref: ref.String(), Source: "built in", Runtimes: []runtimeDoc{}, Ports: []portDoc{}, Gives: []string{}}
	isPack := cat.Packs[ref] != nil
	if ref.Local {
		doc.Source = "local"
		doc.Path = filepath.Join(cat.Root, "packs", ref.Name)
		if _, err := os.Stat(doc.Path); err == nil {
			isPack = true
		} else {
			doc.Path = filepath.Join(cat.Root, "options", ref.Name)
		}
	}
	var files fs.FS
	var ports []pack.Port
	if isPack {
		p, err := cat.Pack(ref)
		if err != nil {
			return nil, err
		}
		doc.Kind, files, ports = "pack", p.Files, p.Ports
		doc.App = &packAppDoc{Service: p.App.Service, Server: p.App.Server, Port: p.App.Port, Health: p.App.Health}
		for _, n := range slices.Sorted(maps.Keys(p.Runtimes)) {
			doc.Runtimes = append(doc.Runtimes, runtimeDoc{Name: n, From: orEmpty(p.Runtimes[n].From), Fallback: p.Runtimes[n].Fallback})
		}
	} else {
		o, err := cat.Option(ref)
		if err != nil {
			return nil, err
		}
		if o == nil {
			return nil, fmt.Errorf("no pack or option %s (packs: %s; options: %s)", ref, refNames(cat.Packs), refNames(cat.Options))
		}
		doc.Kind, files, ports = "option", o.Files, o.Ports
		doc.Slot, doc.Service = o.Slot, o.Service
		if o.Pin != nil {
			doc.Pin = &pinDoc{Name: o.Pin.Name, Default: o.Pin.Default}
		}
		doc.Gives = slices.Sorted(maps.Keys(o.Gives))
	}
	for _, pt := range ports {
		doc.Ports = append(doc.Ports, portDoc{Name: pt.Name, Base: pt.Base, Container: pt.Container})
	}
	if data, err := fs.ReadFile(files, "README.md"); err == nil {
		doc.Readme = string(data)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return doc, nil
}

func printPackDoc(e *env, d *packDoc) {
	src := d.Source
	if d.Path != "" {
		src += " " + d.Path
	}
	fmt.Fprintf(e.stdout, "%s (%s, %s)\n", d.Ref, d.Kind, src)
	if a := d.App; a != nil {
		fmt.Fprintf(e.stdout, "app: %s runs %q on :%d\n", a.Service, a.Server, a.Port)
		if a.Health != "" {
			fmt.Fprintf(e.stdout, "health: %s\n", a.Health)
		}
	}
	if d.Slot != "" {
		fmt.Fprintf(e.stdout, "slot: %s, service %s\n", d.Slot, d.Service)
	}
	if d.Pin != nil {
		fmt.Fprintf(e.stdout, "pin: %s = %s\n", d.Pin.Name, d.Pin.Default)
	}
	for _, r := range d.Runtimes {
		fb := "no fallback"
		if r.Fallback != "" {
			fb = "fallback " + r.Fallback
		}
		fmt.Fprintf(e.stdout, "runtime %s: %s (%s)\n", r.Name, strings.Join(r.From, ", "), fb)
	}
	for _, p := range d.Ports {
		to := "same port"
		if p.Container != 0 {
			to = fmt.Sprintf("container %d", p.Container)
		}
		fmt.Fprintf(e.stdout, "port %s: %d+ID, %s\n", p.Name, p.Base, to)
	}
	if len(d.Gives) > 0 {
		fmt.Fprintf(e.stdout, "gives: %s\n", strings.Join(d.Gives, ", "))
	}
	if d.Readme != "" {
		fmt.Fprintf(e.stdout, "\n%s", d.Readme)
	}
}

// packRefCandidates completes pack eject/show: the built-in packs and options.
func packRefCandidates(context.Context, string) []string {
	cat, err := pack.LoadBuiltins()
	if err != nil {
		return nil
	}
	return slices.Concat(strings.Split(refNames(cat.Packs), ", "), strings.Split(refNames(cat.Options), ", "))
}
