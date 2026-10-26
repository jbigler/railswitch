// Package tailscale runs the tailscale CLI for a tailnet share (D46): it
// serves a loopback port on the tailnet HTTPS port of the same number and
// turns that one port off again. It never runs `tailscale serve reset`,
// which drops every port of every project.
package tailscale

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jbigler/railswitch/internal/sys"
)

// T runs tailscale through R.
type T struct{ R sys.Runner }

// Target is what railswitch serves tailnet HTTPS port port to: the same
// port on loopback.
func Target(port int) string { return "http://127.0.0.1:" + strconv.Itoa(port) }

func (t T) run(ctx context.Context, args ...string) ([]byte, error) {
	res, err := t.R.Run(ctx, sys.Cmd{Name: "tailscale", Args: args})
	if err != nil {
		return nil, fmt.Errorf("tailscale %s: %w", strings.Join(args, " "), err)
	}
	return res.Stdout, nil
}

// Self is this machine's tailnet host name (`tailscale status --json`
// .Self.DNSName, without the trailing dot) and its first IPv4 address
// (.Self.TailscaleIPs). A node that is not running is an error.
func (t T) Self(ctx context.Context) (string, netip.Addr, error) {
	out, err := t.run(ctx, "status", "--json")
	if err != nil {
		return "", netip.Addr{}, err
	}
	var st struct { // ipnstate.Status
		BackendState string
		Self         *struct {
			DNSName      string
			TailscaleIPs []netip.Addr
		}
	}
	if err := json.Unmarshal(out, &st); err != nil {
		return "", netip.Addr{}, fmt.Errorf("tailscale status --json: %w", err)
	}
	if st.BackendState != "Running" || st.Self == nil || st.Self.DNSName == "" {
		return "", netip.Addr{}, fmt.Errorf("tailscale is not connected (state %q)", st.BackendState)
	}
	host := strings.TrimSuffix(st.Self.DNSName, ".")
	if !hostRe.MatchString(host) { // it goes into env values and AddHost=
		return "", netip.Addr{}, fmt.Errorf("tailscale DNS name %q is not a host name", st.Self.DNSName)
	}
	for _, ip := range st.Self.TailscaleIPs {
		if ip.Is4() {
			return host, ip, nil
		}
	}
	return "", netip.Addr{}, fmt.Errorf("tailscale reports no IPv4 address for %s", host)
}

var hostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)

// serveConfig is the part of ipn.ServeConfig (`tailscale serve status
// --json`) that says which port serves what.
type serveConfig struct {
	TCP map[uint16]struct {
		HTTPS, HTTP bool
		TCPForward  string
	}
	Web map[string]struct { // "<host>:<port>"
		Handlers map[string]handler // mount point → handler
	}
	Foreground map[string]*serveConfig // session → its foreground config
}

type handler struct{ Path, Proxy, Text, Redirect string }

func (h handler) String() string {
	switch {
	case h.Proxy != "":
		return h.Proxy
	case h.Path != "":
		return "path " + h.Path
	case h.Redirect != "":
		return "redirect " + h.Redirect
	}
	return "text"
}

// Served maps every tailnet port the serve config holds to what it serves:
// the proxy target of a port whose only handler is a proxy at "/", else a
// description ("tcp <addr>", "/: <target>, /docs: path /srv"…). Ports of
// foreground sessions are included, unless the background config serves
// the same port: that entry is the one `serve … off` acts on.
func (t T) Served(ctx context.Context) (map[int]string, error) {
	out, err := t.run(ctx, "serve", "status", "--json")
	if err != nil {
		return nil, err
	}
	var sc serveConfig
	if err := json.Unmarshal(out, &sc); err != nil {
		return nil, fmt.Errorf("tailscale serve status --json: %w", err)
	}
	served := map[int]string{}
	if err := sc.collect(served); err != nil {
		return nil, fmt.Errorf("tailscale serve status --json: %w", err)
	}
	return served, nil
}

func (sc *serveConfig) collect(served map[int]string) error {
	for port, h := range sc.TCP {
		served[int(port)] = "no handler"
		if h.TCPForward != "" {
			served[int(port)] = "tcp " + h.TCPForward
		}
	}
	for hp, w := range sc.Web {
		_, p, err := net.SplitHostPort(hp)
		if err != nil {
			return err
		}
		port, err := strconv.Atoi(p)
		if err != nil {
			return fmt.Errorf("web %q: %w", hp, err)
		}
		if h, ok := w.Handlers["/"]; ok && len(w.Handlers) == 1 {
			served[port] = h.String()
			continue
		}
		var parts []string
		for _, mount := range slices.Sorted(maps.Keys(w.Handlers)) {
			parts = append(parts, mount+": "+w.Handlers[mount].String())
		}
		served[port] = strings.Join(parts, ", ")
	}
	// railswitch writes only the background config, and `serve … off` edits
	// only that one: a foreground session's port is never railswitch's, and
	// it never hides the background entry of the same port.
	for _, f := range sc.Foreground {
		if f == nil {
			continue
		}
		fg := map[int]string{}
		if err := f.collect(fg); err != nil {
			return err
		}
		for port, desc := range fg {
			if _, ok := served[port]; !ok {
				served[port] = "foreground session: " + desc
			}
		}
	}
	return nil
}

// ServeOn serves tailnet HTTPS port port to Target(port), in the
// background: `tailscale serve --bg --https=<port> http://127.0.0.1:<port>`.
func (t T) ServeOn(ctx context.Context, port int) error {
	p := strconv.Itoa(port)
	if _, err := t.run(ctx, "serve", "--bg", "--https="+p, Target(port)); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "denied") {
			return fmt.Errorf("serve port %d: %w\nlet your user change the serve config: sudo tailscale set --operator=$USER", port, err)
		}
		return fmt.Errorf("serve port %d: %w", port, err)
	}
	return nil
}

// ServeOff turns tailnet HTTPS port port off: `tailscale serve
// --https=<port> off`. Every other port is left as it is.
func (t T) ServeOff(ctx context.Context, port int) error {
	if _, err := t.run(ctx, "serve", "--https="+strconv.Itoa(port), "off"); err != nil {
		return fmt.Errorf("turn off port %d: %w", port, err)
	}
	return nil
}
