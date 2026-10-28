package cli

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/buildinfo"
	"github.com/jbigler/railswitch/internal/jobs"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/term"
	"github.com/jbigler/railswitch/internal/web"
)

const dashboardServeUsage = "usage: railswitch dashboard serve"

// exeCheckEvery is how often serve looks whether its executable changed.
const exeCheckEvery = 5 * time.Second

// cliTimeout bounds one read the dashboard runs (pr ls waits up to 30 s
// for gh, project detect up to 2 min for git).
const cliTimeout = 150 * time.Second

// railswitch dashboard serve: the dashboard itself (its unit's ExecStart).
// It listens on machine.DashboardAddr until stopped, or until its
// executable is replaced while no job runs: it exits 0 and systemd starts
// the new binary.
func dashboardServe(ctx context.Context, e *env, args []string) int {
	fs := e.flags("dashboard serve")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("%s", dashboardServeUsage)
	}
	dirs, err := machine.DefaultDirs()
	if err != nil {
		return e.fail(err)
	}
	tok, err := machine.DashboardToken(dirs)
	if err != nil {
		return e.fail(err)
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return e.fail(err)
	}
	hub, err := jobs.New(e.run, filepath.Join(dirs.Data, "dashboard", "jobs"), func(ctx context.Context) []string {
		return sessionEnv(ctx, e.run, os.Environ())
	})
	if err != nil {
		return e.fail(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return e.fail(err)
	}
	ln, err := net.Listen("tcp", machine.DashboardAddr)
	if err != nil {
		return e.fail(err)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	srv := web.New(web.Config{Token: tok, CLI: selfCLI{r: e.run, exe: exe}, Jobs: hub, Exe: exe, Home: home, Version: buildinfo.Version, Now: time.Now})
	hs := &http.Server{
		Addr: machine.DashboardAddr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(net.Listener) context.Context { return ctx }, // ends the event streams
	}
	ran := make(chan error, 1)
	go func() { ran <- srv.Run(ctx) }()
	served := make(chan error, 1)
	go func() { served <- hs.Serve(ln) }()
	watched := make(chan bool, 1)
	go func() { watched <- watchExe(ctx, exe, exeCheckEvery, hub.Running) }()

	var serveErr error
	select {
	case <-ctx.Done(): // stopped
	case <-watched: // a new binary: systemd starts it
	case serveErr = <-served:
	}
	cancel()
	shut, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	_ = hs.Shutdown(shut)
	<-ran
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return e.fail(serveErr)
	}
	return 0
}

// watchExe reports true once the file at path has another size or mtime
// than at the start and busy says no job runs; false when ctx ends first.
func watchExe(ctx context.Context, path string, every time.Duration, busy func() bool) bool {
	fi, err := os.Stat(path)
	if err != nil {
		<-ctx.Done()
		return false
	}
	size, mod := fi.Size(), fi.ModTime()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
		}
		// A failed stat (the file being replaced) is looked at again.
		if now, err := os.Stat(path); err == nil && (now.Size() != size || !now.ModTime().Equal(mod)) && !busy() {
			return true
		}
	}
}

// selfCLI runs the dashboard's own executable for its reads.
type selfCLI struct {
	r   sys.Runner
	exe string
}

// Run returns the command's stdout; a failure is its last stderr line
// without the "railswitch: " prefix (the page shows it).
func (c selfCLI) Run(ctx context.Context, args ...string) ([]byte, error) {
	return c.RunInput(ctx, nil, args...)
}

// RunInput is Run with stdin on the command's standard input (nil: none).
func (c selfCLI) RunInput(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	cmd := sys.Cmd{Name: c.exe, Args: args, Timeout: cliTimeout}
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	res, err := c.r.Run(ctx, cmd)
	if err == nil {
		return res.Stdout, nil
	}
	if errors.Is(err, sys.ErrExit) {
		lines := strings.Split(strings.TrimSpace(string(res.Stderr)), "\n")
		if msg := strings.TrimPrefix(strings.TrimSpace(lines[len(lines)-1]), "railswitch: "); msg != "" {
			return nil, errors.New(msg)
		}
	}
	return nil, err
}

// sessionKeys are the session's variables a job gets: what a terminal
// window, the display and the SSH agent need (internal/term reads the
// rest).
var sessionKeys = append([]string{"DISPLAY", "WAYLAND_DISPLAY", "XDG_CURRENT_DESKTOP", "XDG_RUNTIME_DIR", "SSH_AUTH_SOCK"}, term.EnvKeys()...)

// sessionEnv is base with sessionKeys overlaid from the user session's
// environment (`systemctl --user show-environment`; a unit doesn't
// inherit the desktop's). A failed read leaves base as it is.
func sessionEnv(ctx context.Context, r sys.Runner, base []string) []string {
	res, err := r.Run(ctx, sys.Cmd{Name: "systemctl", Args: []string{"--user", "show-environment"}, Timeout: 10 * time.Second})
	if err != nil {
		return base
	}
	session := map[string]string{}
	for line := range bytes.Lines(res.Stdout) {
		k, v, ok := strings.Cut(strings.TrimRight(string(line), "\n"), "=")
		if ok && slices.Contains(sessionKeys, k) {
			session[k] = unquoteEnv(v)
		}
	}
	out := make([]string, 0, len(base)+len(session))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if _, ok := session[k]; !ok {
			out = append(out, kv)
		}
	}
	for _, k := range sessionKeys {
		if v, ok := session[k]; ok {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// unquoteEnv decodes a show-environment value: $'…' with C escapes, or
// "…" with shell escapes, or as it is.
func unquoteEnv(v string) string {
	if len(v) >= 3 && strings.HasPrefix(v, "$'") && strings.HasSuffix(v, "'") {
		if s, ok := cUnescape(v[2 : len(v)-1]); ok {
			return s
		}
		return v
	}
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		var b strings.Builder
		in := v[1 : len(v)-1]
		for i := 0; i < len(in); i++ {
			if in[i] == '\\' && i+1 < len(in) && strings.IndexByte("\"\\$`", in[i+1]) >= 0 {
				i++
			}
			b.WriteByte(in[i])
		}
		return b.String()
	}
	return v
}

// cUnescape decodes the escapes of a $'…' string; ok false for a bad one.
func cUnescape(s string) (string, bool) {
	simple := map[byte]byte{'a': '\a', 'b': '\b', 'e': 0x1b, 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t', 'v': '\v', '\\': '\\', '\'': '\'', '"': '"', '?': '?'}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if i+1 >= len(s) {
			return "", false
		}
		i++
		if r, ok := simple[s[i]]; ok {
			b.WriteByte(r)
			continue
		}
		base, digits := 8, 3
		if s[i] == 'x' {
			base, digits = 16, 2
			i++
		}
		digit := func(c byte) bool {
			if base == 8 {
				return c >= '0' && c <= '7'
			}
			return strings.IndexByte("0123456789abcdefABCDEF", c) >= 0
		}
		j := i
		for j < len(s) && j-i < digits && digit(s[j]) {
			j++
		}
		n, err := strconv.ParseUint(s[i:j], base, 8)
		if j == i || err != nil {
			return "", false
		}
		b.WriteByte(byte(n))
		i = j - 1
	}
	return b.String(), true
}
