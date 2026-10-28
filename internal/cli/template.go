package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/jbigler/railswitch/internal/libraries"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/template"
	"github.com/jbigler/railswitch/internal/workspace"
)

const templateUsage = "usage: railswitch template ls | show <ref> | export [--name <n>] [--version <N>] [--description <text>] [--to <dir>] | check [--accept <sha256>] | import <dir> [--accept <sha256>]"

// railswitch template ls|show|export|check|import (D108–D111, D113).
func cmdTemplate(ctx context.Context, e *env, args []string) int {
	if len(args) == 0 {
		return e.usageErr("%s", templateUsage)
	}
	switch args[0] {
	case "ls":
		return templateLs(e, args[1:])
	case "show":
		return templateShow(e, args[1:])
	case "export":
		return templateExport(ctx, e, args[1:])
	case "check":
		return templateCheck(e, args[1:])
	case "import":
		return templateImport(e, args[1:])
	}
	return e.usageErr("template: unknown subcommand %q\n%s", args[0], templateUsage)
}

// templateRow is one template of `template ls --json` (docs/json.md).
type templateRow struct {
	Ref         string   `json:"ref"`
	Name        string   `json:"name"`
	Version     int      `json:"version"`
	Description string   `json:"description"`
	Source      string   `json:"source"` // "machine" or "library <alias>"
	Packages    []string `json:"packages"`
}

// templates are this machine's templates, then each library's (in
// libraries.toml order), as rows. A template that doesn't resolve is left
// out, its reason printed on stderr.
func (e *env) templates(d machine.Dirs) ([]templateRow, error) {
	rows := []templateRow{}
	add := func(ref, source string) {
		t, _, err := template.Resolve(ref, d, workspace.Libraries(d))
		if err != nil {
			fmt.Fprintf(e.stderr, "railswitch: %s\n", safefile.Printable(err.Error()))
			return
		}
		rows = append(rows, templateRow{Ref: ref, Name: t.Name, Version: t.Version, Description: t.Description, Source: source, Packages: t.Packages()})
	}
	names, err := template.Local(d)
	if err != nil {
		return nil, err
	}
	for _, n := range names {
		files, err := template.ReadDir(template.LocalDir(d, n))
		var t *template.Template
		if err == nil {
			t, err = template.Decode(files)
		}
		if err != nil {
			fmt.Fprintf(e.stderr, "railswitch: template %s: %s\n", n, safefile.Printable(err.Error()))
			continue
		}
		add(t.Ref(), "machine")
	}
	c, err := libraries.Load(libraries.Path(d))
	if err != nil {
		return nil, err
	}
	libs := workspace.Libraries(d)
	for _, lib := range c.Libraries {
		co, err := libs.Checkout(lib.Alias)
		if err != nil {
			continue
		}
		for _, lp := range co.Packages {
			if lp.Kind == pack.KindTemplate {
				r := lp.Ref
				r.Library = lib.Alias
				add(r.String(), "library "+lib.Alias)
			}
		}
	}
	return rows, nil
}

// railswitch template ls: every template a new project can start from.
func templateLs(e *env, args []string) int {
	fset := e.flags("template ls")
	if code, ok := parse(fset, args); !ok {
		return code
	}
	if fset.NArg() != 0 {
		return e.usageErr("usage: railswitch template ls")
	}
	d, err := machine.DefaultDirs()
	if err != nil {
		return e.fail(err)
	}
	rows, err := e.templates(d)
	if err != nil {
		return e.fail(err)
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(struct {
			Templates []templateRow `json:"templates"`
		}{rows}); err != nil {
			return e.fail(err)
		}
		return 0
	}
	if len(rows) == 0 {
		fmt.Fprintln(e.stdout, "no templates: railswitch template export saves a project as one")
	}
	for _, r := range rows {
		fmt.Fprintf(e.stdout, "%s\t%s\t%s\t%s\n", safefile.Printable(r.Ref), r.Source, safefile.Printable(strings.Join(r.Packages, " ")), safefile.Printable(r.Description))
	}
	return 0
}

// templateShowDoc is `template show --json` (docs/json.md).
type templateShowDoc struct {
	Ref            string            `json:"ref"`
	SHA256         string            `json:"sha256"`
	StackTOML      string            `json:"stack_toml"`
	Ask            []template.Ask    `json:"ask"`
	Secrets        []string          `json:"secrets"`
	Containerfiles []string          `json:"containerfiles"` // names: Containerfile.<name>
	Readme         string            `json:"readme"`
	Files          []templateFileDoc `json:"files"` // every file with its text, by path
}

