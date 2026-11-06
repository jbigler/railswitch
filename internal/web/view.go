package web

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jbigler/railswitch/internal/jobs"
)

//go:embed templates/*.tmpl
var templateFiles embed.FS

var tmpl = template.Must(template.New("").Funcs(template.FuncMap{"cap": capFirst}).ParseFS(templateFiles, "templates/*.tmpl"))

// capFirst is s with its first letter upper case: a button's label from a
// word that is also a command or job label ("start", "shell").
func capFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}

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
	WT      worktree
	PR      *pr
	Stacked *ghStackRow // nil: its branch is in no gh stack (or gh stack is missing)
	Now     time.Time   // the clock the tests' "5m ago" counts from
}

// Sorting.

// sortKey is the list's order: By one of sortBys, Rev reversed. Its text
// form, the ?sort= value, is the key with a leading "-" when reversed.
type sortKey struct {
	By  string
	Rev bool
}

// Not "running": the list groups worktrees by state (listGroups), so each
// group is sorted by one of these.
var sortBys = []string{"name", "pr", "agent"}

var sortLabels = map[string]string{"name": "Name", "pr": "PR", "agent": "Agent"}

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
// split into reply (waiting for a reply) and turn (your turn); no agent,
// or one whose container doesn't run (its status file outlives it), is
// off.
func agentKind(a *agent) string {
	if a == nil || !a.Running {
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
	case strings.HasPrefix(state, "maintenance/"), strings.HasPrefix(state, "refreshing/"):
		return "◐", "yellow", "busy"
	}
	return "○", "muted", "unknown"
}

// Tests.

// testsView is a worktree's last test run (D158): "✓ passed 5m ago", "✗
// failed 2h ago", "— never"; Title its exit code, times and commit.
type testsView struct {
	Glyph, Text, Class, Title string
	Stale                     bool // HEAD moved since the run
}

// newTests is t's view at now; nil when the project has no test task.
func newTests(t *tests, now time.Time) *testsView {
	switch {
	case t == nil:
		return nil
	case t.Last == nil:
		return &testsView{Glyph: "—", Text: "never", Class: "muted", Title: "The test task hasn't run here"}
	}
	v := &testsView{Glyph: "✓", Text: "passed", Class: "green", Stale: t.Stale}
	if t.State != "passed" {
		v.Glyph, v.Text, v.Class = "✗", "failed", "red"
	}
	v.Text += " " + ago(now.Sub(t.Last.EndedAt))
	v.Title = fmt.Sprintf("exit %d · %s · took %s", t.Last.ExitCode, t.Last.EndedAt.Local().Format("2006-01-02 15:04"), t.Last.EndedAt.Sub(t.Last.StartedAt).Round(time.Second))
	if c := t.Last.Commit; len(c) >= 12 {
		v.Title += " · at " + c[:12]
	}
	if t.Stale {
		v.Title += " · HEAD has moved since"
	}
	return v
}

// ago is d in its largest whole unit: "just now", "5m ago", "3h ago",
// "2d ago".
func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + "m ago"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h ago"
	}
	return strconv.Itoa(int(d/(24*time.Hour))) + "d ago"
}

// PRs.

var prStates = map[string]struct{ word, class string }{
	"draft":  {"draft", "muted"},
	"open":   {"ready", "green"},
	"merged": {"merged", "purple"},
	"closed": {"closed", "red"},
	"queued": {"queued", "blue"}, // in a merge queue (gh stack)
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
	// Pane is the one a narrow window shows: list, or detail when the URL
	// names a worktree (docs/dashboard.md Narrow windows). ListURL is the
	// detail's way back.
	Pane, ListURL string
	Header        headerView
	Projects      projectsView
	Strip         stripView
	Banner        bannerView
	List          listView
	Detail        detailView
	Usage         *usageView // nil: no agent usage in the chart's days
	Jobs          jobsView
	Secrets       *secretsCard // nil: every secret is set
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
	Defaults []bannerDefault // ejected Containerfiles whose default changed
}

// bannerDefault is one ejected Containerfile whose default changed: the
// Images editor compares and resets it.
type bannerDefault struct{ Name, URL string }

