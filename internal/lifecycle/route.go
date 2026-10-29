package lifecycle

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/render"
)

// traefikAPI is the shared proxy's API: its railswitch-api router serves
// it on the web entrypoint (127.0.0.1:80) for Host traefik.localhost
// (machine.ProxyUnits). The Host header is sent explicitly, so no name
// resolution is needed.
const (
	traefikAPI     = "http://127.0.0.1"
	traefikAPIHost = "traefik.localhost"
)

// waitRoute waits until Traefik routes the app's first route, then prints
// its URL as ready. Traefik's docker provider adds a container's routers
// only after the container is healthy and the podman event reaches it, so
// for a moment after `systemctl start` returns the host still gets a 404.
// A route that doesn't appear is a warning, not an error: the app runs.
// It reports whether the route came up (true for an app without one).
func (l *L) waitRoute(ctx context.Context, m *render.Model, in *render.Instance, slug string) (bool, error) {
	app := m.Services[0]
	if len(app.Routes) == 0 {
		return true, nil
	}
	r := app.Routes[0]
	host, err := pack.Expand(r.Host, in.Scope(app.ID))
	if err != nil {
		return false, fmt.Errorf("%s: route %s: %w", app.ID, r.Name, err)
	}
	url := "http://" + host + "/"
	router := m.Prefix + "-" + slug + "-" + r.Name + "@docker" // render's router label, docker provider
	base := cmp.Or(l.TraefikAPI, traefikAPI)
	poll := cmp.Or(l.RoutePoll, 500*time.Millisecond)
	deadline := time.Now().Add(cmp.Or(l.RouteTimeout, 30*time.Second))
	client := &http.Client{Timeout: 2 * time.Second}

	answered := false
	for {
		resp, err := traefikGet(ctx, client, base+"/api/http/routers/"+router)
		if err == nil {
			resp.Body.Close()
			answered = true
			if resp.StatusCode == http.StatusOK {
				fmt.Fprintf(l.Out, "%s ready\n", url)
				return true, nil
			}
		}
		if !time.Now().Before(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(poll):
		}
	}

	if !answered {
		fmt.Fprintf(l.Err, "warning: Traefik has no route for %s: its API at %s did not answer, so Traefik is not running\n"+
			"next: systemctl --user status %s; railswitch status\n", host, base, traefikService)
		return false, nil
	}
	fmt.Fprintf(l.Err, "warning: Traefik has no route for %s yet (router %s); its %s- routers: %s\nnext: railswitch status\n",
		host, router, m.Prefix, l.routers(ctx, client, base, m.Prefix+"-"))
	return false, nil
}

// routers lists Traefik's HTTP routers whose names start with prefix.
func (l *L) routers(ctx context.Context, client *http.Client, base, prefix string) string {
	resp, err := traefikGet(ctx, client, base+"/api/http/routers")
	if err != nil {
		return "unknown (" + err.Error() + ")"
	}
	defer resp.Body.Close()
	var all []struct{ Name string }
	if resp.StatusCode != http.StatusOK {
		return "unknown (" + resp.Status + ")"
	}
	if err := json.NewDecoder(resp.Body).Decode(&all); err != nil {
		return "unknown (" + err.Error() + ")"
	}
	var names []string
	for _, r := range all {
		if strings.HasPrefix(r.Name, prefix) {
			names = append(names, r.Name)
		}
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

func traefikGet(ctx context.Context, client *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Host = traefikAPIHost
	return client.Do(req)
}
