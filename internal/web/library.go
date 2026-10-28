package web

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/jobs"
)

// The Library (spec "Pages"): the packs, options and agents of `pack
// ls`, one package's `pack show`, and its files in the shared editor.
// Built-in packages are read-only; a local package's files are written by
// `pack file write|rm` jobs, the text on stdin.

// libRefRe is a package ref: name@N or local:name@N.
var libRefRe = regexp.MustCompile(`^(?:local:)?[a-z][a-z0-9-]*@[0-9]+$`)

// libPathRe is the shape of a package file path (D93): a file at the top
// or in entrypoints/ or initdb/. The CLI checks the exact list.
var libPathRe = regexp.MustCompile(`^(?:(?:entrypoints|initdb)/)?[A-Za-z0-9][A-Za-z0-9._-]*$`)

// packNameRe is a name `pack new` and `pack eject --as` accept.
var packNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

const packAck = "I've read this file. A package's RUN lines run on this machine during the build, outside the firewall, and its scripts run in the project's containers."

// manifests is each package kind's manifest, which can't be deleted.
var manifests = map[string]string{"pack": "pack.toml", "option": "option.toml", "agent": "agent.toml"}

type libraryView struct {
	Title, Prefix string
	Header        headerView
	Projects      projectsView
	Strip         stripView
	Banner        bannerView
	Jobs          jobsView

	Error  string
	Groups []libGroup
	Sel    *libDetail

	NewURL                 string
	CopyPacks, CopyOptions []string // built-ins a new package can start from
	Slots                  []string
	EditorBase             string // /p/<prefix>/library/
}

type libGroup struct {
	Title string
	Items []libItem
}

type libItem struct {
	Ref, URL, Note string
	Local, Used    bool
	Current        bool
}

type libDetail struct {
	Ref, Kind string
	Doc       packDoc
	Error     string
	Local     bool
	Files     []libFile
	AddBase   string // "": no Add file
}

type libFile struct {
	Path, URL  string
	Size       int64
	Executable bool
}

func libraryURL(prefix string) string { return "/p/" + prefix + "/library" }

func libRefURL(prefix, ref string) string {
	return libraryURL(prefix) + "?ref=" + url.QueryEscape(ref)
}

func libFileURL(prefix, ref, path string) string {
	return libraryURL(prefix) + "/" + ref + "/" + path
}

// usedRefs are the packages the project uses: its options, and every ref
// its lock names. Without project show, none.
func usedRefs(ctx context.Context, c CLI, ws string) map[string]bool {
	used := map[string]bool{}
	show, err := readProjectShow(ctx, c, ws)
	if err != nil {
		return used
	}
	for _, o := range show.Options {
		used[o.Ref] = true
	}
	for _, r := range show.Locked {
		used[r] = true
	}
	delete(used, "none")
	return used
}

func (s *Server) libraryPage(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	s.mu.Lock()
	pv, jv := s.chrome(p.Prefix)
	s.mu.Unlock()
	v := libraryView{
		Title: "Library · " + p.Prefix, Prefix: p.Prefix,
		Header: pv.Header, Projects: pv.Projects, Strip: pv.Strip, Banner: pv.Banner, Jobs: jv,
		NewURL: libraryURL(p.Prefix) + "/new", EditorBase: libraryURL(p.Prefix) + "/",
	}
	cat, err := readJSON[catalogDoc](ctx, s.c.CLI, "-C", p.Workspace, "pack", "ls")
	if err != nil {
		v.Error = err.Error()
		s.html(w, http.StatusOK, "library", v)
		return
	}
	used := usedRefs(ctx, s.c.CLI, p.Workspace)
	sel := r.URL.Query().Get("ref")
	selKind := ""
	item := func(kind, ref, note string) libItem {
		if ref == sel {
			selKind = kind
		}
		return libItem{Ref: ref, URL: libRefURL(p.Prefix, ref), Note: note, Local: strings.HasPrefix(ref, "local:"), Used: used[ref], Current: ref == sel}
	}
	packs, options, agents := libGroup{Title: "Packs"}, libGroup{Title: "Options"}, libGroup{Title: "Agents"}
	for _, x := range cat.Packs {
		packs.Items = append(packs.Items, item("pack", x.Ref, ""))
		if !strings.HasPrefix(x.Ref, "local:") {
			v.CopyPacks = append(v.CopyPacks, x.Ref)
		}
	}
	for _, x := range cat.Options {
		options.Items = append(options.Items, item("option", x.Ref, x.Slot))
		if !strings.HasPrefix(x.Ref, "local:") {
			v.CopyOptions = append(v.CopyOptions, x.Ref)
		}
		if !slices.Contains(v.Slots, x.Slot) {
			v.Slots = append(v.Slots, x.Slot)
		}
	}
	for _, x := range cat.Agents {
		agents.Items = append(agents.Items, item("agent", x.Ref, ""))
	}
	slices.Sort(v.Slots)
	v.Groups = []libGroup{packs, options, agents}
	if sel != "" {
		if selKind == "" {
			http.Error(w, "unknown package", http.StatusNotFound)
			return
		}
		v.Sel = s.libDetail(ctx, p, sel, selKind)
	}
	s.html(w, http.StatusOK, "library", v)
}

