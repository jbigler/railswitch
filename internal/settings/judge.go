package settings

import (
	"bytes"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/textdiff"
	"github.com/jbigler/railswitch/internal/workspace"
)

// Impact is what an edit does to the project's data (the page's label).
type Impact string

const (
	ImpactNone    Impact = "none"    // No data affected
	ImpactCheck   Impact = "check"   // Check before applying: the app's env changes
	ImpactData    Impact = "data"    // Data not carried over: a volume is left behind
	ImpactBlocked Impact = "blocked" // the edit can't be placed, or the result does not render
)

// EditResult is the judgement of edit Index.
type EditResult struct {
	Index   int      `json:"index"`
	Impact  Impact   `json:"impact"`
	Reasons []string `json:"reasons"`
	Error   string   `json:"error"`
}

// Preview is what a list of edits does, written nowhere.
type Preview struct {
	Hash    string       `json:"hash"` // of the current project.toml (workspace.Hash)
	Diff    string       `json:"diff"` // project.toml before and after every edit that could be placed
	Edits   []EditResult `json:"edits"`
	Restart []string     `json:"restart"` // registered worktrees whose running app runs units the edits change
	NewText string       `json:"-"`
	// NewFiles are the Containerfiles the edits eject, a newly chosen
	// option's addon or own image (workspace-relative path → content):
	// written with NewText (D123).
	NewFiles map[string][]byte `json:"-"`
	// Writes are NewFiles' paths, sorted: what the preview shows (D123).
	Writes []string `json:"writes"`
}

// Env is the loaded project the edits apply to.
type Env struct {
	WS      *workspace.Workspace
	Reg     *machine.Registry
	Host    render.Host
	Running func(unit string) bool // a systemd unit is active; nil: none is
}

// Preview judges edits against data, the current project.toml: edit i
// against the project with the edits before it applied, so its result is
// its own; a blocked one is left out of the ones after it. The error is for
// a current project that does not render.
func (e Env) Preview(data []byte, edits []Edit) (*Preview, error) {
	cur, err := e.render(e.WS)
	if err != nil {
		return nil, err
	}
	p := &Preview{Hash: workspace.Hash(data), Edits: []EditResult{}, NewFiles: map[string][]byte{}}
	text, prev := string(data), cur // prev renders text
	for i, ed := range edits {
		r, next := EditResult{Impact: ImpactBlocked, Reasons: []string{}}, (*rendered)(nil)
		t, err := ApplyText(text, ed)
		var files map[string][]byte
		if err == nil && (ed.Op == "option" || ed.Op == "pack") {
			t, files, err = e.syncAddons(text, t)
		}
		if err != nil {
			r.Error = err.Error()
		} else if r, next = e.judge(prev, t, files); next != nil {
			text, prev = t, next
			maps.Copy(p.NewFiles, files)
		}
		r.Index = i
		p.Edits = append(p.Edits, r)
	}
	p.NewText = text
	p.Writes = append([]string{}, slices.Sorted(maps.Keys(p.NewFiles))...)
	p.Diff = textdiff.Unified("project.toml", "project.toml", string(data), text)
	p.Restart = e.restart(cur, prev)
	return p, nil
}

