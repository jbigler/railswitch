package lifecycle

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTraefik serves Traefik's router API. A router named in routers is
// found once after polls for it have been answered 404.
type fakeTraefik struct {
	mu      sync.Mutex
	routers []string
	after   int // 404s before a listed router is found
	polls   map[string]int
}

func newFakeTraefik(t *testing.T, routers ...string) (*fakeTraefik, string) {
	t.Helper()
	ft := &fakeTraefik{routers: routers, polls: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ft.mu.Lock()
		defer ft.mu.Unlock()
		if r.URL.Path == "/api/http/routers" {
			var all []map[string]string
			for _, n := range ft.routers {
				all = append(all, map[string]string{"name": n})
			}
			_ = json.NewEncoder(w).Encode(all)
			return
		}
		name, ok := strings.CutPrefix(r.URL.Path, "/api/http/routers/")
		if !ok {
			http.NotFound(w, r)
			return
		}
		ft.polls[name]++
		for _, n := range ft.routers {
			if n == name && ft.polls[name] > ft.after {
				_ = json.NewEncoder(w).Encode(map[string]string{"name": n})
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return ft, srv.URL
}

const (
	featRouter = "filial-feat-app@docker"
	featReady  = "http://feat.filial.localhost/ ready\n"
)

func (e *tenv) errOut() string { return e.l.Err.(interface{ String() string }).String() }

func TestUpRouteReady(t *testing.T) {
	e := setup(t)
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(e.out.String(), featReady) {
		t.Errorf("out does not end with the ready line:\n%s", e.out)
	}
	if strings.Contains(e.errOut(), "warning") {
		t.Errorf("unexpected warning:\n%s", e.errOut())
	}
}

func TestUpRouteAppearsLater(t *testing.T) {
	e := setup(t)
	ft, url := newFakeTraefik(t, featRouter)
	ft.after = 3
	e.l.TraefikAPI, e.l.RoutePoll, e.l.RouteTimeout = url, time.Millisecond, 10*time.Second
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatal(err)
	}
	if n := ft.polls[featRouter]; n != 4 {
		t.Errorf("polls = %d, want 4", n)
	}
	if !strings.HasSuffix(e.out.String(), featReady) || strings.Contains(e.errOut(), "warning") {
		t.Errorf("out:\n%s\nerr:\n%s", e.out, e.errOut())
	}
}

func TestUpRouteNeverAppears(t *testing.T) {
	e := setup(t)
	ft, url := newFakeTraefik(t, "filial-master-app@docker", "other-feat-app@docker", "dashboard@internal")
	e.l.TraefikAPI, e.l.RoutePoll, e.l.RouteTimeout = url, time.Millisecond, 50*time.Millisecond
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatalf("up fails on a missing route: %v", err)
	}
	if ft.polls[featRouter] < 2 {
		t.Errorf("polled %d times", ft.polls[featRouter])
	}
	if strings.Contains(e.out.String(), "ready") {
		t.Errorf("ready printed:\n%s", e.out)
	}
	msg := e.errOut()
	for _, want := range []string{"feat.filial.localhost", "no route", "filial-master-app@docker", "railswitch status"} {
		if !strings.Contains(msg, want) {
			t.Errorf("warning lacks %q:\n%s", want, msg)
		}
	}
	for _, not := range []string{"other-feat-app", "dashboard@internal"} {
		if strings.Contains(msg, not) {
			t.Errorf("warning lists %q, not a filial- router:\n%s", not, msg)
		}
	}
}

func TestUpTraefikAPIDown(t *testing.T) {
	e := setup(t)
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close() // connection refused
	e.l.TraefikAPI, e.l.RoutePoll, e.l.RouteTimeout = srv.URL, time.Millisecond, 50*time.Millisecond
	if err := e.l.Up(ctx, "feat"); err != nil {
		t.Fatalf("up fails with Traefik down: %v", err)
	}
	msg := e.errOut()
	for _, want := range []string{"feat.filial.localhost", "not running", "systemctl --user status railswitch-traefik.service"} {
		if !strings.Contains(msg, want) {
			t.Errorf("warning lacks %q:\n%s", want, msg)
		}
	}
	if strings.Contains(e.out.String(), "ready") {
		t.Errorf("ready printed:\n%s", e.out)
	}
}
