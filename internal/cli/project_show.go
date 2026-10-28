package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/workspace"
)

// projectDoc is `project show --json`: what the settings page needs in one
// read (docs/json.md).
type projectDoc struct {
	Prefix     string      `json:"prefix"`
	Name       string      `json:"name"`
	Repo       string      `json:"repo"`
	BaseBranch string      `json:"base_branch"`
	Hash       string      `json:"hash"`
	File       string      `json:"file"` // the raw editor's text
	Options    []optionRow `json:"options"`
	AddChoices []string    `json:"add_choices"` // the options a new extra key (D81) can take
	Used       []string    `json:"used"`        // the stack's pack, option and agent refs
	Env        []envPair   `json:"env"`
	Links      []linkRow   `json:"links"`
}

type optionRow struct {
	Key     string   `json:"key"`
	Slot    bool     `json:"slot"`
	Ref     string   `json:"ref"`
	Choices []string `json:"choices"`
}

type envPair struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type linkRow struct {
	Label  string `json:"label"`
	URL    string `json:"url"`
	From   string `json:"from"`
	Hidden bool   `json:"hidden"`
}

// optionRef is a package option a key can choose, with its slot.
type optionRef struct {
	ref  pack.Ref
	slot string
}

// optionRefs are the options a workspace offers: each built-in name at its
// newest version, then each workspace-local one (options/<name>/) at the
// version its option.toml declares, sorted by name.
func optionRefs(cat *pack.Catalog, root string) []optionRef {
	var out []optionRef
	for _, r := range newestBuiltins(cat.Options) {
		out = append(out, optionRef{r, cat.Options[r].Slot})
	}
	for _, m := range localManifests(root, "options", "option.toml") {
		out = append(out, optionRef{m.ref, m.Slot})
	}
	return out
}

// newestBuiltins is each built-in name of m at its newest version, sorted
// by name.
func newestBuiltins[V any](m map[pack.Ref]V) []pack.Ref {
	best := map[string]pack.Ref{}
	for r := range m {
		if b, ok := best[r.Name]; !r.Local && (!ok || r.Version > b.Version) {
			best[r.Name] = r
		}
	}
	out := []pack.Ref{}
	for _, name := range slices.Sorted(maps.Keys(best)) {
		out = append(out, best[name])
	}
	return out
}

// localManifest is what a listing reads of a workspace-local package.
type localManifest struct {
	ref       pack.Ref
	Version   int
	Slot      string
	WorksWith []string `toml:"works_with"`
}

// localManifests reads root/<top>/<name>/<file> for each package dir,
// sorted by name. Decoding is lenient so that a package that doesn't load
// yet is still offered (choosing it shows its error); one with no usable
// version is skipped.
func localManifests(root, top, file string) []localManifest {
	var out []localManifest
	ents, _ := os.ReadDir(filepath.Join(root, top))
	for _, ent := range ents {
		if !ent.IsDir() {
			continue
		}
		var m localManifest
		if _, err := toml.DecodeFile(filepath.Join(root, top, ent.Name(), file), &m); err != nil || m.Version < 1 {
			continue
		}
		m.ref = pack.Ref{Name: ent.Name(), Version: m.Version, Local: true}
		out = append(out, m)
	}
	return out
}

// choices are the refs key can take: a slot's options of that slot, any
// other key's every option, then "none"; the current ref is always one.
func choices(opts []optionRef, key string, slot bool, cur string) []string {
	out := []string{}
	for _, o := range opts {
		if !slot || o.slot == key {
			out = append(out, o.ref.String())
		}
	}
	if cur != "none" && !slices.Contains(out, cur) {
		out = append(out, cur)
	}
	return append(out, "none")
}