// syncAddons makes text's [image] addons follow the options the edit
// changed from before (D123): the addon of an option no longer chosen goes
// (its file stays: it may hold your edits), a newly chosen option's addon is
// added, ejected from its default when the workspace has no file of that
// name. Only the changed options' addons are touched (all of them when the
// pack changed, which sets the OS): an addon you removed or added by hand
// for an option the edit leaves alone stays as you left it. A newly chosen
// option with its own image ([image] build) gets that Containerfile ejected
// the same way. Ejects copy the accepted package. It returns text
// as it was when either stack doesn't resolve (judge then blocks it).
func (e Env) syncAddons(before, text string) (string, map[string][]byte, error) {
	p, err := config.Parse([]byte(text))
	if err != nil {
		return text, nil, nil
	}
	old, err := config.Parse([]byte(before))
	if err != nil {
		return text, nil, nil
	}
	// Ejects copy what the lock accepted (a package back from [[removed]]
	// at its old content), never a change not reviewed yet.
	cat, err := workspace.Accepted(e.WS.Root, p, e.WS.Catalog)
	if err != nil {
		return text, nil, err
	}
	st, err := cat.Resolve(p)
	if err != nil {
		return text, nil, nil
	}
	oldSt, err := e.WS.Catalog.Resolve(old)
	if err != nil {
		return text, nil, nil
	}
	rel := func(file string) string { return "containerfiles/" + file }
	addonOf := func(st *pack.Stack) map[string]string { // option key → its addon, for this pack's OS
		out := map[string]string{}
		for _, ch := range st.Options {
			if a := ch.Option.AppAddon; a != nil && a.OS == st.Pack.Image.OS {
				out[ch.Key] = rel(a.File)
			}
		}
		return out
	}
	was, now := addonOf(oldSt), addonOf(st)
	packChanged := oldSt.PackRef != st.PackRef
	changed := func(key string) bool {
		return packChanged || old.Options[key] != p.Options[key]
	}
	drop := map[string]bool{}
	for key, a := range was {
		if changed(key) && !slices.Contains(slices.Collect(maps.Values(now)), a) {
			drop[a] = true
		}
	}
	var addons []string
	for _, a := range p.Image.Addons {
		if !drop[a] {
			addons = append(addons, a)
		}
	}
	files := map[string][]byte{}
	for _, ch := range st.Options {
		a := ch.Option.AppAddon
		if a == nil || now[ch.Key] == "" || !changed(ch.Key) || slices.Contains(addons, rel(a.File)) {
			continue
		}
		addons = append(addons, rel(a.File))
		if _, err := os.Lstat(filepath.Join(e.WS.Root, "containerfiles", a.File)); err == nil {
			continue // a copy you had: kept as it is
		}
		def, err := fs.ReadFile(ch.Option.Files, a.File)
		if err != nil {
			return "", nil, err
		}
		files[rel(a.File)] = workspace.EjectedContainerfile(ch.Ref.String(), strings.TrimPrefix(a.File, "Containerfile."), def)
	}
	// A newly chosen option with its own image (playwright) needs its
	// Containerfile in the workspace too, and `containerfile eject` refuses
	// one the stack doesn't use yet: it is ejected here, as an addon is.
	for _, ch := range st.Options {
		b := ch.Option.Image.Build
		if b == "" || !changed(ch.Key) {
			continue
		}
		if _, err := os.Lstat(filepath.Join(e.WS.Root, "containerfiles", b)); err == nil {
			continue // a copy you had: kept as it is
		}
		def, err := fs.ReadFile(ch.Option.Files, b)
		if err != nil {
			return "", nil, err
		}
		files[rel(b)] = workspace.EjectedContainerfile(ch.Ref.String(), strings.TrimPrefix(b, "Containerfile."), def)
	}
	if slices.Equal(addons, p.Image.Addons) {
		return text, files, nil
	}
	out, err := config.SetImageAddons(text, addons)
	return out, files, err
}

// rendered is one render of the project: its model, its stamped units, the
// package assets it mounts and each registered worktree's instance
// (Model.Inputs.Worktrees order).
type rendered struct {
	ws     *workspace.Workspace
	m      *render.Model
	units  map[string][]byte
	assets map[string][]byte
	ins    []*render.Instance
}

func (e Env) render(ws *workspace.Workspace) (*rendered, error) {
	m, err := ws.Model(e.Reg, e.Host)
	if err != nil {
		return nil, err
	}
	plan, err := m.Plan()
	if err != nil {
		return nil, err
	}
	r := &rendered{ws: ws, m: m, units: plan.Units, assets: map[string][]byte{}}
	for _, a := range m.Assets {
		r.assets[a.Rel] = a.Data
	}
	for _, wt := range m.Inputs.Worktrees {
		in, err := m.Instance(wt)
		if err != nil {
			return nil, fmt.Errorf("worktree %s: %w", wt.Slug, err)
		}
		r.ins = append(r.ins, in)
	}
	return r, nil
}

