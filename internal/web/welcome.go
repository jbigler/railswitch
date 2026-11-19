package web

import (
	"net/http"
	"slices"
	"strings"
)

// The welcome page (D220): / while no project is registered. It checks the
// machine (the checks fragment, also on the Machine page) and offers two
// starts, a repository or a template folder, both into /new.

type welcomeView struct {
	Title, Prefix string
	Header        headerView
	Projects      projectsView
	Strip         stripView
	Banner        bannerView
	Jobs          jobsView

	Templates      []templateRow
	TemplatesError string
}

func (s *Server) welcome(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	pv := s.pageData("", "", sortKey{By: "name"}, usageDefault)
	jv := newJobs(s.c.Jobs.Jobs(machineKey, ""), nil, "")
	s.mu.Unlock()
	v := welcomeView{
		Title: "Welcome · RailSwitch", Prefix: machineKey,
		Header: pv.Header, Projects: pv.Projects, Strip: pv.Strip, Banner: pv.Banner, Jobs: jv,
	}
	if ls, err := readTemplates(r.Context(), s.c.CLI); err != nil {
		v.TemplatesError = err.Error()
	} else {
		v.Templates = templateItems(ls)
	}
	s.html(w, http.StatusOK, "welcome", v)
}

// doctorCheck is one row of `doctor --json` (docs/json.md).
type doctorCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Warn   bool   `json:"warn"`
	Detail string `json:"detail"`
	Fix    string `json:"fix"`
}

// Cmd is the command a fix starts with, for Copy: up to " (" or ";"
// ("loginctl enable-linger (only needed…)"), when it begins with a program
// doctor's fixes run; "" for a fix in words ("install jq …").
func (c doctorCheck) Cmd() string {
	cmd, _, _ := strings.Cut(c.Fix, " (")
	cmd, _, _ = strings.Cut(cmd, ";")
	cmd = strings.TrimSpace(cmd)
	first, _, _ := strings.Cut(cmd, " ")
	if !slices.Contains([]string{"railswitch", "systemctl", "loginctl", "gh", "podman"}, first) {
		return ""
	}
	return cmd
}

type checksView struct {
	Error              string
	Failed, Warned, OK []doctorCheck
}

// checks is the machine checks fragment: doctor's host checks (-C /, so
// never a workspace's, whatever the dashboard's own folder), failures
// first. The welcome and Machine pages load it after drawing.
func (s *Server) checks(w http.ResponseWriter, r *http.Request) {
	var v checksView
	rows, err := readJSON[[]doctorCheck](r.Context(), s.c.CLI, "-C", "/", "doctor")
	if err != nil {
		v.Error = err.Error()
	}
	for _, c := range rows {
		switch {
		case c.OK:
			v.OK = append(v.OK, c)
		case c.Warn:
			v.Warned = append(v.Warned, c)
		default:
			v.Failed = append(v.Failed, c)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	s.html(w, http.StatusOK, "checks", v)
}