// libDetail is one package's `pack show` and its files; a package without
// a file list (a built-in, or one that doesn't load) offers its manifest.
func (s *Server) libDetail(ctx context.Context, p projectRow, ref, kind string) *libDetail {
	d := &libDetail{Ref: ref, Kind: kind, Local: strings.HasPrefix(ref, "local:")}
	doc, err := readJSON[packDoc](ctx, s.c.CLI, "-C", p.Workspace, "pack", "show", ref)
	if err != nil {
		d.Error = err.Error()
	}
	d.Doc = doc
	for _, f := range doc.Files {
		d.Files = append(d.Files, libFile{Path: f.Path, URL: libFileURL(p.Prefix, ref, f.Path), Size: f.Size, Executable: f.Executable})
	}
	if len(d.Files) == 0 {
		m := manifests[kind]
		d.Files = []libFile{{Path: m, URL: libFileURL(p.Prefix, ref, m)}}
	}
	if d.Local {
		d.AddBase = libraryURL(p.Prefix) + "/" + ref + "/"
	}
	return d
}

// libTarget is the request's {ref} and {path}, answering 404 itself when
// either is malformed.
func libTarget(w http.ResponseWriter, r *http.Request) (ref, path string, ok bool) {
	ref, path = r.PathValue("ref"), r.PathValue("path")
	if !libRefRe.MatchString(ref) || !libPathRe.MatchString(path) {
		http.Error(w, "not a package file", http.StatusNotFound)
		return "", "", false
	}
	return ref, path, true
}

// localTarget is libTarget for a write: a built-in package is read-only.
func localTarget(w http.ResponseWriter, r *http.Request) (ref, path string, ok bool) {
	ref, path, ok = libTarget(w, r)
	if ok && !strings.HasPrefix(ref, "local:") {
		http.Error(w, "a built-in package is read-only: copy it with New pack or New option", http.StatusUnprocessableEntity)
		return "", "", false
	}
	return ref, path, ok
}

func isManifest(path string) bool {
	for _, m := range manifests {
		if path == m {
			return true
		}
	}
	return false
}

func (s *Server) libraryEditor(w http.ResponseWriter, r *http.Request) {
	ref, path, ok := libTarget(w, r)
	if !ok {
		return
	}
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	local := strings.HasPrefix(ref, "local:")
	add := local && r.URL.Query().Get("new") == "1"
	s.mu.Lock()
	pv, jv := s.chrome(p.Prefix)
	s.mu.Unlock()
	act := func(a string) string { return libraryURL(p.Prefix) + "/" + a + "/" + ref + "/" + path }
	v := editorView{
		Title: path + " · " + ref + " · " + p.Prefix, Prefix: p.Prefix,
		Header: pv.Header, Projects: pv.Projects, Strip: pv.Strip, Banner: pv.Banner, Jobs: jv,
		URL: libFileURL(p.Prefix, ref, path), Back: libRefURL(p.Prefix, ref), BackText: "Library · " + ref,
		File: path, Kind: ref, ReadOnly: !local, New: add,
	}
	if !add {
		doc, err := readJSON[packFileDoc](ctx, s.c.CLI, "-C", p.Workspace, "pack", "file", "show", ref, path)
		if err != nil {
			v.Error = err.Error()
			s.html(w, http.StatusOK, "editor", v)
			return
		}
		v.Text, v.Hash = doc.Text, doc.Hash
	}
	if local {
		v.SaveURL = act("save")
		if path != "README.md" {
			v.AckText = packAck
		}
		if usedRefs(ctx, s.c.CLI, p.Workspace)[ref] {
			v.CheckURL, v.CheckRequired = act("check"), true
		}
		if !add && !isManifest(path) {
			v.DeleteURL = act("rm")
		}
	}
	s.html(w, http.StatusOK, "editor", v)
}