// bannerTemplate is the notice of a changed template: Settings has its
// Accept.
type bannerTemplate struct{ Ref, URL string }

func signerWarningText(sw signerWarning) string {
	return "The signer of " + sw.Ref + " (" + sw.Library + ") is no longer trusted. The project keeps running what it has; replace it or re-check the library."
}

type listView struct {
	Sorts      []sortLink
	Rows       []rowView
	Groups     []listGroup // Rows by state, each in Rows' order; no empty group
	AddURL     string
	RefreshURL string
}

// listGroup is one heading of the list: the worktrees that need you (an
// agent waits on you, or the stack failed), the running ones, the rest.
type listGroup struct {
	Name string
	Rows []rowView
}

var groupNames = []string{"Needs you", "Running", "Stopped"}

// listGroups puts rows under groupNames, keeping their order.
func listGroups(rows []rowView) []listGroup {
	by := make([][]rowView, len(groupNames))
	for _, r := range rows {
		i := 2
		switch {
		case r.Need != "" || r.stack == "failed":
			i = 0
		case r.stack == "up" || r.stack == "partial":
			i = 1
		}
		by[i] = append(by[i], r)
	}
	var out []listGroup
	for i, rs := range by {
		if len(rs) > 0 {
			out = append(out, listGroup{Name: groupNames[i], Rows: rs})
		}
	}
	return out
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
		v.Sorts = append(v.Sorts, sortLink{Label: sortLabels[by], URL: base + "?sort=" + next.String(), On: on, Rev: on && k.Rev})
	}
	for _, r := range rows {
		v.Rows = append(v.Rows, newRow(prefix, r, sel, k))
	}
	v.Groups = listGroups(v.Rows)
	return v
}

// stackedView is a worktree branch's gh stack for the detail, in gh stack
// view's words: "Stack #10350", "Base: master", "22 branches (8 merged)";
// then Open top (newest) to bottom, the merged ones apart, the trunk.
type stackedView struct {
	Number    int
	NumberURL string // the stack on GitHub; "" without a number or repo
	Trunk     string
	Count     string // "22 branches (8 merged)"
	Restack   bool   // a branch needs a rebase
	Open      []stackedBranch
	Merged    []stackedBranch
	Error     string // gh stack couldn't say
}

type stackedBranch struct {
	Name                         string
	PR                           *prView // nil without a PR
	PRURL                        string  // the PR, linked through the project's PR URL
	Current, NeedsRebase, Queued bool
}

// newStacked is r's stack, nil when it has none; repo is the PR URL
// prefix ("https://github.com/o/n/pull/") the PR numbers link to, "" for
// no links.
func newStacked(r row, repo string) *stackedView {
	st := r.Stacked
	if st == nil {
		return nil
	}
	if st.Stack == nil {
		return &stackedView{Error: st.Error}
	}
	v := &stackedView{Trunk: st.Stack.Trunk, Number: st.Stack.Number}
	if v.Number > 0 && repo != "" {
		v.NumberURL = strings.TrimSuffix(repo, "pull/") + "stack/" + strconv.Itoa(v.Number)
	}
	queued := 0
	for _, b := range st.Stack.Branches {
		sb := stackedBranch{Name: b.Name, Current: b.Current, NeedsRebase: b.NeedsRebase, Queued: b.Queued}
		if p := b.PR; p != nil {
			sb.PR = newPR(&pr{Number: p.Number, State: p.State, Checks: p.Checks, Review: p.Review})
			if repo != "" {
				sb.PRURL = repo + strconv.Itoa(p.Number)
			}
		}
		v.Restack = v.Restack || b.NeedsRebase
		if b.Queued {
			queued++
		}
		if b.Merged && !b.Current {
			v.Merged = append(v.Merged, sb)
			continue
		}
		v.Open = append([]stackedBranch{sb}, v.Open...) // top first
	}
	n := len(st.Stack.Branches)
	v.Count = fmt.Sprintf("%d branches", n)
	if n == 1 {
		v.Count = "1 branch"
	}
	var notes []string
	if len(v.Merged) > 0 {
		notes = append(notes, fmt.Sprintf("%d merged", len(v.Merged)))
	}
	if queued > 0 {
		notes = append(notes, fmt.Sprintf("%d queued", queued))
	}
	if len(notes) > 0 {
		v.Count += " (" + strings.Join(notes, ", ") + ")"
	}
	return v
}