type templateFileDoc struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

func templateShowOf(ref string, t *template.Template, sha string) (*templateShowDoc, error) {
	doc := &templateShowDoc{Ref: ref, SHA256: sha, Ask: t.Ask, Secrets: t.Secrets, Containerfiles: []string{}, Readme: string(t.Readme), Files: []templateFileDoc{}}
	for n := range t.Containerfiles {
		doc.Containerfiles = append(doc.Containerfiles, n)
	}
	slices.Sort(doc.Containerfiles)
	err := fs.WalkDir(t.Files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(t.Files, p)
		if p == template.StackFile {
			doc.StackTOML = string(data)
		}
		doc.Files = append(doc.Files, templateFileDoc{Path: p, Text: string(data)})
		return err
	})
	return doc, err
}

// railswitch template show <ref>: a template's stack.toml, questions and
// files.
func templateShow(e *env, args []string) int {
	fset := e.flags("template show")
	ref, code, ok := parseNamed(e, fset, args, "usage: railswitch template show <ref>")
	if !ok {
		return code
	}
	d, err := machine.DefaultDirs()
	if err != nil {
		return e.fail(err)
	}
	t, sha, err := template.Resolve(ref, d, workspace.Libraries(d))
	if err != nil {
		return e.fail(err)
	}
	doc, err := templateShowOf(ref, t, sha)
	if err != nil {
		return e.fail(err)
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(doc); err != nil {
			return e.fail(err)
		}
		return 0
	}
	fmt.Fprintf(e.stdout, "# %s (sha256 %s)\n", safefile.Printable(ref), sha)
	for _, f := range doc.Files {
		fmt.Fprintf(e.stdout, "\n# %s\n", safefile.Printable(f.Path))
		for _, line := range strings.SplitAfter(f.Text, "\n") {
			if line != "" {
				fmt.Fprintf(e.stdout, "%s\n", safefile.Printable(strings.TrimSuffix(line, "\n")))
			}
		}
	}
	return 0
}

// railswitch template export: save the project (-C) as a template,
// questions and edited Containerfiles included, in this machine's store
// or --to <dir> (the template directory itself, e.g. a library's
// templates/<name>).
func templateExport(ctx context.Context, e *env, args []string) int {
	fset := e.flags("template export")
	name := fset.String("name", "", "the template's `name` (default: the project's prefix)")
	version := fset.Int("version", 0, "its `version` (default: one more than this machine's, else 1)")
	desc := fset.String("description", "", "a one-line `description`")
	to := fset.String("to", "", "write the template directory `dir` (must not exist) instead of this machine's store")
	if code, ok := parse(fset, args); !ok {
		return code
	}
	if fset.NArg() != 0 {
		return e.usageErr("%s", templateUsage)
	}
	root, err := workspace.Root(e.dir)
	if err != nil {
		return e.fail(err)
	}
	w, err := workspace.Load(ctx, root, "")
	if err != nil {
		return e.fail(err)
	}
	// A template is made from what stack.lock accepted.
	if err := w.CheckLock(); err != nil {
		return e.fail(fmt.Errorf("template export: %w", err))
	}
	if w.ReviewPending() {
		return e.fail(errors.New("template export: a package update waits for review (railswitch lock --review): a template is made from what was accepted"))
	}
	if *name == "" {
		*name = w.Project.Project.Prefix
	}
	dir := *to
	if dir == "" {
		dir = template.LocalDir(w.Dirs, *name)
		prev, err := localVersion(dir)
		if err != nil {
			return e.fail(err)
		}
		switch {
		case *version == 0:
			*version = prev + 1
		case *version <= prev:
			return e.fail(fmt.Errorf("template %s@%d is on this machine: a new version must be higher than %d", *name, prev, prev))
		}
	} else if _, err := os.Lstat(dir); err == nil {
		return e.fail(fmt.Errorf("%s already exists: export writes a new directory", dir))
	} else if *version == 0 {
		*version = 1
	}
	files, user, err := editedContainerfiles(root, w)
	if err != nil {
		return e.fail(err)
	}
	// [image] addons keeps the user addons: an option's addon comes from
	// its eject in the new project.
	p := *w.Project
	p.Image.Addons = slices.DeleteFunc(slices.Clone(p.Image.Addons), func(a string) bool {
		return !slices.Contains(user, strings.TrimPrefix(a, "containerfiles/Containerfile."))
	})
	t, dropped, err := template.Export(&p, files, bundledPackages(w.Stack), *name, *version)
	if err != nil {
		return e.fail(err)
	}
	if *desc != "" {
		t.Description = *desc
		if t, err = t.Build(); err != nil {
			return e.fail(err)
		}
	}
	if err := t.Write(dir); err != nil {
		return e.fail(err)
	}
	fmt.Fprintf(e.stdout, "wrote template %s to %s\n", t.Ref(), dir)
	for _, d := range dropped {
		fmt.Fprintf(e.stdout, "dropped %s\n", d)
	}
	for _, a := range t.Ask {
		fmt.Fprintf(e.stdout, "asks %s (default %s)\n", a.Key, safefile.Printable(strconv.Quote(a.Default)))
	}
	for _, s := range t.Secrets {
		fmt.Fprintf(e.stdout, "declares secret %s (no value)\n", s)
	}
	for _, n := range slices.Sorted(maps.Keys(t.Containerfiles)) {
		fmt.Fprintf(e.stdout, "keeps containerfiles/Containerfile.%s\n", n)
	}
	for _, r := range slices.SortedFunc(maps.Keys(t.Bundled), func(a, b pack.Ref) int { return strings.Compare(a.String(), b.String()) }) {
		fmt.Fprintf(e.stdout, "bundles %s (%s/)\n", r, t.Bundled[r].Dir)
	}
	return 0
}

