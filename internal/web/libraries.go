package web

import (
	"net/http"
	"regexp"
	"slices"

	"github.com/jbigler/railswitch/internal/jobs"
)

// The Library page's Libraries section (Stage 7c, D103–D104): the
// machine's package libraries from `library ls`. Add library runs
// `library add <url> --alias <a> [--branch <b>]`, Fetch `library fetch
// <alias>`, and a fetch waiting on its signer is trusted with `library
// trust <alias> --fingerprint <fp> [--rotation | --new-signer]`. All are
// machine-level jobs (machineKey), which the Library page shows beside
// the project's.

var libAliasRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,19}$`)

const maxLibraryURL = 2048

type libRepoView struct {
	Alias, URL, Branch, ID string
	Status, StatusClass    string
	Serial, Packages       int
	Commit, Fetched        string
	Pending                *libPendingView
}

// libPendingView is a fetch waiting for "Trust this key" (Rotation
// false) or "Confirm rotation". Known: the library trusts other keys and
// none vouched for this one, so trusting it takes a second confirmation.
type libPendingView struct {
	Rotation             bool
	Known                bool
	Fingerprint, Trusted string // the key to trust; for a rotation, the trusted key that vouches for it
}

var libStatusClass = map[string]string{"trusted": "green", "unknown-signer": "yellow", "rotation": "yellow"}

func newLibRepos(doc libraryDoc) []libRepoView {
	var out []libRepoView
	for _, l := range doc.Libraries {
		v := libRepoView{
			Alias: l.Alias, URL: l.URL, Branch: l.Branch, ID: l.ID, Status: l.Status, StatusClass: libStatusClass[l.Status],
			Serial: l.Serial, Packages: l.Packages, Commit: shortSHA(l.Commit), Fetched: l.Fetched,
		}
		if v.StatusClass == "" && l.Status != "" {
			v.StatusClass = "red"
		}
		if p := l.Pending; p != nil {
			v.Pending = &libPendingView{Rotation: p.Status == "rotation", Known: p.KnownLibrary && p.Status != "rotation", Fingerprint: p.trustFingerprint()}
			if v.Pending.Rotation {
				v.Pending.Trusted = p.Fingerprint
			}
		}
		out = append(out, v)
	}
	return out
}

// pageJobs is the jobs a page of prefix shows, newest first: the
// project's and, with machine (the Library page), the machine-level ones.
func (s *Server) pageJobs(prefix string, machine bool) []jobs.Snapshot {
	snaps := s.c.Jobs.Jobs(prefix, "")
	if !machine {
		return snaps
	}
	snaps = append(snaps, s.c.Jobs.Jobs(machineKey, "")...)
	slices.SortStableFunc(snaps, func(a, b jobs.Snapshot) int { return b.Started.Compare(a.Started) })
	return snaps
}

// libraryAdd runs `library add <url> --alias <a> [--branch <b>]`.
func (s *Server) libraryAdd(w http.ResponseWriter, r *http.Request) {
	u, alias, branch := r.PostFormValue("url"), r.PostFormValue("alias"), r.PostFormValue("branch")
	if u == "" || len(u) > maxLibraryURL || badValue(u) {
		http.Error(w, "a library is a git URL or path: not empty, no leading -, no line break", http.StatusBadRequest)
		return
	}
	if !validAlias(alias) {
		http.Error(w, "an alias is a lower-case letter, then up to 19 lower-case letters, digits and -; not local", http.StatusBadRequest)
		return
	}
	args := []string{"library", "add", u, "--alias", alias}
	if branch != "" {
		if badName(branch) || badValue(branch) {
			http.Error(w, "a branch has no spaces and doesn't start with -", http.StatusBadRequest)
			return
		}
		args = append(args, "--branch", branch)
	}
	s.machineJob(w, r, "library add "+alias, args)
}

// libraryFetch runs `library fetch <alias>`.
func (s *Server) libraryFetch(w http.ResponseWriter, r *http.Request) {
	alias := r.PostFormValue("alias")
	if !validAlias(alias) {
		http.Error(w, "not a library alias", http.StatusBadRequest)
		return
	}
	s.machineJob(w, r, "library fetch "+alias, []string{"library", "fetch", alias})
}

// libraryTrust runs `library trust <alias> --fingerprint <fp>` for the
// fetch waiting now, as `library ls` reads it again: the posted kind
// (trust or rotation) and fingerprint must be what waits. A rotation adds
// --rotation; a new signer of a library with other keys needs the second
// confirmation and adds --new-signer.
func (s *Server) libraryTrust(w http.ResponseWriter, r *http.Request) {
	alias, fp, kind := r.PostFormValue("alias"), r.PostFormValue("fingerprint"), r.PostFormValue("kind")
	if !validAlias(alias) {
		http.Error(w, "not a library alias", http.StatusBadRequest)
		return
	}
	if !signerFpRe.MatchString(fp) {
		http.Error(w, "not a key fingerprint (SHA256:…)", http.StatusBadRequest)
		return
	}
	if kind != "trust" && kind != "rotation" {
		http.Error(w, "kind is trust or rotation", http.StatusBadRequest)
		return
	}
	doc, err := readJSON[libraryDoc](r.Context(), s.c.CLI, "library", "ls")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	i := slices.IndexFunc(doc.Libraries, func(l libraryRow) bool { return l.Alias == alias })
	if i < 0 {
		http.Error(w, "unknown library "+alias, http.StatusBadRequest)
		return
	}
	p := doc.Libraries[i].Pending
	if p == nil || (p.Status == "rotation") != (kind == "rotation") || p.trustFingerprint() != fp {
		http.Error(w, "what waits for "+alias+" changed since you opened this page: reload it and check the fingerprint again", http.StatusBadRequest)
		return
	}
	args := []string{"library", "trust", alias, "--fingerprint", fp}
	switch {
	case kind == "rotation":
		args = append(args, "--rotation")
	case p.KnownLibrary:
		if r.PostFormValue("new_signer") != "1" {
			http.Error(w, "no trusted key vouched for this one: tick the second confirmation first", http.StatusBadRequest)
			return
		}
		args = append(args, "--new-signer")
	}
	s.machineJob(w, r, "library trust "+alias, args)
}

func validAlias(a string) bool { return libAliasRe.MatchString(a) && a != "local" }

func (s *Server) machineJob(w http.ResponseWriter, r *http.Request, label string, args []string) {
	s.start(w, r, jobs.Spec{Project: machineKey, Label: label, Dir: s.c.Home, Argv: append([]string{s.c.Exe}, args...)}, nil, "/machine")
}
