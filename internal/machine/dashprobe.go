package machine

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DashboardHost is the Host the dashboard serves under (its guard allows
// it): the probe sends it so another program on the port is told apart.
const DashboardHost = "railswitch.localhost:7700"

// Probe is what answers at the dashboard's address.
type Probe struct {
	Ours    bool   // railswitch's dashboard answered /healthz
	Version string // its version, when Ours
	// Other is how something else answered (its status and first line);
	// "" when nothing answered.
	Other string
	Err   error // the connection's error when nothing answered
}

// ProbeDashboard asks addr's /healthz as the dashboard's browser would; only
// railswitch's dashboard answers 200 "ok <version>".
func ProbeDashboard(ctx context.Context, addr string) Probe {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/healthz", nil)
	if err != nil {
		return Probe{Err: err}
	}
	req.Host = DashboardHost
	resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		return Probe{Err: err}
	}
	defer resp.Body.Close()
	line, _ := bufio.NewReader(io.LimitReader(resp.Body, 512)).ReadString('\n')
	line = strings.TrimSpace(line)
	if v, ok := strings.CutPrefix(line, "ok "); ok && resp.StatusCode == http.StatusOK {
		return Probe{Ours: true, Version: v}
	}
	return Probe{Other: fmt.Sprintf("%d %q", resp.StatusCode, line)}
}

// String says what answered, for a terminal.
func (p Probe) String() string {
	switch {
	case p.Ours:
		return "railswitch dashboard " + p.Version
	case p.Other != "":
		return "another program answers on " + DashboardAddr + " (" + p.Other + ")"
	}
	return "nothing answers on " + DashboardAddr + ": " + p.Err.Error()
}

// ProbeFix is the next step for a probe that isn't Ours.
func (p Probe) Fix() string {
	if p.Other != "" {
		return "stop the program listening on " + DashboardAddr + " (ss -ltnp 'sport = :7700' names it), then systemctl --user restart " + DashboardService
	}
	return "railswitch dashboard; if it keeps failing: journalctl --user -u " + DashboardService
}
