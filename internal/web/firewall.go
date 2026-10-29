package web

import (
	"net/http"
	"slices"
	"strings"
)

// The Settings page's "Would be blocked" (D119): `fw show --json` for the
// project, loaded after the page (it inspects every container and resolves
// each address). A rejected address that resolves to a host can be staged
// into the allow list of the container's profile.

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
	Error string
	Rows  []fwBlockedRow
	None  []string // containers with no hook (fw show's "NO FIREWALL")
}

type fwBlockedRow struct {
	Container, Mode, Addr, Port, Host string
	Profile                           string // the list a host is allowed in; "": none can (a data container)
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
	var v fwBlockedView
	doc, err := readJSON[fwDoc](r.Context(), s.c.CLI, "-C", p.Workspace, "fw", "show")
	if err != nil {
		v.Error = err.Error()
	}
	for _, c := range doc.Containers {
		if c.Firewall == "none" {
			v.None = append(v.None, c.Name)
		}
		for _, x := range c.Rejected {
			v.Rows = append(v.Rows, fwBlockedRow{Container: c.Name, Mode: c.Mode, Addr: x.Addr, Port: x.Proto + "/" + x.Port, Host: x.Host, Profile: allowProfile(c.Profiles)})
		}
	}
	s.html(w, http.StatusOK, "fw-blocked", v)
}
