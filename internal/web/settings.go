package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/jobs"
)

// The settings page (spec "Page"): staged edits of project.toml, judged
// by `settings preview` and applied all or none by a `settings apply`
// job; secrets and the raw file are jobs too. Every value that changes
// something travels on the job's stdin, never in argv.

// edit is one staged change, as `settings preview` and `settings apply`
// read it (docs/json.md). The page checks only that the list is one JSON
// array of known ops; the CLI checks which fields each op takes.
type edit struct {
	Op    string `json:"op"`
	Key   string `json:"key,omitempty"`
	Ref   string `json:"ref,omitempty"`
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
	Label string `json:"label,omitempty"`
	URL   string `json:"url,omitempty"`
}

var editOps = []string{"pack", "option", "env.set", "env.unset", "link.add", "link.remove", "link.hide", "link.show"}

// maxEdits is the largest edit list or project.toml the page posts.
const maxEdits = 1 << 20

var hashRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// parseEdits reads one JSON array of edits with known ops and returns it
// with its canonical bytes, the stdin the CLI gets.
func parseEdits(b []byte) ([]edit, []byte, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var edits []edit
	if err := dec.Decode(&edits); err != nil {
		return nil, nil, fmt.Errorf("edits: %w", err)
	}
	if edits == nil {
		return nil, nil, errors.New("edits: want a JSON array")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, nil, errors.New("edits: want one JSON array")
	}
	for i, e := range edits {
		if !slices.Contains(editOps, e.Op) {
			return nil, nil, fmt.Errorf("edit %d: unknown op %q", i, e.Op)
		}
	}
	out, err := json.Marshal(edits)
	return edits, out, err
}

// View models.

type settingsView struct {
	Title, Prefix string
	Header        headerView
	Projects      projectsView
	Strip         stripView
	Banner        bannerView
	Jobs          jobsView

	Error        string // project show failed: the page has nothing to edit
	Project      projectShow
	Pack         *optionView // the stack's pack, when it has another choice
	Options      []optionView
	Links        []linkRowView
	Secrets      []secretView
	SecretsError string
	Services     []string // for a new secret
	Staged       stagedView
	Template     settingsTemplate

	PreviewURL, SecretURL, FileURL string
}

type optionView struct {
	Key, Ref string
	Slot     bool
	Choices  []choiceView
}

type choiceView struct {
	Ref     string
	Current bool
}

type linkRowView struct {
	Label, URL, From string
	Hidden, Own      bool // Own: a project link, which Remove removes
}

type secretView struct {
	Name, Services, Delivery, When string
	Set                            bool
}

// stagedView is the staged-changes panel.
type stagedView struct {
	Cards      []cardView
	Count      int
	Blocked    bool // Apply is off
	Error      string
	Hash, Diff string
	Restart    string // "Restart the 2 affected running worktrees now"; "" for none
	RestartWho string
	ApplyText  string
	ApplyURL   string
	Install    *installView // the library packages the edits install; nil: none
}

type cardView struct {
	Index        int
	Title        string
	Label, Class string
	Reasons      []string
	Error        string
}