func buildProjectDoc(w *workspace.Workspace, data []byte) projectDoc {
	p, st := w.Project, w.Stack
	d := projectDoc{
		Prefix: p.Project.Prefix, Name: p.Project.Name, Repo: p.Project.Repo, BaseBranch: p.Project.BaseBranch,
		Hash: workspace.Hash(data), File: string(data), Options: []optionRow{}, Env: []envPair{}, Links: []linkRow{},
	}
	opts := optionRefs(w.Catalog, w.Root)
	d.AddChoices = []string{}
	for _, o := range opts {
		d.AddChoices = append(d.AddChoices, o.ref.String())
	}
	for _, key := range pack.OptionKeys(p.Options) {
		slot := slices.Contains(config.Slots, key)
		ref, ok := p.Options[key]
		if !ok {
			if !slot {
				continue
			}
			ref = "none"
		}
		d.Options = append(d.Options, optionRow{Key: key, Slot: slot, Ref: ref, Choices: choices(opts, key, slot, ref)})
	}
	for _, name := range slices.Sorted(maps.Keys(p.Env)) {
		d.Env = append(d.Env, envPair{name, p.Env[name]})
	}
	for _, l := range p.Links {
		d.Links = append(d.Links, linkRow{Label: l.Label, URL: l.URL, From: "project"})
	}
	add := func(from string, links []pack.Link) {
		for _, l := range links {
			d.Links = append(d.Links, linkRow{l.Label, l.URL, from, slices.Contains(p.HideLinks, l.Label)})
		}
	}
	d.Used = []string{st.PackRef.String()}
	for _, ch := range st.Options {
		d.Used = append(d.Used, ch.Ref.String())
	}
	for _, a := range st.Agents {
		if r := a.Ref.String(); !slices.Contains(d.Used, r) {
			d.Used = append(d.Used, r)
		}
	}
	add("pack "+st.PackRef.String(), st.Pack.Links)
	for _, ch := range st.Options {
		add("option "+ch.Ref.String(), ch.Option.Links)
	}
	for _, t := range st.Tools {
		add("tool "+t.Name, t.Links)
	}
	return d
}

// railswitch project show: the project's choices, env and links for the
// settings page.
func projectShow(ctx context.Context, e *env, args []string) int {
	fs := e.flags("project show")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("project show takes no arguments")
	}
	w, err := workspace.Load(ctx, e.dir, "")
	if err != nil {
		return e.fail(err)
	}
	data, err := projectBytes(w.Root)
	if err != nil {
		return e.fail(err)
	}
	d := buildProjectDoc(w, data)
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(d); err != nil {
			return e.fail(err)
		}
		return 0
	}
	fmt.Fprintf(e.stdout, "%s (%s)  %s  base %s\n", d.Name, d.Prefix, d.Repo, d.BaseBranch)
	for _, o := range d.Options {
		fmt.Fprintf(e.stdout, "%s = %s  (%s)\n", o.Key, o.Ref, strings.Join(o.Choices, ", "))
	}
	for _, v := range d.Env {
		fmt.Fprintf(e.stdout, "env %s=%s\n", v.Name, v.Value)
	}
	for _, l := range d.Links {
		hidden := ""
		if l.Hidden {
			hidden = " (hidden)"
		}
		fmt.Fprintf(e.stdout, "link %s  %s  [%s]%s\n", l.Label, l.URL, l.From, hidden)
	}
	return 0
}

// railswitch project write --if-hash <h>: replace project.toml with stdin
// when it parses, validates and resolves; then lock and render.
func projectWrite(ctx context.Context, e *env, args []string) int {
	fs := e.flags("project write")
	hash := fs.String("if-hash", "", "the project.toml hash the page read (required)")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 || *hash == "" {
		return e.usageErr("usage: railswitch project write --if-hash <hash>  (the whole file on stdin)")
	}
	next, err := e.readStdin()
	if err != nil {
		return e.fail(err)
	}
	root, err := workspace.Root(e.dir)
	if err != nil {
		return e.fail(err)
	}
	old, err := projectBytes(root)
	if err != nil {
		return e.fail(err)
	}
	if workspace.Hash(old) != *hash {
		return e.fail(errChanged)
	}
	p, err := config.Parse(next)
	if err != nil {
		return e.fail(err)
	}
	if err := p.Validate(); err != nil {
		return e.fail(fmt.Errorf("project.toml: %w", err))
	}
	cat, err := pack.LoadCatalog(root)
	if err != nil {
		return e.fail(err)
	}
	if _, err := cat.Resolve(p); err != nil {
		return e.fail(err)
	}
	if err := e.commitProject(ctx, root, next); err != nil {
		return e.fail(err)
	}
	return 0
}