// stackedRow is the list's short form: the current branch's place among
// all of the stack's branches, bottom first ("15/22"); "" without a stack.
func stackedRow(r row) string {
	if r.Stacked == nil || r.Stacked.Stack == nil {
		return ""
	}
	bs := r.Stacked.Stack.Branches
	i := slices.IndexFunc(bs, func(b ghStackBranch) bool { return b.Current })
	return fmt.Sprintf("%d/%d", i+1, len(bs))
}

// projectPRRepo is the PR URL prefix any row's PR has ("…/pull/"): every
// worktree of a project is on one repository, so a stack's PRs link even
// when the selected worktree's own branch has none yet.
func projectPRRepo(rows []row) string {
	for _, r := range rows {
		if p := prRepo(r.PR); p != "" {
			return p
		}
	}
	return ""
}

// prRepo is the PR URL prefix of p's URL ("…/pull/"), "" without one.
func prRepo(p *pr) string {
	if p == nil {
		return ""
	}
	i := strings.LastIndex(p.URL, "/pull/")
	if i < 0 {
		return ""
	}
	return p.URL[:i+len("/pull/")]
}

type rowView struct {
	ID, Slug, URL, Branch             string
	Stacked                           string // "2/3" in a gh stack, else ""
	Base, Sel                         bool
	PR                                *prView
	StackGlyph, StackText, StackClass string
	AgentGlyph, AgentText, AgentClass string
	AgentTitle                        string
	Need                              string     // the agent waits on you: its label ("permission"), shown by the name
	Stale                             bool       // runs an older unit: "restart to apply"
	Tests                             *testsView // nil: no test task
	stack                             string     // the stack's state, for listGroups
}

func newRow(prefix string, r row, sel string, k sortKey) rowView {
	kind := agentKind(r.WT.Agent)
	v := rowView{
		ID: "row-" + r.WT.Slug, Slug: r.WT.Slug, URL: "/p/" + prefix + "/" + r.WT.Slug + k.query(),
		Branch: r.WT.Branch, Base: r.WT.Base, Sel: r.WT.Slug == sel, PR: newPR(r.PR),
		StackGlyph: stackGlyph(r.WT.Stack), StackText: stackText(r.WT), StackClass: stackClass(r.WT.Stack),
		AgentGlyph: agentGlyph(kind), AgentText: agentLabel(kind), AgentClass: agentClass(kind),
		Stale: r.WT.Stale, Stacked: stackedRow(r), Tests: newTests(r.WT.Tests, r.Now),
		stack: r.WT.Stack,
	}
	if kind != "off" { // an agent that isn't running says nothing
		v.AgentTitle = r.WT.Agent.Name + ": " + r.WT.Agent.Message
	}
	if needsInput(kind) {
		v.Need = agentLabel(kind)
	}
	return v
}

type detailView struct {
	Empty                             bool
	Slug, Branch                      string
	PR                                *prView
	StackGlyph, StackText, StackClass string
	Links                             []linkView
	AppURL                            string // the first app link: the header's Open app; "" for none
	Services                          []serviceView
	ServicesUp                        string // "2/9": services running of all, for the Services tab
	Agent                             *agentView
	Actions                           []actionView
	Terms                             []actionView // terminal panels: URL is the WebSocket path (D149)
	AgentTerms                        []agentTerm  // an agent terminal per configured agent
	TermWindowURL                     string       // the term action: a window on the desktop
	AgentStopURL                      string       // POST agent=<name>: railswitch agent stop <name> (D176)
	AgentSetupURL                     string       // POST agent=<name>: railswitch agent setup --again <name> (D187)
	RunningAgent                      *agentTerm   // the agent running in this worktree (one at most, D62); nil: none
	RemoveURL, TasksURL               string       // RemoveURL "" for the base worktree
	DownURL                           string
	PRPageURL                         string // the worktree's PR page (D152)
	AgentDefaultURL                   string // the agent default config dialog (D135)
	SnapshotsURL                      string // the database snapshots dialog (D157); "" without a data service
	Stacked                           *stackedView
	SwitchURL                         string // with Stacked: posts a branch to check out
	Switching                         string // the branch a running stack switch checks out; "" for none
	Merged, Stale                     bool
	Tests                             *testsView // nil: no test task
	TaskURL                           string     // posts a task to run as a job
	Queue                             queueList  // the agent work queue (D159)
	Usage                             string     // its agents' usage (D162); "" for none
	Runaway                           bool       // that usage is far above its usual day
}