// judge compares the render of text with prev's; the render is nil when
// text does not load, validate, resolve or render (blocked).
func (e Env) judge(prev *rendered, text string, files map[string][]byte) (EditResult, *rendered) {
	blocked := func(err error) (EditResult, *rendered) {
		return EditResult{Impact: ImpactBlocked, Reasons: []string{}, Error: err.Error()}, nil
	}
	p, err := config.Parse([]byte(text))
	if err != nil {
		return blocked(err)
	}
	if err := p.Validate(); err != nil {
		return blocked(fmt.Errorf("project.toml: %w", err))
	}
	ws, err := prev.ws.WithProject(p)
	if err != nil {
		return blocked(err)
	}
	for rel, data := range files {
		ws = ws.WithFile(rel, data)
	}
	next, err := e.render(ws)
	if err != nil {
		return blocked(err)
	}
	data, err := dataReasons(prev, next)
	if err != nil {
		return blocked(err)
	}
	env := envReasons(prev, next)
	r := EditResult{Impact: ImpactNone, Reasons: append(append([]string{}, data...), env...)}
	switch {
	case len(data) > 0:
		r.Impact = ImpactData
	case len(env) > 0:
		r.Impact = ImpactCheck
	}
	return r, next
}

type volume struct {
	name, svc string
	cache     bool
}

// volumes are the named volumes of worktree i, each once, in service order.
func (r *rendered) volumes(i int) ([]volume, error) {
	var out []volume
	for _, s := range r.m.Services {
		for _, v := range s.Volumes {
			name, err := r.ins[i].VolumeName(s.ID, v)
			if err != nil {
				return nil, err
			}
			if !slices.ContainsFunc(out, func(x volume) bool { return x.name == name }) {
				out = append(out, volume{name, s.ID, v.Cache})
			}
		}
	}
	return out, nil
}

// dataReasons name, per registered worktree, the non-cache volumes cur
// mounts and next does not, with the service's new volumes that start
// empty, and the dump hint when such a service's option can be dumped.
func dataReasons(cur, next *rendered) ([]string, error) {
	var reasons []string
	dump := false
	for i := range cur.ins {
		have, err := cur.volumes(i)
		if err != nil {
			return nil, err
		}
		want, err := next.volumes(i)
		if err != nil {
			return nil, err
		}
		in := func(vs []volume, name string) bool {
			return slices.ContainsFunc(vs, func(v volume) bool { return v.name == name })
		}
		var svcs []string
		left := map[string][]string{}
		for _, v := range have {
			if !v.cache && !in(want, v.name) {
				if left[v.svc] == nil {
					svcs = append(svcs, v.svc)
				}
				left[v.svc] = append(left[v.svc], v.name)
			}
		}
		for _, svc := range svcs {
			reason := list(left[svc], "is", "are") + " left behind"
			var fresh []string
			for _, v := range want {
				if v.svc == svc && !v.cache && !in(have, v.name) {
					fresh = append(fresh, v.name)
				}
			}
			if len(fresh) > 0 {
				reason += "; " + list(fresh, "starts", "start") + " empty"
			}
			reasons = appendNew(reasons, reason)
			dump = dump || dumps(cur.ws, svc)
		}
	}
	if dump {
		reasons = append(reasons, "run railswitch db dump first")
	}
	return reasons, nil
}

// dumps reports whether service svc runs an option with [dump].
func dumps(ws *workspace.Workspace, svc string) bool {
	return slices.ContainsFunc(ws.Stack.Options, func(ch *pack.Chosen) bool {
		return ch.Service == svc && ch.Option.Dump != nil
	})
}

