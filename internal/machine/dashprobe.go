package machine

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// DashboardHost is the Host the dashboard serves under (its guard allows
// it): the probe sends it so another program on the port is told apart.
const DashboardHost = "railswitch.localhost:7700"

// TokenFingerprint is what /healthz shows of the login token: enough to
// tell this user's dashboard from another's, nothing to log in with.
func TokenFingerprint(tok string) string {
	sum := sha256.Sum256([]byte("railswitch-dashboard-token:" + tok))
	return hex.EncodeToString(sum[:6])
}

// DashboardFingerprint is the fingerprint of the token file's token, ""
// when there is none yet. It never creates the token.
func DashboardFingerprint(d Dirs) (string, error) {
	tok, err := readToken(filepath.Join(d.Data, "dashboard", "token"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return TokenFingerprint(tok), nil
}

// Probe is what answers at the dashboard's address.
type Probe struct {
	Ours    bool   // railswitch's dashboard with this user's token answered /healthz
	Version string // its version, when Ours or Foreign
	// Foreign: a railswitch dashboard with another login token answered
	// (another user's, or an ssh -L forward of the port to another machine).
	Foreign bool
	// Other is how something else answered: its status and first line, or
	// its error when it took the connection but gave no HTTP answer.
	Other string
	Err   error // the connection was refused: nothing listens
}

var (
	versionRe     = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,64}$`)
	fingerprintRe = regexp.MustCompile(`^[0-9a-f]{12}$`)
)

// ProbeDashboard asks addr's /healthz as the dashboard's browser would.
// Only railswitch's dashboard answers 200 "ok <version> <fingerprint>"; a
// dashboard from before the fingerprint answers "ok <version>" and counts
// as Ours. fp is this user's TokenFingerprint ("": don't compare).
func ProbeDashboard(ctx context.Context, addr, fp string) Probe {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/healthz", nil)
	if err != nil {
		return Probe{Err: err}
	}
	req.Host = DashboardHost
	resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if errors.Is(err, syscall.ECONNREFUSED) {
		return Probe{Err: err}
	}
	if err != nil {
		return Probe{Other: fmt.Sprintf("no HTTP answer: %q", err.Error())}
	}
	defer resp.Body.Close()
	line, _ := bufio.NewReader(io.LimitReader(resp.Body, 512)).ReadString('\n')
	line = strings.TrimSpace(line)
	f := strings.Fields(line)
	if resp.StatusCode == http.StatusOK && (len(f) == 2 || len(f) == 3) && f[0] == "ok" && versionRe.MatchString(f[1]) {
		switch {
		case len(f) == 2:
			return Probe{Ours: true, Version: f[1]}
		case fingerprintRe.MatchString(f[2]) && (fp == "" || f[2] == fp):
			return Probe{Ours: true, Version: f[1]}
		case fingerprintRe.MatchString(f[2]):
			return Probe{Foreign: true, Version: f[1]}
		}
	}
	return Probe{Other: fmt.Sprintf("%d %q", resp.StatusCode, line)}
}

// String says what answered, for a terminal.
func (p Probe) String() string {
	switch {
	case p.Ours:
		return "railswitch dashboard " + p.Version
	case p.Foreign:
		return "a railswitch dashboard with another login answers on " + DashboardAddr + " (another user's, or an ssh -L forward of the port)"
	case p.Other != "":
		return "another program answers on " + DashboardAddr + " (" + p.Other + ")"
	}
	return "nothing answers on " + DashboardAddr + ": " + p.Err.Error()
}

// Fix is the next step for a probe that isn't Ours.
func (p Probe) Fix() string {
	if p.Foreign || p.Other != "" {
		return "stop the program listening on " + DashboardAddr + " (ss -ltnp 'sport = :7700' names it), then systemctl --user restart " + DashboardService
	}
	return "railswitch dashboard; if it keeps failing: journalctl --user -u " + DashboardService
}
