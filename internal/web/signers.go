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
// <library> <fingerprint>`; Add key runs `signer add <library> -` with
// the pasted key on stdin, never argv. Both are machine-level jobs, keyed
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

	Error     string
	Libraries []signerLibView
	AddURL    string
	RmURL     string
}

type signerLibView struct {
	ID      string
	Serial  int
	Keys    []signerKeyView
	Revoked []string
}

type signerKeyView struct {
	Fingerprint, Added, Kind, Comment string
}

// keyParts is a public key line's type and comment; the key material
// itself is not shown.
func keyParts(line string) (kind, comment string) {
	f := strings.Fields(line)
	if len(f) > 0 {
		kind = f[0]
	}
	if len(f) > 2 {
		comment = strings.Join(f[2:], " ")
	}
	return kind, comment
}

func (s *Server) machinePage(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	pv := s.pageData("", "", sortKey{By: "name"})
	jv := newJobs(s.c.Jobs.Jobs(machineKey, ""), nil, "")
	s.mu.Unlock()
	v := machineView{
		Title: "Machine · RailSwitch", Prefix: machineKey,
		Header: pv.Header, Projects: pv.Projects, Strip: pv.Strip, Banner: pv.Banner, Jobs: jv,
		AddURL: "/machine/signers/add", RmURL: "/machine/signers/rm",
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
			kind, comment := keyParts(k.Key)
			lv.Keys = append(lv.Keys, signerKeyView{Fingerprint: k.Fingerprint, Added: k.Added, Kind: kind, Comment: comment})
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

// signerRm runs `signer rm <library> <fingerprint>`.
func (s *Server) signerRm(w http.ResponseWriter, r *http.Request) {
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
		Project: machineKey, Label: "signer rm " + lib, Dir: s.c.Home,
		Argv: []string{s.c.Exe, "signer", "rm", lib, fp},
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
