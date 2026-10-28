package settings

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/render"
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
	p := &Preview{Hash: workspace.Hash(data), Edits: []EditResult{}}
	text, prev := string(data), cur // prev renders text
	for i, ed := range edits {
		r, next := EditResult{Impact: ImpactBlocked, Reasons: []string{}}, (*rendered)(nil)
		if t, err := ApplyText(text, ed); err != nil {
			r.Error = err.Error()
		} else if r, next = e.judge(prev, t); next != nil {
			text, prev = t, next
		}
		r.Index = i
		p.Edits = append(p.Edits, r)
	}
	p.NewText = text
	p.Diff = unifiedDiff(string(data), text)
	p.Restart = e.restart(cur, prev)
	return p, nil
}

// rendered is one render of the project: its model, its stamped units and
// each registered worktree's instance (Model.Inputs.Worktrees order).
type rendered struct {
	ws    *workspace.Workspace
	m     *render.Model
	units map[string][]byte
	ins   []*render.Instance
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
	r := &rendered{ws: ws, m: m, units: plan.Units}
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
func (e Env) judge(prev *rendered, text string) (EditResult, *rendered) {
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
// containers' stamped units differ between cur and next.
func (e Env) restart(cur, next *rendered) []string {
	out := []string{}
	prefix, app := cur.m.Prefix, cur.ws.Project.Stack.App
	var ids []string
	for _, s := range append(slices.Clone(cur.m.Services), next.m.Services...) {
		if !slices.Contains(ids, s.ID) {
			ids = append(ids, s.ID)
		}
	}
	for _, wt := range cur.m.Inputs.Worktrees {
		if e.Running == nil || !e.Running(naming.Container(prefix, wt.Slug, app)+".service") {
			continue
		}
		for _, id := range ids {
			unit := naming.Container(prefix, wt.Slug, id) + ".container"
			if !bytes.Equal(cur.units[unit], next.units[unit]) {
				out = append(out, wt.Slug)
				break
			}
		}
	}
	return out
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
