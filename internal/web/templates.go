package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/jbigler/railswitch/internal/jobs"
)

// Templates (Stage 7d, D108–D111): the Library page lists `template ls`
// and shows one `template show`; Settings saves the project as a template
// (`template export`) and accepts a changed one (`template check
// --accept`); the new project page starts from one, answering its
// questions with `--answer key=value`.

// templateRefRe is a template ref: name@N on this machine, <alias>:name@N
// in a library. local: is a workspace's packages, never a template.
var templateRefRe = regexp.MustCompile(`^(?:[a-z][a-z0-9-]{0,19}:)?[a-z][a-z0-9-]*@[1-9][0-9]*$`)

func validTemplateRef(ref string) bool {
	return templateRefRe.MatchString(ref) && !strings.HasPrefix(ref, "local:")
}

// askKeyRe is an ask key: a dotted project.toml path (options.cache,
// versions.postgres, env.TZ).
var askKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

var templateVersionRe = regexp.MustCompile(`^[1-9][0-9]{0,8}$`)

var templateSHARe = regexp.MustCompile(`^(?:sha256:)?[0-9a-f]{64}$`)

// templateLs is `template ls`.
type templateLs struct {
	Templates []templateRow `json:"templates"`
}

type templateRow struct {
	Ref         string   `json:"ref"`
	Name        string   `json:"name"`
	Version     int      `json:"version"`
	Description string   `json:"description"`
	Source      string   `json:"source"` // machine, or library <alias>
	Packages    []string `json:"packages"`
}

// templateDoc is `template show <ref>`.
type templateDoc struct {
	Ref            string        `json:"ref"`
	SHA256         string        `json:"sha256"`
	StackTOML      string        `json:"stack_toml"`
	Ask            []templateAsk `json:"ask"`
	Secrets        []string      `json:"secrets"`
	Containerfiles []string      `json:"containerfiles"`
	Readme         string        `json:"readme"`
}

// templateAsk is one question; Answer is the dry run's, the value the
// project gets.
type templateAsk struct {
	Key     string `json:"key"`
	Prompt  string `json:"prompt"`
	Default string `json:"default"`
	Answer  string `json:"answer"`
}

// templateStamp is the template a project was made from.
type templateStamp struct {
	Ref    string `json:"ref"`
	SHA256 string `json:"sha256"`
}

func readTemplates(ctx context.Context, c CLI) (templateLs, error) {
	return readJSON[templateLs](ctx, c, "template", "ls")
}

func readTemplate(ctx context.Context, c CLI, ref string) (templateDoc, error) {
	return readJSON[templateDoc](ctx, c, "template", "show", ref)
}

// templateItems are the listed templates the page can name: a row whose
// ref is malformed is left out.
func templateItems(doc templateLs) []templateRow {
	var out []templateRow
	for _, t := range doc.Templates {
		if validTemplateRef(t.Ref) {
			out = append(out, t)
		}
	}
	return out
}

// askField is a question as the new project page shows it: Name is its
// form field.
type askField struct {
	Key, Prompt, Default, Name string
}

func answerField(key string) string { return "answer-" + key }

// askFields are t's questions with a well-formed key; one that isn't
// can't be answered.
func askFields(t templateDoc) []askField {
	var out []askField
	for _, a := range t.Ask {
		if askKeyRe.MatchString(a.Key) {
			out = append(out, askField{Key: a.Key, Prompt: a.Prompt, Default: a.Default, Name: answerField(a.Key)})
		}
	}
	return out
}

// templateAnswers reads the posted answer-<key> fields against t's
// questions, in their order: a key t doesn't ask, or a value with a
// control character, is refused.
func templateAnswers(r *http.Request, t templateDoc) ([]string, error) {
	fields := askFields(t)
	asked := map[string]bool{}
	for _, a := range fields {
		asked[a.Key] = true
	}
	for k := range r.PostForm {
		if key, ok := strings.CutPrefix(k, "answer-"); ok && !asked[key] {
			return nil, fmt.Errorf("the template %s doesn't ask %q", t.Ref, key)
		}
	}
	var out []string
	for _, a := range fields {
		vals, ok := r.PostForm[a.Name]
		if !ok {
			continue
		}
		if len(vals) != 1 || strings.ContainsFunc(vals[0], unicode.IsControl) {
			return nil, fmt.Errorf("%s: one value, no line break", a.Key)
		}
		out = append(out, a.Key+"="+vals[0])
	}
	return out, nil
}

// Library page.

type libTemplateItem struct {
	templateRow
	URL     string
	Current bool
}

type libTemplateDetail struct {
	Row   templateRow
	Doc   templateDoc
	Error string
}

func libTemplateURL(prefix, ref string) string {
	return libraryURL(prefix) + "?template=" + url.QueryEscape(ref)
}

