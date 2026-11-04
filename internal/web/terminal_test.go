package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jbigler/railswitch/internal/sys"
)

// TestTTYArgv: each kind runs its fixed railswitch command.
func TestTTYArgv(t *testing.T) {
	wsb := []string{testExe, "-C", "/ws/ftest", "-w", "b"}
	for _, c := range []struct {
		kind, agent string
		want        []string
	}{
		{"agent", "", append(slices.Clone(wsb), "agent")},
		{"agent", "probe", append(slices.Clone(wsb), "agent", "probe")},
		{"nvim", "", append(slices.Clone(wsb), "nvim")},
		{"shell", "", append(slices.Clone(wsb), "exec", "--", "sh", "-c", "command -v bash >/dev/null && exec bash -l; exec sh -l")},
		{"bash", "", nil},
	} {
		got, ok := ttyArgv(testExe, "/ws/ftest", "b", c.kind, c.agent)
		if ok != (c.want != nil) || !slices.Equal(got, c.want) {
			t.Errorf("%s %q: %q, %v; want %q", c.kind, c.agent, got, ok, c.want)
		}
	}
}

// ttyFixture is a live server whose terminals run script (bash) in place
// of railswitch.
func ttyFixture(t *testing.T, script string) (*fixture, *httptest.Server) {
	t.Helper()
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	f := newFixture(t)
	exe := filepath.Join(t.TempDir(), "railswitch")
	if err := os.WriteFile(exe, []byte("#!/bin/bash\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.s.c.Exe = exe
	f.s.c.Run = sys.Exec{}
	f.s.c.Env = func(context.Context) []string { return []string{"PATH=" + os.Getenv("PATH")} }
	srv := httptest.NewServer(f.s.Handler())
	t.Cleanup(srv.Close)
	return f, srv
}

// dialTTY opens a terminal as the dashboard page does: allowed Host, the
// cookie, the page's Origin (origin "" = that one).
func dialTTY(t *testing.T, srv *httptest.Server, path, origin string, cookie bool) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	if origin == "" {
		origin = "http://" + testHost
	}
	h := http.Header{"Origin": {origin}}
	if cookie {
		h.Set("Cookie", "railswitch_dashboard="+testToken)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+path, &websocket.DialOptions{HTTPHeader: h, Host: testHost})
	if c != nil {
		t.Cleanup(func() { c.CloseNow() })
	}
	return c, resp, err
}

// readAll reads until the connection closes: the output, the last text
// frame, and the close error.
func readAll(t *testing.T, c *websocket.Conn, until string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out strings.Builder
	var text string
	for {
		typ, b, err := c.Read(ctx)
		if err != nil {
			return out.String(), text, err
		}
		if typ == websocket.MessageText {
			text = string(b)
			continue
		}
		out.Write(b)
		if until != "" && strings.Contains(out.String(), until) {
			return out.String(), text, nil
		}
	}
}

func closeOf(err error) (websocket.StatusCode, string) {
	var ce websocket.CloseError
	if errors.As(err, &ce) {
		return ce.Code, ce.Reason
	}
	return -1, ""
}

// TestTTYOrigin: another site's page, though same-site to the cookie, is
// refused before the upgrade; so is a request without the cookie.
func TestTTYOrigin(t *testing.T) {
	_, srv := ttyFixture(t, "exec cat")
	if _, resp, err := dialTTY(t, srv, "/p/ftest/tty/b/shell?cols=80&rows=24", "http://app.foo.localhost", true); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("foreign Origin: %v, %v; want 403", resp, err)
	}
	if _, resp, err := dialTTY(t, srv, "/p/ftest/tty/b/shell?cols=80&rows=24", "", false); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no cookie: %v, %v; want 401", resp, err)
	}
}

// TestTTYRefusals: what can't run is accepted, then closed with a code and
// a reason the panel shows.
func TestTTYRefusals(t *testing.T) {
	_, srv := ttyFixture(t, "exec cat")
	for _, c := range []struct {
		path   string
		send   string
		code   websocket.StatusCode
		reason string
	}{
		{"/p/ftest/tty/nope/shell?cols=80&rows=24", "", 4004, "unknown worktree"},
		{"/p/ftest/tty/b/bash?cols=80&rows=24", "", 4000, "unknown terminal bash"},
		{"/p/ftest/tty/b/agent?agent=nope&cols=80&rows=24", "", 4000, "unknown agent nope"},
		{"/p/ftest/tty/b/agent?agent=-x&cols=80&rows=24", "", 4000, "unknown agent -x"},
		{"/p/ftest/tty/b/shell?cols=0&rows=24", "", 4000, "bad size"},
		{"/p/ftest/tty/b/shell?cols=80&rows=1001", "", 4000, "bad size"},
		{"/p/ftest/tty/b/shell?cols=80&rows=24", "hello", 4000, "bad message"},
		{"/p/ftest/tty/b/shell?cols=80&rows=24", `{"cols":0,"rows":5}`, 4000, "bad message"},
	} {
		conn, _, err := dialTTY(t, srv, c.path, "", true)
		if err != nil {
			t.Fatalf("%s: %v", c.path, err)
		}
		if c.send != "" {
			_ = conn.Write(context.Background(), websocket.MessageText, []byte(c.send))
		}
		_, _, err = readAll(t, conn, "")
		if code, reason := closeOf(err); code != c.code || reason != c.reason {
			t.Errorf("%s %q: closed %d %q; want %d %q", c.path, c.send, code, reason, c.code, c.reason)
		}
	}
}