// impacts are the preview's labels (spec "settings preview").
var impacts = map[string]struct{ label, class string }{
	"none":    {"No data affected", "green"},
	"check":   {"Check before applying", "yellow"},
	"data":    {"Data not carried over", "red"},
	"blocked": {"Blocked", "red"},
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// newStaged is the panel for edits as doc judged them; err is a preview
// that failed as a whole.
func newStaged(applyURL string, show projectShow, edits []edit, doc previewDoc, err error) stagedView {
	n := len(edits)
	v := stagedView{Count: n, Hash: doc.Hash, Diff: doc.Diff, ApplyURL: applyURL,
		ApplyText: fmt.Sprintf("Apply %d %s", n, plural(n, "change", "changes"))}
	if err != nil {
		v.Error, v.Blocked = err.Error(), true
	} else {
		v.Install = newInstall(doc.Install)
	}
	judged := map[int]editResult{}
	for _, r := range doc.Edits {
		judged[r.Index] = r
	}
	for i, e := range edits {
		c := cardView{Index: i, Title: editTitle(show, e)}
		if r, ok := judged[i]; ok {
			im, known := impacts[r.Impact]
			if !known {
				im.label, im.class = r.Impact, "red"
			}
			c.Label, c.Class, c.Reasons, c.Error = im.label, im.class, r.Reasons, r.Error
			v.Blocked = v.Blocked || r.Impact == "blocked" || !known
		} else {
			v.Blocked = true
		}
		v.Cards = append(v.Cards, c)
	}
	if k := len(doc.Restart); k > 0 && err == nil {
		v.Restart = fmt.Sprintf("Restart the %d affected running %s now", k, plural(k, "worktree", "worktrees"))
		v.RestartWho = strings.Join(doc.Restart, ", ")
	}
	return v
}

// editTitle is a card's title: what changes, with the old value from
// show where it has one.
func editTitle(show projectShow, e edit) string {
	switch e.Op {
	case "pack":
		return fmt.Sprintf("Pack: %s → %s", show.Pack.Ref, e.Ref)
	case "option":
		old := "none"
		if i := slices.IndexFunc(show.Options, func(o optionRow) bool { return o.Key == e.Key }); i >= 0 {
			old = show.Options[i].Ref
		}
		return fmt.Sprintf("%s: %s → %s", e.Key, old, e.Ref)
	case "env.set":
		if i := slices.IndexFunc(show.Env, func(r envRow) bool { return r.Name == e.Name }); i >= 0 {
			return fmt.Sprintf("Env %s: %q → %q", e.Name, show.Env[i].Value, e.Value)
		}
		return fmt.Sprintf("Env %s: added, %q", e.Name, e.Value)
	case "env.unset":
		return "Env " + e.Name + ": removed"
	case "link.add":
		return "Link " + e.Label + ": added, " + e.URL
	case "link.remove":
		return "Link " + e.Label + ": removed"
	case "link.hide":
		return "Link " + e.Label + ": hidden"
	case "link.show":
		return "Link " + e.Label + ": shown"
	}
	return e.Op
}

func settingsURL(prefix string) string { return "/p/" + prefix + "/settings" }

// chrome fills the parts every page shares. s.mu is held.
func (s *Server) chrome(prefix string) (pageView, jobsView) {
	pv := s.pageData(prefix, "", sortKey{By: "name"})
	rows, _ := s.rows(prefix, sortKey{By: "name"}, "")
	return pv, newJobs(s.c.Jobs.Jobs(prefix, ""), rows, "")
}

// Handlers.

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	base := settingsURL(p.Prefix)
	s.mu.Lock()
	pv, jv := s.chrome(p.Prefix)
	s.mu.Unlock()
	v := settingsView{
		Title: "Settings · " + p.Prefix, Prefix: p.Prefix,
		Header: pv.Header, Projects: pv.Projects, Strip: pv.Strip, Banner: pv.Banner, Jobs: jv,
		PreviewURL: base + "/preview", SecretURL: base + "/secret", FileURL: base + "/file",
		Staged: newStaged(base+"/apply", projectShow{}, nil, previewDoc{}, nil),
	}
	show, err := readProjectShow(r.Context(), s.c.CLI, p.Workspace)
	if err != nil {
		v.Error = err.Error()
		s.html(w, http.StatusOK, "settings", v)
		return
	}
	v.Project = show
	v.Template = s.settingsTemplate(r.Context(), p)
	if len(show.Pack.Choices) > 1 {
		pv := optionView{Key: "pack", Ref: show.Pack.Ref, Slot: true}
		for _, c := range show.Pack.Choices {
			pv.Choices = append(pv.Choices, choiceView{Ref: c, Current: c == show.Pack.Ref})
		}
		v.Pack = &pv
	}
	for _, o := range show.Options {
		ov := optionView{Key: o.Key, Ref: o.Ref, Slot: o.Slot}
		for _, c := range o.Choices {
			ov.Choices = append(ov.Choices, choiceView{Ref: c, Current: c == o.Ref})
		}
		v.Options = append(v.Options, ov)
	}
	for _, l := range show.Links {
		v.Links = append(v.Links, linkRowView{Label: l.Label, URL: l.URL, From: l.From, Hidden: l.Hidden, Own: l.From == "project"})
	}
	secrets, err := readSecrets(r.Context(), s.c.CLI, p.Workspace)
	if err != nil {
		v.SecretsError = err.Error()
	}
	for _, sec := range secrets {
		sv := secretView{Name: sec.Name, Services: strings.Join(sec.Services, ", "), Delivery: sec.Delivery, Set: sec.Set}
		if sec.Created != nil {
			sv.When, _, _ = strings.Cut(*sec.Created, "T")
		}
		v.Secrets = append(v.Secrets, sv)
	}
	if ov, err := s.overviewOf(r.Context(), p.Prefix, p.Workspace); err == nil {
		for _, wt := range ov.Worktrees {
			for _, svc := range wt.Services {
				if !slices.Contains(v.Services, svc.Service) {
					v.Services = append(v.Services, svc.Service)
				}
			}
		}
	}
	s.html(w, http.StatusOK, "settings", v)
}

// settingsPreview answers the staged-changes panel for the posted edit
// list, judged by `settings preview`; the previewed hash is also in
// X-Settings-Hash.
func (s *Server) settingsPreview(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxEdits))
	if err != nil {
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	edits, in, err := parseEdits(b)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	applyURL := settingsURL(p.Prefix) + "/apply"
	if len(edits) == 0 {
		s.html(w, http.StatusOK, "staged", newStaged(applyURL, projectShow{}, nil, previewDoc{}, nil))
		return
	}
	show, _ := readProjectShow(r.Context(), s.c.CLI, p.Workspace) // only for the old values in the titles
	doc, err := readPreview(r.Context(), s.c.CLI, p.Workspace, in)
	w.Header().Set("X-Settings-Hash", doc.Hash)
	s.html(w, http.StatusOK, "staged", newStaged(applyURL, show, edits, doc, err))
}