type linkView struct{ Label, URL, Class string }

// serviceView is a service row: LogsURL (Dozzle, by container name) while
// its container runs, RestartURL while a restartable one runs. A oneshot
// (deps) logs to its unit's journal, which Dozzle never sees: LogsPost, in
// any state, starts a job that prints it (D186).
type serviceView struct{ Name, State, Word, Glyph, Class, LogsURL, LogsPost, RestartURL, Confirm string }

type agentView struct{ Name, Glyph, Label, Class, Message string }

type actionView struct{ Label, URL string }

// agentTerm opens one agent's terminal panel; Running while its container
// runs.
type agentTerm struct {
	Name, URL string
	Running   bool
}

// newDetail is row r's detail; repo is the project's PR URL prefix
// (projectPRRepo), which the stack's PR numbers link through. Links: App
// (the main button), the project's, the stack's, all greyed while the
// stack is stopped; then PR, Review app and Logs, quieter.
func newDetail(prefix string, r row, repo string) detailView {
	wt := r.WT
	path := "/p/" + prefix + "/" + wt.Slug
	v := detailView{
		Slug: wt.Slug, Branch: wt.Branch, PR: newPR(r.PR),
		StackGlyph: stackGlyph(wt.Stack), StackText: stackText(wt), StackClass: stackClass(wt.Stack),
		TasksURL: path + "/tasks", PRPageURL: path + "/pr", AgentDefaultURL: path + "/agent-default", Stale: wt.Stale,
		Stacked: newStacked(r, repo), Tests: newTests(wt.Tests, r.Now), TaskURL: path + "/task",
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
				if v.AppURL == "" {
					v.AppURL = l.URL
				}
			}
			v.Links = append(v.Links, linkView{Label: l.Label, URL: l.URL, Class: class + dim})
		}
	}
	if v.PR != nil && v.PR.URL != "" {
		v.Links = append(v.Links, linkView{Label: "PR", URL: v.PR.URL, Class: "ln quiet" + dim})
	}
	if r.PR != nil && r.PR.ReviewApp != "" {
		// Deployed by the PR's own CI, not this stack: never greyed.
		v.Links = append(v.Links, linkView{Label: "Review app", URL: r.PR.ReviewApp, Class: "ln quiet"})
	}
	v.Links = append(v.Links, linkView{Label: "Logs", URL: logsURL, Class: "ln quiet"})
	up := 0
	for _, s := range wt.Services {
		g, c, word := serviceLook(s.State)
		sv := serviceView{Name: s.Service, State: s.State, Word: word, Glyph: g, Class: c}
		if s.Role == "deps" {
			sv.LogsPost = path + "/logs/" + s.Service
		} else if (word == "running" || word == "starting") && s.Container != "" {
			sv.LogsURL = logsURL + "show?name=" + url.QueryEscape(s.Container)
			if s.Restartable {
				sv.RestartURL = path + "/restart/" + s.Service
				if s.Role == "agent" { // its session goes with it
					sv.Confirm = "Restart " + s.Service + "? Its session ends: what the agent is doing stops, and it starts fresh."
				}
			}
		}
		v.Services = append(v.Services, sv)
		if word == "running" {
			up++
		}
	}
	v.ServicesUp = fmt.Sprintf("%d/%d", up, len(v.Services))
	if a := wt.Agent; a != nil {
		kind := agentKind(a)
		v.Agent = &agentView{Name: a.Name, Glyph: agentGlyph(kind), Label: agentLabel(kind), Class: agentClass(kind)}
		if kind != "off" { // a stopped agent's message is its status file's last
			v.Agent.Message = a.Message
		}
	}
	v.Queue = newQueue(prefix, wt)
	// What the stack's state allows: a stopped one starts, a running one
	// restarts or stops, a partial or failed one any of the three.
	acts := []string{"restart", "stop"}
	switch wt.Stack {
	case "stopped":
		acts = []string{"up"}
	case "partial", "failed":
		acts = []string{"up", "restart", "stop"}
	}
	for _, act := range acts {
		v.Actions = append(v.Actions, actionView{Label: worktreeActions[act].label, URL: path + "/act/" + act})
	}
	v.TermWindowURL = path + "/act/term"
	v.AgentStopURL = path + "/agent-stop"
	v.AgentSetupURL = path + "/agent-setup"
	tty := "/p/" + prefix + "/tty/" + wt.Slug + "/"
	// One per configured agent: each is a service agent-<name> (on demand
	// or not), so the page can open any of them.
	for _, s := range wt.Services {
		if name, ok := strings.CutPrefix(s.Service, "agent-"); ok && s.Role == "agent" {
			_, _, word := serviceLook(s.State)
			v.AgentTerms = append(v.AgentTerms, agentTerm{Name: name, URL: tty + "agent?agent=" + url.QueryEscape(name), Running: word == "running"})
		}
	}
	if len(v.AgentTerms) == 0 { // none listed: the default agent, or the one the overview names
		a := agentTerm{Name: "agent", URL: tty + "agent"}
		if wt.Agent != nil {
			a.Name, a.URL, a.Running = wt.Agent.Name, a.URL+"?agent="+url.QueryEscape(wt.Agent.Name), wt.Agent.Running
		}
		v.AgentTerms = []agentTerm{a}
	}
	for i := range v.AgentTerms {
		if v.AgentTerms[i].Running && v.RunningAgent == nil {
			v.RunningAgent = &v.AgentTerms[i]
		}
	}
	if slices.ContainsFunc(wt.Services, func(s service) bool { return s.Service == "nvim" }) {
		v.Terms = append(v.Terms, actionView{Label: "nvim", URL: tty + "nvim"})
	}
	v.Terms = append(v.Terms, actionView{Label: "shell", URL: tty + "shell"})
	if v.Stacked != nil && v.Stacked.Error == "" {
		v.SwitchURL = path + "/stack/switch"
	}
	if slices.ContainsFunc(wt.Services, func(s service) bool { return s.Role == "data" }) {
		v.SnapshotsURL = path + "/snapshots"
	}
	v.DownURL = path + "/down"
	if !wt.Base {
		v.RemoveURL = path + "/remove"
		v.Merged = r.PR != nil && r.PR.State == "merged"
	}
	return v
}