// TestTTYLimit: a ninth terminal is refused while eight are open.
func TestTTYLimit(t *testing.T) {
	f, srv := ttyFixture(t, "exec sleep 60")
	var open []*websocket.Conn
	for range ttyMax {
		c, _, err := dialTTY(t, srv, "/p/ftest/tty/b/shell?cols=80&rows=24", "", true)
		if err != nil {
			t.Fatal(err)
		}
		open = append(open, c)
	}
	deadline := time.Now().Add(3 * time.Second)
	for f.s.ttyOpen() < ttyMax && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	c, _, err := dialTTY(t, srv, "/p/ftest/tty/b/shell?cols=80&rows=24", "", true)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = readAll(t, c, "")
	if code, reason := closeOf(err); code != 4029 || reason != "8 terminals are open: close one" {
		t.Errorf("ninth: %d %q", code, reason)
	}
	for _, c := range open {
		c.Close(websocket.StatusNormalClosure, "")
	}
}

// TestTTYEcho: typed keys reach the program and its output comes back.
func TestTTYEcho(t *testing.T) {
	_, srv := ttyFixture(t, "exec cat")
	c, _, err := dialTTY(t, srv, "/p/ftest/tty/b/shell?cols=80&rows=24", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Write(context.Background(), websocket.MessageBinary, []byte("abc\r")); err != nil {
		t.Fatal(err)
	}
	if out, _, err := readAll(t, c, "abc"); err != nil {
		t.Fatalf("%q: %v", out, err)
	}
}

// TestTTYResize: the start size, then a resize message, reach the program;
// its exit code arrives before a normal close.
func TestTTYResize(t *testing.T) {
	_, srv := ttyFixture(t, "stty size; read x; stty size; exit 0")
	c, _, err := dialTTY(t, srv, "/p/ftest/tty/b/shell?cols=81&rows=21", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := readAll(t, c, "21 81"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"cols":90,"rows":20}`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := c.Write(ctx, websocket.MessageBinary, []byte("\r")); err != nil {
		t.Fatal(err)
	}
	out, text, err := readAll(t, c, "")
	if !strings.Contains(out, "20 90") || text != `{"exit":0}` {
		t.Errorf("out %q, text %q", out, text)
	}
	if code, _ := closeOf(err); code != websocket.StatusNormalClosure {
		t.Errorf("closed %d", code)
	}
}

// TestTTYExitCode: the exit code arrives even when a background child
// still holds the terminal.
func TestTTYExitCode(t *testing.T) {
	_, srv := ttyFixture(t, "sleep 30 & exit 3")
	c, _, err := dialTTY(t, srv, "/p/ftest/tty/b/shell?cols=80&rows=24", "", true)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, text, _ := readAll(t, c, "")
	if text != `{"exit":3}` || time.Since(start) > 3*time.Second {
		t.Errorf("text %q after %v", text, time.Since(start))
	}
}

// TestTTYEnv: the program sees an xterm's TERM and COLORTERM.
func TestTTYEnv(t *testing.T) {
	_, srv := ttyFixture(t, `echo "env:$TERM:$COLORTERM"`)
	c, _, err := dialTTY(t, srv, "/p/ftest/tty/b/shell?cols=80&rows=24", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if out, _, _ := readAll(t, c, ""); !strings.Contains(out, "env:xterm-256color:truecolor") {
		t.Errorf("out %q", out)
	}
}

// TestTTYBackground: an nvim terminal gets the panel's bg as
// NVIM_BACKGROUND; another value, or another kind, gets none.
func TestTTYBackground(t *testing.T) {
	_, srv := ttyFixture(t, `echo "bg:${NVIM_BACKGROUND-unset}:"`)
	for path, want := range map[string]string{
		"/p/ftest/tty/b/nvim?cols=80&rows=24&bg=light":  "bg:light:",
		"/p/ftest/tty/b/nvim?cols=80&rows=24&bg=dark":   "bg:dark:",
		"/p/ftest/tty/b/nvim?cols=80&rows=24&bg=;x":     "bg:unset:",
		"/p/ftest/tty/b/shell?cols=80&rows=24&bg=light": "bg:unset:",
	} {
		c, _, err := dialTTY(t, srv, path, "", true)
		if err != nil {
			t.Fatal(path, err)
		}
		if out, _, _ := readAll(t, c, ""); !strings.Contains(out, want) {
			t.Errorf("%s: out %q, want %q", path, out, want)
		}
	}
}

// waitGone waits until pid (read from file) no longer exists.
func waitGone(t *testing.T, file string) {
	t.Helper()
	var pid int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(file)
		if p, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
			pid = p
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("no pid written")
	}
	for !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		if time.Now().After(deadline) {
			t.Fatalf("pid %d outlived its terminal", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestTTYClientClose: closing the panel ends the program and frees the slot.
func TestTTYClientClose(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	f, srv := ttyFixture(t, "echo $$ > "+pidFile+"; exec sleep 60")
	c, _, err := dialTTY(t, srv, "/p/ftest/tty/b/shell?cols=80&rows=24", "", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, err := os.Stat(pidFile); err != nil; _, err = os.Stat(pidFile) {
		time.Sleep(10 * time.Millisecond)
	}
	c.Close(websocket.StatusNormalClosure, "")
	waitGone(t, pidFile)
	deadline := time.Now().Add(2 * time.Second)
	for f.s.ttyOpen() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d terminals still counted", f.s.ttyOpen())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestTTYPing: a peer that never answers a ping is dropped, its program
// with it.
func TestTTYPing(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	f, srv := ttyFixture(t, "echo $$ > "+pidFile+"; exec sleep 60")
	f.s.ttyPing, f.s.ttyPong = 50*time.Millisecond, 50*time.Millisecond
	if _, _, err := dialTTY(t, srv, "/p/ftest/tty/b/shell?cols=80&rows=24", "", true); err != nil {
		t.Fatal(err)
	}
	// never read: the client library answers pings only while reading
	waitGone(t, pidFile)
}
