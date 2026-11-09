package web

import (
	"net/http"
	"net/url"
	"strings"
)

// issueNew is GitHub's new-issue form for railswitch (D190).
const issueNew = "https://github.com/jbigler/railswitch/issues/new"

// issue is GET /issue, the Report issue link: it sends the browser to
// GitHub's new-issue form with a body naming the version and the page the
// link was clicked on. The page is its route pattern, from the same-origin
// Referer, so no project prefix or worktree slug leaves the machine.
func (s *Server) issue(w http.ResponseWriter, r *http.Request) {
	page := "unknown"
	if u, err := url.Parse(r.Referer()); err == nil && u.Path != "" {
		_, p := s.mux.Handler(&http.Request{Method: http.MethodGet, Host: r.Host, URL: &url.URL{Path: u.Path}})
		// Only a registered GET pattern, never a bare path, which could name the project.
		if rest, ok := strings.CutPrefix(p, "GET "); ok {
			page = rest
		}
	}
	body := "**What happened**\n\n\n**What did you expect**\n\n\n---\nrailswitch " + s.c.Version + " · page `" + page + "`\n"
	http.Redirect(w, r, issueNew+"?body="+url.QueryEscape(body), http.StatusSeeOther)
}
