package web

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/jobs"
)

//go:embed templates/*.tmpl
var templateFiles embed.FS

var tmpl = template.Must(template.New("").ParseFS(templateFiles, "templates/*.tmpl"))

// render executes the named template (a page or a fragment).
func render(name string, data any) (string, error) {
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, name, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

// logsURL is Dozzle, the machine's log viewer.
const logsURL = "http://logs.localhost/"

// viewLines is how many of a job's kept lines the page shows.
const viewLines = 500

// row is a worktree with its branch's PR (nil: none, or not known yet).
type row struct {
	WT worktree
	PR *pr
}

// Sorting.

// sortKey is the list's order: By one of sortBys, Rev reversed. Its text
// form, the ?sort= value, is the key with a leading "-" when reversed.
type sortKey struct {
	By  string
	Rev bool
}

var sortBys = []string{"name", "running", "pr", "agent"}

func parseSort(s string) sortKey {
	k := sortKey{}
	s, k.Rev = strings.CutPrefix(s, "-")
	if !slices.Contains(sortBys, s) {
		return sortKey{By: "name"}
	}
	k.By = s
	return k
}

func (k sortKey) String() string {
	if k.Rev {
		return "-" + k.By
	}
	return k.By
}

// query is the ?sort= suffix of a page URL; none for the default.
func (k sortKey) query() string {
	if k == (sortKey{By: "name"}) {
		return ""
	}
	return "?sort=" + url.QueryEscape(k.String())
}

var stackOrder = map[string]int{"failed": 0, "up": 1, "partial": 2, "stopped": 3}

var agentOrder = map[string]int{"permission": 0, "question": 1, "reply": 2, "turn": 3, "working": 4, "idle": 5, "off": 6}

func order(m map[string]int, k string) int {
	if v, ok := m[k]; ok {
		return v
	}
	return len(m)
}

// byName: the base worktree first, then by slug.
func byName(a, b row) int {
	if a.WT.Base != b.WT.Base {
		if a.WT.Base {
			return -1
		}
		return 1
	}
	return strings.Compare(a.WT.Slug, b.WT.Slug)
}

// sortRows orders rows by k, ties by name; reversed, the whole order
// flips.
func sortRows(rows []row, k sortKey) {
	primary := func(a, b row) int { return 0 }
	switch k.By {
	case "running":
		primary = func(a, b row) int { return order(stackOrder, a.WT.Stack) - order(stackOrder, b.WT.Stack) }
	case "pr":
		primary = func(a, b row) int {
			switch {
			case a.PR == nil && b.PR == nil:
				return 0
			case a.PR == nil:
				return 1
			case b.PR == nil:
				return -1
			}
			return b.PR.Number - a.PR.Number
		}
	case "agent":
		primary = func(a, b row) int {
			return order(agentOrder, agentKind(a.WT.Agent)) - order(agentOrder, agentKind(b.WT.Agent))
		}
	}
	slices.SortStableFunc(rows, func(a, b row) int {
		c := primary(a, b)
		if c == 0 {
			c = byName(a, b)
		}
		if k.Rev {
			c = -c
		}
		return c
	})
}

// Agents.

// agentKind is what the page shows of an agent: its state, with done
// split into reply (waiting for a reply) and turn (your turn); no agent
// is off.
func agentKind(a *agent) string {
	if a == nil {
		return "off"
	}
	switch a.State {
	case "done":
		if a.Reply {
			return "reply"
		}
		return "turn"
	case "permission", "question", "working", "idle":
		return a.State
	}
	return "off"
}

var agentLabels = map[string]string{"reply": "waiting for reply", "turn": "your turn"}

func agentLabel(kind string) string {
	if l, ok := agentLabels[kind]; ok {
		return l
	}
	return kind
}

var agentGlyphs = map[string]string{"permission": "◆", "question": "◆", "reply": "◆", "turn": "·", "working": "●"}

func agentGlyph(kind string) string {
	if g, ok := agentGlyphs[kind]; ok {
		return g
	}
	return "○"
}

// agentClass per the spec's colour table: need is the accent, bold.
func agentClass(kind string) string {
	switch kind {
	case "permission", "question", "reply":
		return "need"
	case "working":
		return "blue"
	case "turn":
		return "yellow"
	}
	return "muted"
}

// needsInput: the kinds counted in the header and the tab title.
func needsInput(kind string) bool {
	return kind == "permission" || kind == "question" || kind == "reply"
}

// notePhrases are the kinds that notify, as the notification says them.
var notePhrases = map[string]string{
	"permission": "needs permission",
	"question":   "has a question",
	"reply":      "is waiting for your reply",
	"turn":       "your turn",
}

// Stack and services.

func stackClass(stack string) string {
	switch stack {
	case "up":
		return "green"
	case "partial":
		return "yellow"
	case "failed":
		return "red"
	}
	return "muted"
}

var stackGlyphs = map[string]string{"up": "●", "partial": "◐", "failed": "✗"}

func stackGlyph(stack string) string {
	if g, ok := stackGlyphs[stack]; ok {
		return g
	}
	return "○"
}

// stackText is the stack's state; partial says how many of the stack's
// services (app, worker, data) are active.
func stackText(wt worktree) string {
	if wt.Stack != "partial" {
		return wt.Stack
	}
	n, active := 0, 0
	for _, s := range wt.Services {
		if s.Role == "app" || s.Role == "worker" || s.Role == "data" {
			n++
			if strings.HasPrefix(s.State, "active/") {
				active++
			}
		}
	}
	return fmt.Sprintf("%d/%d", active, n)
}

// serviceLook is a unit state's glyph and class.
// serviceLook is a service state's glyph, colour class and plain word
// (the systemd state itself is the row's title).
func serviceLook(state string) (glyph, class, word string) {
	switch {
	case strings.HasPrefix(state, "active/"), state == "running":
		return "●", "green", "running"
	case strings.HasPrefix(state, "activating/"):
		return "◐", "yellow", "starting"
	case strings.HasPrefix(state, "deactivating/"):
		return "◐", "yellow", "stopping"
	case strings.HasPrefix(state, "reloading/"):
		return "◐", "yellow", "reloading"
	case strings.HasPrefix(state, "failed/"):
		return "✗", "red", "failed"
	case state == "on-demand":
		return "○", "muted", "on demand"
	case strings.HasPrefix(state, "inactive/"):
		return "○", "muted", "stopped"
	}
	return "○", "muted", state
}

// PRs.

var prStates = map[string]struct{ word, class string }{
	"draft":  {"draft", "muted"},
	"open":   {"ready", "green"},
	"merged": {"merged", "purple"},
	"closed": {"closed", "red"},
}

func prStateClass(state string) string { return prStates[state].class }

var reviews = map[string]struct{ word, class string }{
	"pending":           {"pending", "yellow"},
	"approved":          {"approved", "blue"},
	"changes_requested": {"changes requested", "red"},
}

func reviewClass(review string) string { return reviews[review].class }

var checks = map[string]struct{ glyph, class string }{
	"pass":    {"✓", "green"},
	"pending": {"◌", "yellow"},
	"fail":    {"✗", "red"},
}

func checksClass(c string) string { return checks[c].class }

// prView is a PR at a glance.
type prView struct {
	Number              int
	URL, Title          string
	Glyph, ChecksClass  string // "" without checks
	State, StateClass   string
	Review, ReviewClass string // "" when none, or the PR is closed
	Text, Line          string // the row's and the detail's wording
}

func newPR(p *pr) *prView {
	if p == nil {
		return nil
	}
	st, ok := prStates[p.State]
	if !ok {
		st.word, st.class = p.State, "muted"
	}
	v := &prView{Number: p.Number, URL: p.URL, Title: p.Title, State: st.word, StateClass: st.class,
		Glyph: checks[p.Checks].glyph, ChecksClass: checks[p.Checks].class}
	if p.State == "open" || p.State == "draft" {
		v.Review, v.ReviewClass = reviews[p.Review].word, reviews[p.Review].class
	}
	text := []string{fmt.Sprintf("#%d", p.Number)}
	line := []string{fmt.Sprintf("PR #%d %s", p.Number, v.State)}
	if v.Glyph != "" {
		text = append(text, v.Glyph)
		line = append(line, "checks "+v.Glyph)
	}
	v.Text = strings.Join(append(text, v.State), " ")
	if v.Review != "" {
		v.Text += " · " + v.Review
		line = append(line, v.Review)
	}
	v.Line = strings.Join(line, " · ")
	return v
}

// prLabel is the row's PR text, "—" without one.
func prLabel(p *pr) string {
	if p == nil {
		return "—"
	}
	return newPR(p).Text
}

// View models of the templates.

type pageView struct {
	Title, Prefix, Sel, Sort string
	Header                   headerView
	Projects                 projectsView
	Strip                    stripView
	Banner                   bannerView
	List                     listView
	Detail                   detailView
	Jobs                     jobsView
	Secrets                  *secretsCard // nil: every secret is set
}

// secretsCard asks for the project's unset secrets while up refuses one
// of them, then starts the base worktree. Later are unset secrets up
// doesn't need (an on-demand agent's): shown, but Start doesn't wait.
type secretsCard struct {
	Base, SecretURL, StartURL string
	Unset, Later              []secretView
}

// newSecretsCard is the card for secrets, nil while up needs none of the
// unset ones.
func newSecretsCard(prefix string, rows []rowView, secrets []secretRow) *secretsCard {
	c := &secretsCard{SecretURL: settingsURL(prefix) + "/secret"}
	for _, sec := range secrets {
		v := secretView{Name: sec.Name, Services: strings.Join(sec.Services, ", "), Delivery: sec.Delivery}
		switch {
		case sec.Set:
		case sec.Up:
			c.Unset = append(c.Unset, v)
		default:
			c.Later = append(c.Later, v)
		}
	}
	if len(c.Unset) == 0 {
		return nil
	}
	for _, r := range rows {
		if r.Base {
			c.Base, c.StartURL = r.Slug, "/p/"+prefix+"/"+r.Slug+"/act/up"
		}
	}
	return c
}

// title is the tab title: the count of agents needing input.
func title(need int) string {
	if need == 0 {
		return "RailSwitch"
	}
	return fmt.Sprintf("(%d) RailSwitch", need)
}

type headerView struct {
	Prefix string
	Need   int
}

type projectsView struct{ Items []switchItem }

type switchItem struct {
	N                        int // 1–9 jump; 0 past nine
	Prefix, URL              string
	Worktrees, Running, Need int
	Current                  bool
	Review                   bool // a package update waits for review
	WTs                      []switchWT
}

type switchWT struct {
	Slug, URL string
	Need      bool
}

type stripView struct{ Items []stripItem }

type stripItem struct{ Text, URL string }

// bannerView is the project's banner: Review links the review page while
// a package update waits; Signers are its signer warnings.
type bannerView struct {
	Lines    []string
	Review   string
	Signers  []string
	Template *bannerTemplate
}

// bannerTemplate is the notice of a changed template: Settings has its
// Accept.
type bannerTemplate struct{ Ref, URL string }

func signerWarningText(sw signerWarning) string {
	return "The signer of " + sw.Ref + " (" + sw.Library + ") is no longer trusted. The project keeps running what it has; replace it or re-check the library."
}

type listView struct {
	Sorts      []sortLink
	Rows       []rowView
	AddURL     string
	RefreshURL string
}

type sortLink struct {
	Label, URL string
	On, Rev    bool
}

// newList is the list of rows (sorted) with sel selected.
func newList(prefix string, rows []row, sel string, k sortKey) listView {
	base := "/p/" + prefix
	if sel != "" {
		base += "/" + sel
	}
	v := listView{AddURL: "/p/" + prefix + "/worktrees", RefreshURL: "/p/" + prefix + "/prs/refresh"}
	for _, by := range sortBys {
		next := sortKey{By: by}
		on := k.By == by
		if on {
			next.Rev = !k.Rev
		}
		v.Sorts = append(v.Sorts, sortLink{Label: by, URL: base + "?sort=" + next.String(), On: on, Rev: on && k.Rev})
	}
	for _, r := range rows {
		v.Rows = append(v.Rows, newRow(prefix, r, sel, k))
	}
	return v
}

type rowView struct {
	ID, Slug, URL, Branch             string
	Base, Sel                         bool
	PR                                *prView
	StackGlyph, StackText, StackClass string
	AgentGlyph, AgentText, AgentClass string
	AgentTitle                        string
	Stale                             bool // runs an older unit: "restart to apply"
}

func newRow(prefix string, r row, sel string, k sortKey) rowView {
	kind := agentKind(r.WT.Agent)
	v := rowView{
		ID: "row-" + r.WT.Slug, Slug: r.WT.Slug, URL: "/p/" + prefix + "/" + r.WT.Slug + k.query(),
		Branch: r.WT.Branch, Base: r.WT.Base, Sel: r.WT.Slug == sel, PR: newPR(r.PR),
		StackGlyph: stackGlyph(r.WT.Stack), StackText: stackText(r.WT), StackClass: stackClass(r.WT.Stack),
		AgentGlyph: agentGlyph(kind), AgentText: agentLabel(kind), AgentClass: agentClass(kind),
		Stale: r.WT.Stale,
	}
	if r.WT.Agent != nil {
		v.AgentTitle = r.WT.Agent.Name + ": " + r.WT.Agent.Message
	}
	return v
}

type detailView struct {
	Empty                             bool
	Slug, Branch                      string
	PR                                *prView
	StackGlyph, StackText, StackClass string
	Links                             []linkView
	Services                          []serviceView
	Agent                             *agentView
	Actions                           []actionView
	RemoveURL, TasksURL               string // RemoveURL "" for the base worktree
	Merged, Stale                     bool
}

type linkView struct{ Label, URL, Class string }

type serviceView struct{ Name, State, Word, Glyph, Class string }

type agentView struct{ Name, Glyph, Label, Class, Message string }

type actionView struct{ Label, URL string }

// newDetail is the selected worktree. Links: App (the main button), the
// project's, the stack's, all greyed while the stack is stopped; then PR
// and Logs, quieter.
func newDetail(prefix string, r row) detailView {
	wt := r.WT
	path := "/p/" + prefix + "/" + wt.Slug
	v := detailView{
		Slug: wt.Slug, Branch: wt.Branch, PR: newPR(r.PR),
		StackGlyph: stackGlyph(wt.Stack), StackText: stackText(wt), StackClass: stackClass(wt.Stack),
		TasksURL: path + "/tasks", Stale: wt.Stale,
	}
	dim := ""
	if wt.Stack == "stopped" {
		dim = " dim"
	}
	for _, group := range []string{"app", "project", "stack"} {
		for _, l := range wt.Links {
			if l.Group != group {
				continue
			}
			class := "ln"
			if group == "app" {
				class += " main"
			}
			v.Links = append(v.Links, linkView{Label: l.Label, URL: l.URL, Class: class + dim})
		}
	}
	if v.PR != nil && v.PR.URL != "" {
		v.Links = append(v.Links, linkView{Label: "PR", URL: v.PR.URL, Class: "ln quiet" + dim})
	}
	v.Links = append(v.Links, linkView{Label: "Logs", URL: logsURL, Class: "ln quiet"})
	for _, s := range wt.Services {
		g, c, word := serviceLook(s.State)
		v.Services = append(v.Services, serviceView{Name: s.Service, State: s.State, Word: word, Glyph: g, Class: c})
	}
	if a := wt.Agent; a != nil {
		kind := agentKind(a)
		v.Agent = &agentView{Name: a.Name, Glyph: agentGlyph(kind), Label: agentLabel(kind), Class: agentClass(kind), Message: a.Message}
	}
	for _, act := range []string{"up", "stop", "restart"} {
		v.Actions = append(v.Actions, actionView{Label: act, URL: path + "/act/" + act})
	}
	if !wt.Base {
		v.RemoveURL = path + "/remove"
		v.Merged = r.PR != nil && r.PR.State == "merged"
	}
	return v
}

type jobsView struct{ Jobs []jobView }

type jobView struct {
	ID, Label, State, Class, Started, CancelURL string
	Exit                                        int
	Running                                     bool
	Earlier                                     int // kept lines not shown
	Dropped                                     bool
	Conflict                                    bool // refused: the file changed since it was read
	Lines                                       []string
}

// conflictRe is how settings apply, project write, containerfile write
// and pack file write refuse a stale --if-hash.
var conflictRe = regexp.MustCompile(`changed (on disk )?since`)

var jobClasses = map[jobs.State]string{jobs.StateRunning: "blue", jobs.StateOK: "green", jobs.StateFailed: "red", jobs.StateCancelled: "muted"}

// newJobs is the jobs of worktree slug, the project's own and those of a
// worktree no longer in rows (a removed one's `wt rm` stays visible),
// newest first, each with its last viewLines lines.
func newJobs(snaps []jobs.Snapshot, rows []row, slug string) jobsView {
	var v jobsView
	for _, j := range snaps {
		other := j.Spec.Slug != slug && j.Spec.Slug != ""
		if other && slices.ContainsFunc(rows, func(r row) bool { return r.WT.Slug == j.Spec.Slug }) {
			continue
		}
		jv := jobView{
			ID: "job-" + j.ID, Label: j.Spec.Label, State: string(j.State), Class: jobClasses[j.State],
			Started: j.Started.Local().Format("15:04:05"), Exit: j.Exit,
			Running: j.State == jobs.StateRunning, CancelURL: "/jobs/" + j.ID + "/cancel",
			Dropped: j.Dropped, Lines: j.Lines,
			Conflict: j.State == jobs.StateFailed && slices.ContainsFunc(j.Lines, func(l string) bool { return conflictRe.MatchString(l) }),
		}
		if n := len(j.Lines); n > viewLines {
			jv.Earlier, jv.Lines = n-viewLines, j.Lines[n-viewLines:]
		}
		v.Jobs = append(v.Jobs, jv)
	}
	return v
}

// Dialog fragments.

type removeView struct {
	Slug, Dir, URL string
	Dirty          bool
}

type tasksView struct {
	Slug, URL string
	Tasks     []task
}

// colorTokens are the editable tokens, in the editor's order.
var colorTokens = []string{"bg", "bg2", "text", "muted", "accent", "blue", "green", "yellow"}

// colorTokensAll are every token of a mode (the CSS variables).
var colorTokensAll = append(slices.Clone(colorTokens), "sel", "border", "purple", "red")

var colorPresets = []string{"flexoki", "paper", "solar", "contrast"}

type colorsView struct{ Modes []colorsModeView }

type colorsModeView struct {
	Mode, Base string
	Presets    []string
	Fields     []colorField
	Warnings   []string
}

type colorField struct {
	Token, Value string
	Set          bool // changed from the preset
}

func newColors(d colorsDoc) colorsView {
	var v colorsView
	for _, m := range []struct {
		name string
		mode colorsMode
		warn []string
	}{{"light", d.Light, d.Warnings.Light}, {"dark", d.Dark, d.Warnings.Dark}} {
		mv := colorsModeView{Mode: m.name, Base: m.mode.Base, Presets: colorPresets, Warnings: m.warn}
		for _, tok := range colorTokens {
			_, set := m.mode.Overrides[tok]
			mv.Fields = append(mv.Fields, colorField{Token: tok, Value: m.mode.Colors[tok], Set: set})
		}
		v.Modes = append(v.Modes, mv)
	}
	return v
}
