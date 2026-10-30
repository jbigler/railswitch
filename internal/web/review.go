package web

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/jbigler/railswitch/internal/jobs"
)

// The review page (Stage 7a, D97–D98): a package update waiting under
// stack.lock, as `lock --review` shows it, and Accept, which runs
// `lock --accept <hash>` as a project-level job once the flagged items
// are acknowledged. The page sends no file text: Accept names the
// reviewed hash, and the CLI recomputes the review before writing.

// reviewDoc is `lock --review`.
type reviewDoc struct {
	Hash     string          `json:"hash"`
	Flags    int             `json:"flags"`
	Packages []reviewPackage `json:"packages"`
	Restart  []string        `json:"restart"`
	Ejected  []string        `json:"ejected"` // the project's own copies of a Containerfile this changes
}

type reviewPackage struct {
	Ref          string       `json:"ref"`
	Kind         string       `json:"kind"`
	Status       string       `json:"status"` // changed, added, removed, first
	From         string       `json:"from"`
	To           string       `json:"to"`
	AlsoLockedIn []string     `json:"also_locked_in"`
	Files        []reviewFile `json:"files"`
}

type reviewFile struct {
	Path   string       `json:"path"`
	Status string       `json:"status"`
	Diff   string       `json:"diff"` // "": an empty file added or removed
	Flags  []reviewFlag `json:"flags"`
}

// reviewFlag is one check's finding; Line is the new file's line, 0 for
// a manifest flag.
type reviewFlag struct {
	Check string `json:"check"`
	Line  int    `json:"line"`
	Text  string `json:"text"`
}

type reviewView struct {
	Title, Prefix string
	Header        headerView
	Projects      projectsView
	Strip         stripView
	Banner        bannerView
	Jobs          jobsView

	Error     string
	Hash      string
	AckText   string // "": no flags, nothing to acknowledge
	Restart   string // "main, b"; "" for none
	AcceptURL string
	Packages  []reviewPkgView
	Ejected   []bannerDefault // the project's own copies of a default this changes
}

type reviewPkgView struct {
	Ref, Kind, Status, From, To, Also string
	Files                             []reviewFileView
}

type reviewFileView struct {
	Path, Status string
	Flags        int
	Notes        []reviewFlag // flags no diff line shows: manifest and file-level flags, an empty file's
	Lines        []diffLine
	Empty        bool
}

// diffLine is one line of a unified diff: Class meta, hunk, add, del or
// ctx; Check names the flag that highlights it.
type diffLine struct {
	Text, Class, Check string
}

