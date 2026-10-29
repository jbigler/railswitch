package web

import (
	"crypto/subtle"
	"net/http"
	"strconv"
)

// cookieName holds the login token.
const cookieName = "railswitch_dashboard"

// allowedHosts are the only Host headers served (DNS rebinding).
var allowedHosts = map[string]bool{
	"railswitch.localhost:7700": true,
	"localhost:7700":            true,
	"127.0.0.1:7700":            true,
}

const csp = "default-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

// denyText is the refusal page's advice.
const denyText = "Run railswitch dashboard in a terminal and open the link it prints."

// guard serves next only to the logged-in page (spec "Access"): an
// allowed Host always; the cookie for everything but /healthz and
// /login; and for a POST (any unsafe method) the Origin of that Host.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", csp)
		// same-origin, not no-referrer: with no-referrer some browsers send
		// a form POST's Origin as "null", refused below, and the page
		// would not work without its script.
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		if !allowedHosts[r.Host] {
			// Name what came and what works: a tunnel on another port or
			// the machine's own address is the usual cause.
			http.Error(w, "forbidden host "+strconv.Quote(r.Host)+": the dashboard answers only at "+
				"http://railswitch.localhost:7700/, http://localhost:7700/ or http://127.0.0.1:7700/ (from another machine, "+
				"forward the same port: ssh -L 7700:127.0.0.1:7700 <host>)", http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/healthz":
			next.ServeHTTP(w, r)
			return
		case "/login":
			s.login(w, r)
			return
		}
		if c, err := r.Cookie(cookieName); err != nil || !s.tokenOK(c.Value) {
			s.simple(w, http.StatusUnauthorized, "", "You are not logged in.", denyText)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("Origin") != "http://"+r.Host {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// tokenOK compares t with the token in constant time.
func (s *Server) tokenOK(t string) bool {
	return s.c.Token != "" && subtle.ConstantTimeCompare([]byte(t), []byte(s.c.Token)) == 1
}

// login sets the cookie for the right token and redirects to /, so the
// token leaves the address bar.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !s.tokenOK(r.URL.Query().Get("token")) {
		s.simple(w, http.StatusForbidden, "", "That login link is not valid.", denyText)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: s.c.Token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 31536000,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