// settingsApply runs `settings apply --if-hash <h> [--accept-install <r>]
// [--restart]` with the edit list on stdin, a project-level job. Edits
// that install a library package need the Install review's hash and,
// with flags, the tick.
func (s *Server) settingsApply(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2*maxEdits)
	edits, in, err := parseEdits([]byte(r.PostFormValue("edits")))
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if len(edits) == 0 {
		http.Error(w, "nothing staged", http.StatusUnprocessableEntity)
		return
	}
	hash := r.PostFormValue("hash")
	if !hashRe.MatchString(hash) {
		http.Error(w, "preview the changes first: no project.toml hash", http.StatusUnprocessableEntity)
		return
	}
	argv := []string{s.c.Exe, "-C", p.Workspace, "settings", "apply", "--if-hash", hash}
	label := "settings apply"
	// Without --accept-install the CLI installs no library package, so
	// only a form that names an Install review needs the preview again.
	if posted := r.PostFormValue("accept_install"); posted != "" {
		doc, err := readPreview(r.Context(), s.c.CLI, p.Workspace, in)
		if err != nil {
			http.Error(w, "preview failed: "+err.Error(), http.StatusUnprocessableEntity)
			return
		}
		if err := checkInstall(doc.Install, posted, r.PostFormValue("ack")); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		argv = append(argv, "--accept-install", doc.Install.Hash)
		label += " --accept-install"
	}
	if r.PostFormValue("restart") != "" {
		argv = append(argv, "--restart")
		label += " --restart"
	}
	s.start(w, r, jobs.Spec{Project: p.Prefix, Label: label, Dir: p.Workspace, Argv: argv}, in, settingsURL(p.Prefix))
}

// settingsSecret runs `secret set <name>`, or with add `secret add <name>
// --service <id>… [--env VAR]`, with the value on stdin.
func (s *Server) settingsSecret(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	name, value := r.PostFormValue("name"), r.PostFormValue("value")
	if badName(name) {
		http.Error(w, "bad secret name", http.StatusUnprocessableEntity)
		return
	}
	if value == "" {
		http.Error(w, "a value is required", http.StatusUnprocessableEntity)
		return
	}
	argv := []string{s.c.Exe, "-C", p.Workspace, "secret"}
	label := "secret set " + name
	if r.PostFormValue("add") == "" {
		argv = append(argv, "set", name)
	} else {
		label = "secret add " + name
		argv = append(argv, "add", name)
		services := r.PostForm["services"]
		if len(services) == 0 {
			http.Error(w, "pick at least one service", http.StatusUnprocessableEntity)
			return
		}
		for _, svc := range services {
			if badName(svc) {
				http.Error(w, "bad service name", http.StatusUnprocessableEntity)
				return
			}
			argv = append(argv, "--service", svc)
		}
		if env := r.PostFormValue("env"); env != "" {
			if badName(env) {
				http.Error(w, "bad env variable name", http.StatusUnprocessableEntity)
				return
			}
			argv = append(argv, "--env", env)
		}
	}
	s.start(w, r, jobs.Spec{Project: p.Prefix, Label: label, Dir: p.Workspace, Argv: argv}, []byte(value), settingsURL(p.Prefix))
}

type settingsFileView struct {
	Title, Prefix string
	Header        headerView
	Projects      projectsView
	Strip         stripView
	Banner        bannerView
	Jobs          jobsView

	Error            string
	Text, Hash       string
	URL, SettingsURL string
}

func (s *Server) settingsFilePage(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	pv, jv := s.chrome(p.Prefix)
	s.mu.Unlock()
	v := settingsFileView{
		Title: "project.toml · " + p.Prefix, Prefix: p.Prefix,
		Header: pv.Header, Projects: pv.Projects, Strip: pv.Strip, Banner: pv.Banner, Jobs: jv,
		URL: settingsURL(p.Prefix) + "/file", SettingsURL: settingsURL(p.Prefix),
	}
	if show, err := readProjectShow(r.Context(), s.c.CLI, p.Workspace); err != nil {
		v.Error = err.Error()
	} else {
		v.Text, v.Hash = show.File, show.Hash
	}
	s.html(w, http.StatusOK, "settingsfile", v)
}

// settingsFile runs `project write --if-hash <h>` with the text on stdin,
// a project-level job. A browser posts a textarea with CRLF line breaks;
// they become LF again.
func (s *Server) settingsFile(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4*maxEdits)
	hash, text := r.PostFormValue("hash"), strings.ReplaceAll(r.PostFormValue("text"), "\r\n", "\n")
	if !hashRe.MatchString(hash) {
		http.Error(w, "no project.toml hash: reload the page", http.StatusUnprocessableEntity)
		return
	}
	if strings.TrimSpace(text) == "" {
		http.Error(w, "project.toml can't be empty", http.StatusUnprocessableEntity)
		return
	}
	if len(text) > maxEdits {
		http.Error(w, "project.toml is larger than 1 MiB", http.StatusUnprocessableEntity)
		return
	}
	s.start(w, r, jobs.Spec{
		Project: p.Prefix, Label: "project write", Dir: p.Workspace,
		Argv: []string{s.c.Exe, "-C", p.Workspace, "project", "write", "--if-hash", hash},
	}, []byte(text), settingsURL(p.Prefix)+"/file")
}