// libraryCheck answers the Check panel, judged by `pack file write
// --dry-run`.
func (s *Server) libraryCheck(w http.ResponseWriter, r *http.Request) {
	ref, path, ok := localTarget(w, r)
	if !ok {
		return
	}
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	hash, text, err := editorForm(w, r, true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	doc, err := readCheck(r.Context(), s.c.CLI, text, "-C", p.Workspace, "pack", "file", "write", ref, path, "--if-hash", hash, "--dry-run")
	s.html(w, http.StatusOK, "check", newCheck(doc, err, libFileURL(p.Prefix, ref, path), true))
}

// librarySave runs `pack file write <ref> <path> --if-hash <h>
// [--restart]` with the text on stdin; an empty hash adds the file.
func (s *Server) librarySave(w http.ResponseWriter, r *http.Request) {
	ref, path, ok := localTarget(w, r)
	if !ok {
		return
	}
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	hash, text, err := editorForm(w, r, true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if path != "README.md" && !acked(w, r) {
		return
	}
	argv := []string{s.c.Exe, "-C", p.Workspace, "pack", "file", "write", ref, path, "--if-hash", hash}
	label := "pack file write " + ref + " " + path
	if r.PostFormValue("restart") != "" {
		argv = append(argv, "--restart")
		label += " --restart"
	}
	s.start(w, r, jobs.Spec{Project: p.Prefix, Label: label, Dir: p.Workspace, Argv: argv}, text, libFileURL(p.Prefix, ref, path))
}

// libraryRm runs `pack file rm <ref> <path> --if-hash <h>`.
func (s *Server) libraryRm(w http.ResponseWriter, r *http.Request) {
	ref, path, ok := localTarget(w, r)
	if !ok {
		return
	}
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	hash := r.PostFormValue("hash")
	if !hashRe.MatchString(hash) {
		http.Error(w, "no file hash: reload the page", http.StatusUnprocessableEntity)
		return
	}
	s.start(w, r, jobs.Spec{
		Project: p.Prefix, Label: "pack file rm " + ref + " " + path, Dir: p.Workspace,
		Argv: []string{s.c.Exe, "-C", p.Workspace, "pack", "file", "rm", ref, path, "--if-hash", hash},
	}, nil, libRefURL(p.Prefix, ref))
}

// libraryNew starts a local package: a copy of a built-in (`pack eject
// <ref> --as <name>`) or a skeleton (`pack new <name>`, `pack new
// --option <slot> <name>`).
func (s *Server) libraryNew(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	kind, name, from, slot := r.PostFormValue("kind"), r.PostFormValue("name"), r.PostFormValue("from"), r.PostFormValue("slot")
	if kind != "pack" && kind != "option" {
		http.Error(w, "kind is pack or option", http.StatusUnprocessableEntity)
		return
	}
	if !packNameRe.MatchString(name) {
		http.Error(w, "a name is lower-case letters, digits and -, starting with a letter", http.StatusUnprocessableEntity)
		return
	}
	cat, err := readJSON[catalogDoc](r.Context(), s.c.CLI, "-C", p.Workspace, "pack", "ls")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var builtins, slots []string
	if kind == "pack" {
		for _, x := range cat.Packs {
			builtins = append(builtins, x.Ref)
		}
	} else {
		for _, x := range cat.Options {
			builtins, slots = append(builtins, x.Ref), append(slots, x.Slot)
		}
	}
	var args []string
	switch {
	case from != "":
		if strings.HasPrefix(from, "local:") || !slices.Contains(builtins, from) {
			http.Error(w, "start from a built-in "+kind, http.StatusUnprocessableEntity)
			return
		}
		args = []string{"pack", "eject", from, "--as", name}
	case kind == "pack":
		args = []string{"pack", "new", name}
	default:
		if !slices.Contains(slots, slot) {
			http.Error(w, "pick the option's slot", http.StatusUnprocessableEntity)
			return
		}
		args = []string{"pack", "new", "--option", slot, name}
	}
	s.start(w, r, jobs.Spec{
		Project: p.Prefix, Label: strings.Join(args, " "), Dir: p.Workspace,
		Argv: append([]string{s.c.Exe, "-C", p.Workspace}, args...),
	}, nil, libraryURL(p.Prefix))
}