var hunkRe = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// diffLines splits a unified diff into lines, highlighting those at a
// flag's line in the new file; the flags it shows on no line come back as
// notes, so none goes unseen.
func diffLines(diff string, flags []reviewFlag) ([]diffLine, []reviewFlag) {
	at := map[int]string{}
	for _, f := range flags {
		if c, ok := at[f.Line]; ok {
			at[f.Line] = c + ", " + f.Check
		} else if f.Line > 0 {
			at[f.Line] = f.Check
		}
	}
	shown := map[int]bool{}
	var lines []diffLine
	n, inHunk := 0, false
	for _, l := range strings.Split(strings.TrimSuffix(diff, "\n"), "\n") {
		d := diffLine{Text: l, Class: "meta"}
		switch {
		case strings.HasPrefix(l, "@@"):
			d.Class, inHunk = "hunk", false
			if m := hunkRe.FindStringSubmatch(l); m != nil {
				n, _ = strconv.Atoi(m[1])
				inHunk = true
			}
		case !inHunk, strings.HasPrefix(l, `\`):
		case strings.HasPrefix(l, "-"):
			d.Class = "del"
		default:
			d.Class = "ctx"
			if strings.HasPrefix(l, "+") {
				d.Class = "add"
			}
			if c, ok := at[n]; ok {
				d.Check, shown[n] = c, true
			}
			n++
		}
		lines = append(lines, d)
	}
	var notes []reviewFlag
	for _, f := range flags {
		if !shown[f.Line] {
			notes = append(notes, f)
		}
	}
	return lines, notes
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// ackText is the acknowledgement Accept needs when the review flags n
// items; "" for none.
func ackText(n int) string {
	switch n {
	case 0:
		return ""
	case 1:
		return "I've read the 1 flagged item"
	}
	return fmt.Sprintf("I've read the %d flagged items", n)
}

func reviewURL(prefix string) string { return "/p/" + prefix + "/review" }

func reviewPackages(doc reviewDoc) []reviewPkgView {
	var pkgs []reviewPkgView
	for _, p := range doc.Packages {
		pv := reviewPkgView{
			Ref: p.Ref, Kind: p.Kind, Status: p.Status,
			From: shortSHA(p.From), To: shortSHA(p.To), Also: strings.Join(p.AlsoLockedIn, ", "),
		}
		for _, f := range p.Files {
			fv := reviewFileView{Path: f.Path, Status: f.Status, Flags: len(f.Flags), Empty: f.Diff == ""}
			if fv.Empty {
				fv.Notes = f.Flags
			} else {
				fv.Lines, fv.Notes = diffLines(f.Diff, f.Flags)
			}
			pv.Files = append(pv.Files, fv)
		}
		pkgs = append(pkgs, pv)
	}
	return pkgs
}

// installView is the Install review (Stage 7c, D106): the review of the
// library packages a staged change or a new project installs, shown with
// the review page's component. Apply or Create then names its hash.
type installView struct {
	Hash, AckText string
	Packages      []reviewPkgView
}

func newInstall(doc *reviewDoc) *installView {
	if doc == nil {
		return nil
	}
	return &installView{Hash: doc.Hash, AckText: ackText(doc.Flags), Packages: reviewPackages(*doc)}
}

// checkInstall holds a posted accept_install to the install review read
// again: the same hash and, with flags, the tick.
func checkInstall(doc *reviewDoc, posted, ack string) error {
	switch {
	case doc == nil:
		return errors.New("the change no longer installs a library package: look at it again")
	case !hashRe.MatchString(posted):
		return errors.New("this installs a library package: read its Install review first")
	case posted != doc.Hash:
		return errors.New("the install review changed since you read it: look at it again")
	case doc.Flags > 0 && ack != "1":
		return fmt.Errorf("tick \"%s\" first", ackText(doc.Flags))
	}
	return nil
}

func (s *Server) reviewPage(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	pv, jv := s.chrome(p.Prefix)
	s.mu.Unlock()
	v := reviewView{
		Title: "Review update · " + p.Prefix, Prefix: p.Prefix,
		Header: pv.Header, Projects: pv.Projects, Strip: pv.Strip, Banner: pv.Banner, Jobs: jv,
		AcceptURL: reviewURL(p.Prefix) + "/accept",
	}
	doc, err := readJSON[reviewDoc](r.Context(), s.c.CLI, "-C", p.Workspace, "lock", "--review")
	if err != nil {
		v.Error = err.Error()
		s.html(w, http.StatusOK, "review", v)
		return
	}
	v.Hash, v.AckText, v.Restart = doc.Hash, ackText(doc.Flags), strings.Join(doc.Restart, ", ")
	v.Packages = reviewPackages(doc)
	for _, name := range doc.Ejected {
		v.Ejected = append(v.Ejected, bannerDefault{Name: name, URL: "/p/" + p.Prefix + "/images/edit/" + name})
	}
	s.html(w, http.StatusOK, "review", v)
}

// reviewAccept runs `lock --accept <hash> [--restart]` once the review
// read again still has the posted hash and, with flags, the form carries
// the acknowledgement.
func (s *Server) reviewAccept(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	hash := r.PostFormValue("hash")
	if !hashRe.MatchString(hash) {
		http.Error(w, "no review hash: reload the page", http.StatusBadRequest)
		return
	}
	doc, err := readJSON[reviewDoc](r.Context(), s.c.CLI, "-C", p.Workspace, "lock", "--review")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(doc.Packages) == 0 {
		http.Error(w, "nothing is waiting for review", http.StatusBadRequest)
		return
	}
	if hash != doc.Hash {
		http.Error(w, "the update changed since you opened this page: reload it and review again", http.StatusBadRequest)
		return
	}
	if doc.Flags > 0 && r.PostFormValue("ack") != "1" {
		http.Error(w, "tick \""+ackText(doc.Flags)+"\" first", http.StatusBadRequest)
		return
	}
	argv := []string{s.c.Exe, "-C", p.Workspace, "lock", "--accept", hash}
	label := "lock --accept"
	if r.PostFormValue("restart") != "" {
		argv = append(argv, "--restart")
		label += " --restart"
	}
	s.start(w, r, jobs.Spec{Project: p.Prefix, Label: label, Dir: p.Workspace, Argv: argv}, nil, reviewURL(p.Prefix))
}