// libraryTemplates fills the Templates section and, with ?template=, the
// selected one's `template show`. It answers false after a 404 it wrote.
func (s *Server) libraryTemplates(ctx context.Context, w http.ResponseWriter, r *http.Request, v *libraryView) bool {
	doc, err := readTemplates(ctx, s.c.CLI)
	if err != nil {
		v.TemplatesError = err.Error()
	}
	sel := r.URL.Query().Get("template")
	d := &libTemplateDetail{}
	found := false
	for _, t := range templateItems(doc) {
		v.Templates = append(v.Templates, libTemplateItem{templateRow: t, URL: libTemplateURL(v.Prefix, t.Ref), Current: t.Ref == sel})
		if t.Ref == sel {
			d.Row, found = t, true
		}
	}
	if sel == "" {
		return true
	}
	if !found {
		http.Error(w, "unknown template", http.StatusNotFound)
		return false
	}
	if d.Doc, err = readTemplate(ctx, s.c.CLI, sel); err != nil {
		d.Error = err.Error()
	}
	v.TemplateSel = d
	return true
}

// Settings.

// settingsTemplate is the Settings page's template notice and Save as
// template, from the overview's template fields and, when the template
// changed, its `template show` now.
type settingsTemplate struct {
	Ref, Since string // Since: the recorded sha, short
	Update     bool
	SHA256     string // the template's sha now, which Accept records
	Error      string
	AcceptURL  string
	ExportURL  string
}

func (s *Server) settingsTemplate(ctx context.Context, p projectRow) settingsTemplate {
	base := settingsURL(p.Prefix) + "/template"
	v := settingsTemplate{AcceptURL: base + "/accept", ExportURL: base + "/export"}
	ov, err := s.overviewOf(ctx, p.Prefix, p.Workspace)
	if err != nil || ov.Template == nil {
		return v
	}
	v.Ref, v.Since, v.Update = ov.Template.Ref, shortSHA(ov.Template.SHA256), ov.TemplateUpdate
	if !v.Update {
		return v
	}
	if !validTemplateRef(v.Ref) {
		v.Error = "the recorded template ref is malformed"
		return v
	}
	doc, err := readTemplate(ctx, s.c.CLI, v.Ref)
	if err != nil {
		v.Error = err.Error()
		return v
	}
	v.SHA256 = doc.SHA256
	return v
}

// templateExport runs `template export --name <n> --version <N>`, a
// project-level job.
func (s *Server) templateExport(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	name, version := r.PostFormValue("name"), r.PostFormValue("version")
	if !packNameRe.MatchString(name) {
		http.Error(w, "a template name is lower-case letters, digits and -, starting with a letter", http.StatusBadRequest)
		return
	}
	if !templateVersionRe.MatchString(version) {
		http.Error(w, "a template version is a whole number from 1", http.StatusBadRequest)
		return
	}
	s.start(w, r, jobs.Spec{
		Project: p.Prefix, Label: "template export " + name + "@" + version, Dir: p.Workspace,
		Argv: []string{s.c.Exe, "-C", p.Workspace, "template", "export", "--name", name, "--version", version},
	}, nil, settingsURL(p.Prefix))
}

// templateAccept runs `template check --accept <sha>` for the change
// waiting now: the posted sha must be the template's as `template show`
// reads it again.
func (s *Server) templateAccept(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	sha := r.PostFormValue("sha256")
	if !templateSHARe.MatchString(sha) {
		http.Error(w, "no template hash: reload the page", http.StatusBadRequest)
		return
	}
	ov, err := s.overviewOf(r.Context(), p.Prefix, p.Workspace)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ov.Template == nil || !ov.TemplateUpdate || !validTemplateRef(ov.Template.Ref) {
		http.Error(w, "no template change waits", http.StatusBadRequest)
		return
	}
	doc, err := readTemplate(r.Context(), s.c.CLI, ov.Template.Ref)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sha != doc.SHA256 {
		http.Error(w, "the template "+ov.Template.Ref+" changed again since you opened this page: reload it", http.StatusBadRequest)
		return
	}
	s.start(w, r, jobs.Spec{
		Project: p.Prefix, Label: "template check --accept", Dir: p.Workspace,
		Argv: []string{s.c.Exe, "-C", p.Workspace, "template", "check", "--accept", sha},
	}, nil, settingsURL(p.Prefix))
}

// New project.

// newTemplateView is the Stack card for a picked template: its
// questions, each a field with its default.
type newTemplateView struct {
	Row    templateRow
	Doc    templateDoc
	Fields []askField
}

// newTemplate answers the Stack card for the posted template: its
// questions, or without one the card before a read.
func (s *Server) newTemplate(w http.ResponseWriter, r *http.Request) {
	ref := r.PostFormValue("template")
	if ref == "" {
		s.html(w, http.StatusOK, "newstack", newStackView{})
		return
	}
	if !validTemplateRef(ref) {
		http.Error(w, "not a template ref", http.StatusBadRequest)
		return
	}
	ls, err := readTemplates(r.Context(), s.c.CLI)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	v := newTemplateView{}
	found := false
	for _, t := range templateItems(ls) {
		if t.Ref == ref {
			v.Row, found = t, true
		}
	}
	if !found {
		http.Error(w, "unknown template "+ref, http.StatusBadRequest)
		return
	}
	if v.Doc, err = readTemplate(r.Context(), s.c.CLI, ref); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	v.Fields = askFields(v.Doc)
	s.html(w, http.StatusOK, "newtemplate", v)
}