// bundledPackages are st's local pack and options as a template bundles
// them (D116): the content the project runs, its accepted copy for a
// locked one (st is a loaded workspace's).
func bundledPackages(st *pack.Stack) map[pack.Ref]template.Bundle {
	out := map[pack.Ref]template.Bundle{}
	if st.PackRef.Local {
		out[st.PackRef] = template.Bundle{Kind: pack.KindPack, Dir: "packs/" + st.PackRef.Name, Files: st.Pack.Files}
	}
	for _, ch := range st.Options {
		if ch.Ref.Local {
			out[ch.Ref] = template.Bundle{Kind: pack.KindOption, Dir: "options/" + ch.Ref.Name, Files: ch.Option.Files}
		}
	}
	return out
}

// localVersion is the version of the template in the store directory dir;
// 0 when there is none. One that doesn't decode is an error: export
// replaces only a template it can read.
func localVersion(dir string) (int, error) {
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	files, err := template.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	t, err := template.Decode(files)
	if err != nil {
		return 0, fmt.Errorf("%s: %w (remove it to export again)", dir, err)
	}
	return t.Version, nil
}

// personalContainerfiles are the names of the editor's and the agents'
// Containerfiles (D112): every tool's, agent-base and each of st's
// agents'. A template never holds one.
func personalContainerfiles(cat *pack.Catalog, st *pack.Stack) map[string]bool {
	out := map[string]bool{"agent-base": true}
	for _, t := range cat.Tools {
		out[cfName(t.Image.File)] = true
	}
	for _, a := range st.Agents {
		out[cfName(a.Agent.Image.File)] = true
	}
	return out
}

// editedContainerfiles are the Containerfiles root's stack builds that a
// template keeps, by name: each whose body differs from the default it was
// ejected from (the containerfile show diff), and each with no default (a
// user addon), and the names of those with no default. The editor's and
// the agents' are never kept (D112).
func editedContainerfiles(root string, w *workspace.Workspace) (map[string][]byte, []string, error) {
	out := map[string][]byte{}
	var user []string
	personal := personalContainerfiles(w.Catalog, w.Stack)
	for _, rel := range render.Containerfiles(w.Project, w.Stack) {
		name := strings.TrimPrefix(rel, "containerfiles/Containerfile.")
		if personal[name] {
			continue
		}
		doc, err := cfShowJSON(root, name)
		if err != nil {
			return nil, nil, err
		}
		if doc.Default == nil {
			user = append(user, name)
		}
		if doc.Default == nil || doc.Diff != "" {
			out[name] = []byte(doc.Text)
		}
	}
	return out, user, nil
}
