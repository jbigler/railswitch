package web

import (
	"net/http"
	"regexp"
	"strings"
	"unicode"

	"github.com/jbigler/railswitch/internal/jobs"
)

// The machine page's Signers section (Stage 7b): the SSH keys trusted to
// sign each package library, from `signer ls`. Distrust runs `signer rm
// <library> <fingerprint>`, Revoke `signer revoke <library> <fingerprint>`;
// Add key runs `signer add <library> -` with the pasted key on stdin,
// never argv. Both are machine-level jobs, keyed
// by machineKey, which is no valid project prefix.

// machineKey is the job key of machine-level jobs.
const machineKey = "~machine"

const maxSignerKey = 16 << 10

var (
	signerLibRe = regexp.MustCompile(`^[a-z0-9][a-z0-9./_-]{0,199}$`)
	signerFpRe  = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}$`)
)

// signerDoc is `signer ls`.
type signerDoc struct {
	Libraries []signerLibrary `json:"libraries"`
}

type signerLibrary struct {
	ID         string         `json:"id"`
	LastSerial int            `json:"last_serial"`
	Keys       []signerKey    `json:"keys"`
	Revoked    []signerRevoke `json:"revoked"`
}

type signerKey struct {
	Key         string `json:"key"`
	Fingerprint string `json:"fingerprint"`
	Added       string `json:"added"`
}

type signerRevoke struct {
	Fingerprint string `json:"fingerprint"`
}

type machineView struct {
	Title, Prefix string
	Header        headerView
	Projects      projectsView
	Strip         stripView
	Banner        bannerView
	Jobs          jobsView

	Error          string
	Templates      []templateRow
	TemplatesError string
	Libraries      []signerLibView
	AddURL         string
	RmURL          string
	RevokeURL      string
}

type signerLibView struct {
	ID      string
	Serial  int
	Keys    []signerKeyView
	Revoked []string
}

// signerKeyView is one trusted key: its type, never the key material.
// signers.toml keeps no comment.
type signerKeyView struct {
	Fingerprint, Added, Kind string
}

func (s *Server) machinePage(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	pv := s.pageData("", "", sortKey{By: "name"})
	jv := newJobs(s.c.Jobs.Jobs(machineKey, ""), nil, "")
	s.mu.Unlock()
	v := machineView{
		Title: "Machine · RailSwitch", Prefix: machineKey,
		Header: pv.Header, Projects: pv.Projects, Strip: pv.Strip, Banner: pv.Banner, Jobs: jv,
		AddURL: "/machine/signers/add", RmURL: "/machine/signers/rm", RevokeURL: "/machine/signers/revoke",
	}
	if ls, err := readTemplates(r.Context(), s.c.CLI); err != nil {
		v.TemplatesError = err.Error()
	} else {
		v.Templates = templateItems(ls)
	}
	doc, err := readJSON[signerDoc](r.Context(), s.c.CLI, "signer", "ls")
	if err != nil {
		v.Error = err.Error()
		s.html(w, http.StatusOK, "machine", v)
		return
	}
	for _, l := range doc.Libraries {
		lv := signerLibView{ID: l.ID, Serial: l.LastSerial}
		for _, k := range l.Keys {
			kind, _, _ := strings.Cut(k.Key, " ")
			lv.Keys = append(lv.Keys, signerKeyView{Fingerprint: k.Fingerprint, Added: k.Added, Kind: kind})
		}
		for _, x := range l.Revoked {
			lv.Revoked = append(lv.Revoked, x.Fingerprint)
		}
		v.Libraries = append(v.Libraries, lv)
	}
	s.html(w, http.StatusOK, "machine", v)
}

func validLibraryID(id string) bool {
	return signerLibRe.MatchString(id) && !strings.Contains(id, "..")
}

// signerRm runs `signer rm <library> <fingerprint>`: distrust.
func (s *Server) signerRm(w http.ResponseWriter, r *http.Request) { s.signerFpJob(w, r, "rm") }

// signerRevoke runs `signer revoke <library> <fingerprint>`.
func (s *Server) signerRevoke(w http.ResponseWriter, r *http.Request) {
	s.signerFpJob(w, r, "revoke")
}

// signerFpJob runs `signer <verb> <library> <fingerprint>` from the form.
func (s *Server) signerFpJob(w http.ResponseWriter, r *http.Request, verb string) {
	lib, fp := r.PostFormValue("library"), r.PostFormValue("fingerprint")
	if !validLibraryID(lib) {
		http.Error(w, "not a library id", http.StatusBadRequest)
		return
	}
	if !signerFpRe.MatchString(fp) {
		http.Error(w, "not a key fingerprint (SHA256:…)", http.StatusBadRequest)
		return
	}
	s.start(w, r, jobs.Spec{
		Project: machineKey, Label: "signer " + verb + " " + lib, Dir: s.c.Home,
		Argv: []string{s.c.Exe, "signer", verb, lib, fp},
	}, nil, "/machine")
}

// signerKeyText is the pasted key: one public key line.
func signerKeyText(raw string) (string, bool) {
	if len(raw) > maxSignerKey {
		return "", false
	}
	var lines []string
	for _, l := range strings.Split(raw, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) != 1 || strings.ContainsFunc(lines[0], func(r rune) bool { return unicode.IsControl(r) && r != '\t' }) {
		return "", false
	}
	return lines[0], true
}

// signerAdd runs `signer add <library> -` with the key on stdin.
func (s *Server) signerAdd(w http.ResponseWriter, r *http.Request) {
	lib := r.PostFormValue("library")
	if !validLibraryID(lib) {
		http.Error(w, "not a library id", http.StatusBadRequest)
		return
	}
	key, ok := signerKeyText(r.PostFormValue("key"))
	if !ok {
		http.Error(w, "paste one public key line (at most 16 KiB)", http.StatusBadRequest)
		return
	}
	s.start(w, r, jobs.Spec{
		Project: machineKey, Label: "signer add " + lib, Dir: s.c.Home,
		Argv: []string{s.c.Exe, "signer", "add", lib, "-"},
	}, []byte(key), "/machine")
}