// jobsView is the jobs section; Fold (the worktree page) folds away the
// output of a job that ended well, which other pages show as their result.
type jobsView struct {
	Jobs []jobView
	Fold bool
}

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

type downView struct{ Slug, URL, SnapshotsURL string }

type removeView struct {
	Slug, Dir, URL string
	Dirty          bool
}

type agentDefaultView struct {
	Slug, PromoteURL, ApplyURL string
	Agents                     []agentDefaultRow
}

type agentDefaultRow struct {
	agentRow
	Promoted string // the default's date, as the page shows it
}

func newAgentDefault(prefix, slug string, rows []agentRow) agentDefaultView {
	path := "/p/" + prefix + "/" + slug + "/agent-default/"
	v := agentDefaultView{Slug: slug, PromoteURL: path + "promote", ApplyURL: path + "apply"}
	for _, r := range rows {
		if !r.Config {
			continue
		}
		ar := agentDefaultRow{agentRow: r}
		if r.Default != nil {
			ar.Promoted = r.Default.Promoted.Local().Format("Jan 2 15:04")
		}
		v.Agents = append(v.Agents, ar)
	}
	return v
}

type tasksView struct {
	Slug, URL                 string // "" without a worktree: no tasks or actions
	TTY                       string // the worktree's terminal path, for a terminal task (D175)
	SettingsURL               string // the palette's "new task" goes to its Tasks card
	Tasks                     []task
	Actions, Worktrees, Pages []actionView
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
