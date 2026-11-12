package web

import (
	"fmt"
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
		Refused []struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		} `json:"refused"`
		LAN []struct {
			Addr string `json:"addr"`
			Port string `json:"port"`
			Host string `json:"host"`
		} `json:"lan"`
	} `json:"containers"`
}

type fwBlockedView struct {
	Prefix string
	Error  string
	Title  string           // "Blocked", or what it would be under enforce
	Count  int              // the rows a host can be allowed for
	Groups []fwBlockedGroup // one per container with rows
	None   []string         // containers with no hook (fw show's "NO FIREWALL")
	LAN    []string         // "<container>: <addr> tcp/<port> (<host>)", config.toml [[lan]] (D196)
}

// fwBlockedGroup is a container's rows, the most refused first.
type fwBlockedGroup struct {
	Container, Mode string
	Profile         string // the list its hosts are allowed in; "": none can (a data container)
	Rows            []fwBlockedRow
}

type fwBlockedRow struct {
	Addr, Host, Profile string
	Reason              string // "DNS", or the address and port rejected
	Count               int    // a refused name's lookups; 0 for an address
	Slug, Service       string // the worktree service the container runs; "": none (e.g. exec's)
	// Refused: a name the DNS filter refused (D195). fw allow --for opens
	// addresses only, so only "until it restarts" makes the name resolve.
	Refused bool
	// Own: one of the project's own *.<prefix>.localhost names, which no
	// allow list is for.
	Own bool
}

// CanList reports whether the row's host can go into an allow list.
func (r fwBlockedRow) CanList() bool { return r.Host != "" && r.Profile != "" && !r.Own }

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
	own := "." + p.Prefix + ".localhost"
	enforce, other := false, false
	for _, c := range doc.Containers {
		if c.Firewall == "none" {
			v.None = append(v.None, c.Name)
		}
		for _, l := range c.LAN {
			v.LAN = append(v.LAN, fmt.Sprintf("%s: %s tcp/%s (%s)", c.Name, l.Addr, l.Port, l.Host))
		}
		g := fwBlockedGroup{Container: c.Name, Mode: c.Mode, Profile: allowProfile(c.Profiles)}
		add := func(row fwBlockedRow) {
			row.Profile = g.Profile
			row.Own = strings.HasSuffix("."+row.Host, own)
			if o, ok := owner[c.Name]; ok && c.Mode == "enforce" {
				row.Slug, row.Service = o[0], o[1]
			}
			if row.CanList() {
				v.Count++
			}
			g.Rows = append(g.Rows, row)
		}
		// A name its DNS filter refused (D195): no address, but a host to
		// allow like any other.
		for _, x := range c.Refused {
			add(fwBlockedRow{Host: x.Name, Reason: "DNS", Count: x.Count, Refused: true})
		}
		for _, x := range c.Rejected {
			add(fwBlockedRow{Addr: x.Addr, Host: x.Host, Reason: strings.TrimSpace(x.Addr + " " + x.Proto + "/" + x.Port)})
		}
		if len(g.Rows) == 0 {
			continue
		}
		slices.SortStableFunc(g.Rows, func(a, b fwBlockedRow) int { return b.Count - a.Count })
		enforce, other = enforce || c.Mode == "enforce", other || c.Mode != "enforce"
		v.Groups = append(v.Groups, g)
	}
	switch {
	case enforce && other:
		v.Title = "Blocked, or would be under enforce"
	case other:
		v.Title = "Would be blocked under enforce"
	default:
		v.Title = "Blocked"
	}
	s.html(w, http.StatusOK, "fw-blocked", v)
}