// envReasons name the app service's env vars, in any registered worktree,
// that next drops or changes.
func envReasons(cur, next *rendered) []string {
	app := cur.ws.Project.Stack.App
	var reasons []string
	for i := range cur.ins {
		after := map[string]string{}
		for _, v := range next.ins[i].Env[app] {
			after[v.Name] = v.Value
		}
		for _, v := range cur.ins[i].Env[app] {
			switch val, ok := after[v.Name]; {
			case !ok:
				reasons = appendNew(reasons, v.Name+" is no longer given to "+app)
			case val != v.Value:
				reasons = appendNew(reasons, v.Name+" changes for "+app)
			}
		}
	}
	return reasons
}

// restart are the registered worktrees whose app unit runs and any of whose
// containers' stamped units differ between cur and next, or every running
// one when a mounted package asset's bytes differ (assets are the
// project's, not a worktree's). cur.m is nil for a current project that
// does not render: then every unit differs.
func (e Env) restart(cur, next *rendered) []string {
	out := []string{}
	prefix, app := next.m.Prefix, next.ws.Project.Stack.App
	services := next.m.Services
	if cur.m != nil {
		services = append(slices.Clone(cur.m.Services), services...)
	}
	var ids []string
	for _, s := range services {
		if !slices.Contains(ids, s.ID) {
			ids = append(ids, s.ID)
		}
	}
	assets := !maps.EqualFunc(cur.assets, next.assets, bytes.Equal)
	for _, wt := range next.m.Inputs.Worktrees {
		if e.Running == nil || !e.Running(naming.Container(prefix, wt.Slug, app)+".service") {
			continue
		}
		if assets || slices.ContainsFunc(ids, func(id string) bool {
			unit := naming.Container(prefix, wt.Slug, id) + ".container"
			return !bytes.Equal(cur.units[unit], next.units[unit])
		}) {
			out = append(out, wt.Slug)
		}
	}
	return out
}

// TagChange is a worktree's build step whose image tag a change alters;
// OldTag or NewTag is "" for a step only one side builds.
type TagChange struct {
	Worktree string `json:"worktree"`
	Name     string `json:"name"`
	OldTag   string `json:"old_tag"`
	NewTag   string `json:"new_tag"`
}

// Change is what changing the workspace's files does to its builds and
// running worktrees.
type Change struct {
	Steps   []TagChange `json:"steps"`
	Restart []string    `json:"restart"`
}

// Compare judges next, the project with files changed in memory
// (WithFile, LoadEdited), against cur. The error is next's: it does not
// render. A current project that does not load (cur nil) or render (a
// broken file being fixed) has no builds, and each of next's running
// worktrees restarts.
func (e Env) Compare(cur, next *workspace.Workspace) (*Change, error) {
	to, err := e.render(next)
	if err != nil {
		return nil, err
	}
	from := &rendered{ws: cur}
	if cur != nil {
		if r, err := e.render(cur); err == nil {
			from = r
		}
	}
	c := &Change{Steps: []TagChange{}, Restart: e.restart(from, to)}
	for i, in := range to.ins {
		slug := to.m.Inputs.Worktrees[i].Slug
		var before []render.BuildStep
		if from.ins != nil {
			before = from.ins[i].Builds
		}
		old := map[string]string{}
		for _, b := range before {
			old[b.Name] = b.Tag
		}
		for _, b := range in.Builds {
			if old[b.Name] != b.Tag {
				c.Steps = append(c.Steps, TagChange{Worktree: slug, Name: b.Name, OldTag: old[b.Name], NewTag: b.Tag})
			}
			delete(old, b.Name)
		}
		for _, b := range before {
			if _, gone := old[b.Name]; gone {
				c.Steps = append(c.Steps, TagChange{Worktree: slug, Name: b.Name, OldTag: b.Tag})
			}
		}
	}
	return c, nil
}

func list(names []string, one, many string) string {
	if len(names) == 1 {
		return names[0] + " " + one
	}
	return strings.Join(names, ", ") + " " + many
}

func appendNew(list []string, s string) []string {
	if slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}
