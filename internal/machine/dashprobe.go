package machine

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

// ProbeProof is the dashboard's answer to a probe's nonce: an HMAC keyed
// by the login token, which only a holder of the token can give, and
// which gives nothing to log in with.
func ProbeProof(tok, nonce string) string {
	m := hmac.New(sha256.New, []byte(tok))
	m.Write([]byte("railswitch-dashboard-probe:" + nonce))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

// NonceOK is whether n is a probe's nonce: 32 hex.
func NonceOK(n string) bool { return nonceRe.MatchString(n) }

// ReadDashboardToken is the token file's token, "" when there is none
// yet. It never creates the token (doctor reads, never writes).
func ReadDashboardToken(d Dirs) (string, error) {
	tok, err := readToken(filepath.Join(d.Data, "dashboard", "token"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return tok, err
}

// DashboardAddrs are where the dashboard listens and is probed: IPv4
// loopback, which must answer, and IPv6 loopback, which may refuse (IPv6
// off: browsers don't use it either).
var DashboardAddrs = []string{DashboardAddr, DashboardAddr6}

// Probe is what answers at one of the dashboard's addresses.
type Probe struct {
	Addr    string // the address asked
	Ours    bool   // the dashboard proved it holds this user's token
	Version string // its version, when it answered as a railswitch dashboard
	// Unproved: a railswitch dashboard answered without the proof (one from
	// before it, still running) or with a wrong one (another user's, an
	// ssh -L forward's, or a program imitating it).
	Unproved bool
	// Other is how something else answered: its status and first line, or
	// its error when it took the connection but gave no HTTP answer.
	Other string
	Err   error // the connection was refused: nothing listens
}

var (
	versionRe = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,64}$`)
	nonceRe   = regexp.MustCompile(`^[0-9a-f]{32}$`)
	proofRe   = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// ProbeDashboard asks addr's /healthz as the dashboard's browser would,
// with a fresh nonce. railswitch's dashboard answers 200 "ok <version>
// <ProbeProof(token, nonce)>"; only the proof made with tok is Ours.
func ProbeDashboard(ctx context.Context, addr, tok string) (p Probe) {
	defer func() { p.Addr = addr }()
	nb := make([]byte, 16)
	rand.Read(nb) // never fails (crypto/rand)
	nonce := hex.EncodeToString(nb)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/healthz?nonce="+url.QueryEscape(nonce), nil)
	if err != nil {
		return Probe{Err: err}
	}
	req.Host = DashboardHost
	resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if nothingThere(err) {
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
		if len(f) == 3 && proofRe.MatchString(f[2]) && tok != "" && hmac.Equal([]byte(f[2]), []byte(ProbeProof(tok, nonce))) {
			return Probe{Ours: true, Version: f[1]}
		}
		if len(f) == 2 || proofRe.MatchString(f[2]) {
			return Probe{Unproved: true, Version: f[1]}
		}
	}
	return Probe{Other: fmt.Sprintf("%d %q", resp.StatusCode, line)}
}

// String says what answered, for a terminal.
func (p Probe) String() string {
	switch {
	case p.Ours:
		return "railswitch dashboard " + p.Version
	case p.Unproved:
		return "a railswitch dashboard (" + p.Version + ") answers on " + p.Addr + " but not with your login: an older one still running, one started before the login token changed, another user's, or an ssh -L forward of the port"
	case p.Other != "":
		return "another program answers on " + p.Addr + " (" + p.Other + ")"
	}
	return "nothing answers on " + p.Addr + ": " + p.Err.Error()
}

// Fix is the next step for a probe that isn't Ours.
func (p Probe) Fix() string {
	switch {
	case p.Unproved:
		return "systemctl --user restart " + DashboardService + "; if it still fails, stop the program listening on " + p.Addr + " (ss -ltnp 'sport = :7700' names it)"
	case p.Other != "":
		return "stop the program listening on " + p.Addr + " (ss -ltnp 'sport = :7700' names it), then systemctl --user restart " + DashboardService
	}
	return "railswitch dashboard; if it keeps failing: journalctl --user -u " + DashboardService
}

// ProbeDashboards probes each of addrs (the first must answer; a later one
// may refuse: nothing there, so browsers use the others) and returns the
// first that isn't Ours, or the first's when all are.
func ProbeDashboards(ctx context.Context, addrs []string, tok string) Probe {
	var first Probe
	for i, a := range addrs {
		p := ProbeDashboard(ctx, a, tok)
		switch {
		case i == 0:
			first = p
			if !p.Ours {
				return p
			}
		case p.Ours, p.Err != nil:
		default:
			return p
		}
	}
	return first
}

// nothingThere is whether err means no program listens at the address:
// refused, or (IPv6 off) no such address or network.
func nothingThere(err error) bool {
	for _, e := range []syscall.Errno{syscall.ECONNREFUSED, syscall.EADDRNOTAVAIL, syscall.ENETUNREACH, syscall.EAFNOSUPPORT} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}
