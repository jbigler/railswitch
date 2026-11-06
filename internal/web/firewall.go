package web

import (
	"net/http"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/jobs"
)

// The Settings page's "Would be blocked" (D119): `fw show --json` for the
// project, loaded after the page (it inspects every container and resolves
// each address). A rejected address that resolves to a host can be staged
// into the allow list of the container's profile, or, in a container under
// enforce, allowed in it at once for a while (D184).

// fwDoc is `fw show --json`.
type fwDoc struct {
	Containers []struct {
		Name     string   `json:"name"`
		Firewall string   `json:"firewall"`
		Profiles []string `json:"profiles"`
		Mode     string   `json:"mode"`
		Rejected []struct {
			Addr  string `json:"addr"`
			Proto string `json:"proto"`
			Port  string `json:"port"`
			Host  string `json:"host"`
		} `json:"rejected"`
	} `json:"containers"`
}

type fwBlockedView struct {
	Prefix string
	Error  string
	Rows   []fwBlockedRow
	None   []string // containers with no hook (fw show's "NO FIREWALL")
}

type fwBlockedRow struct {
	Container, Mode, Addr, Port, Host string
	Profile                           string // the list a host is allowed in; "": none can (a data container)
	Slug, Service                     string // the worktree service the container runs; "": none (e.g. exec's)
}

// liveFor is a live allow's choices: the form value, and fw allow's --for
// ("": until the container restarts).
var liveFor = map[string]string{"10m": "10m", "1h": "1h", "restart": ""}

// fwAllowLive allows host in the running container of a worktree's
// service now: fw allow, for a while or until it restarts. The allow list
// in project.toml is the lasting way (the other button).
func (s *Server) fwAllowLive(w http.ResponseWriter, r *http.Request) {
	p, wt, ok := s.lookup(w, r)
	if !ok {
		return
	}
	svc := r.PathValue("service")
	if !slices.ContainsFunc(wt.Services, func(x service) bool { return x.Service == svc }) {
		http.Error(w, "unknown service", http.StatusNotFound)
		return
	}
	host := r.PostFormValue("host")
	// fw allow --for takes addresses and host names, never ranges or meta:
	if strings.ContainsAny(host, "/:") || config.CheckAllowHost(host) != nil {
		http.Error(w, "not a host name: "+host, http.StatusUnprocessableEntity)
		return
	}
	d, ok := liveFor[r.PostFormValue("for")]
	if !ok {
		http.Error(w, "for: 10m, 1h or restart", http.StatusUnprocessableEntity)
		return
	}
	argv := []string{s.c.Exe, "-C", p.Workspace, "-w", wt.Slug, "fw", "allow"}
	label := "allow " + host + " in " + svc + " until it restarts"
	if d != "" {
		argv = append(argv, "--for", d)
		label = "allow " + host + " in " + svc + " for " + d
	}
	s.start(w, r, jobs.Spec{
		Project: p.Prefix, Slug: wt.Slug, Label: label, Dir: p.Workspace,
		Argv: append(argv, svc, host),
	}, nil, worktreePath(p, wt.Slug))
}

// allowProfile is the editable allow list of a container with profiles.
func allowProfile(profiles []string) string {
	switch {
	case slices.Contains(profiles, "app"):
		return "app"
	case slices.Contains(profiles, "deps"):
		return "deps"
	case slices.ContainsFunc(profiles, func(p string) bool { return p == "agent" || strings.HasPrefix(p, "agent-") }):
		return "agent"
	}
	return ""
}

func (s *Server) settingsBlocked(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	v := fwBlockedView{Prefix: p.Prefix}
	doc, err := readJSON[fwDoc](r.Context(), s.c.CLI, "-C", p.Workspace, "fw", "show")
	if err != nil {
		v.Error = err.Error()
	}
	// A container's worktree and service, by its exact name: for the live
	// allow. Without the overview the rows still offer the list.
	owner := map[string][2]string{}
	if ov, err := s.overviewOf(r.Context(), p.Prefix, p.Workspace); err == nil {
		for _, wt := range ov.Worktrees {
			for _, x := range wt.Services {
				if x.Container != "" {
					owner[x.Container] = [2]string{wt.Slug, x.Service}
				}
			}
		}
	}
	for _, c := range doc.Containers {
		if c.Firewall == "none" {
			v.None = append(v.None, c.Name)
		}
		for _, x := range c.Rejected {
			row := fwBlockedRow{Container: c.Name, Mode: c.Mode, Addr: x.Addr, Port: x.Proto + "/" + x.Port, Host: x.Host, Profile: allowProfile(c.Profiles)}
			if o, ok := owner[c.Name]; ok && c.Mode == "enforce" {
				row.Slug, row.Service = o[0], o[1]
			}
			v.Rows = append(v.Rows, row)
		}
	}
	s.html(w, http.StatusOK, "fw-blocked", v)
}
