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
		args        []string
		want        []string
	}{
		{"agent", "", nil, append(slices.Clone(wsb), "agent")},
		{"agent", "probe", nil, append(slices.Clone(wsb), "agent", "probe")},
		{"nvim", "", nil, append(slices.Clone(wsb), "nvim")},
		{"shell", "", nil, append(slices.Clone(wsb), "exec", "--", "sh", "-c", "command -v bash >/dev/null && exec bash -l; exec sh -l")},
		{"task", "rails:test:watcher", nil, append(slices.Clone(wsb), "task", "rails:test:watcher")},
		{"task", "test", []string{"a", "b c"}, append(slices.Clone(wsb), "task", "test", "a", "b c")},
		{"task", "", nil, nil},
		{"bash", "", nil, nil},
	} {
		got, ok := ttyArgv(testExe, "/ws/ftest", "b", c.kind, c.agent, c.args)
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
	f.s.ttyBase = t.Context() // a terminal outlives its connection: the test's end ends it
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
		{"/p/ftest/tty/b/task?task=nope&cols=80&rows=24", "", 4000, "unknown task nope"},
		{"/p/ftest/tty/b/task?cols=80&rows=24", "", 4000, "unknown task "},
		{"/p/ftest/tty/b/task?task=test&args=%22x&cols=80&rows=24", "", 4000, `args: unterminated " quote`},
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

// TestTTYLimit: a ninth terminal is refused while eight run, attached or
// not; a second connection to one of them is not a ninth.
func TestTTYLimit(t *testing.T) {
	f, srv := ttyFixture(t, "exec sleep 60")
	for i := range ttyMax {
		c, _, err := dialTTY(t, srv, "/p/ftest/tty/b/task?task=test&args="+strconv.Itoa(i)+"&cols=80&rows=24", "", true)
		if err != nil {
			t.Fatal(err)
		}
		if i%2 == 0 {
			c.Close(websocket.StatusNormalClosure, "") // the page left: it still runs
		}
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
	if code, reason := closeOf(err); code != 4029 || reason != "8 terminals are running: end one from ▣ in the header" {
		t.Errorf("ninth: %d %q", code, reason)
	}
	if _, _, err := dialTTY(t, srv, "/p/ftest/tty/b/task?task=test&args=0&cols=80&rows=24", "", true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if n := f.s.ttyOpen(); n != ttyMax {
		t.Errorf("%d terminals after opening one again, want %d", n, ttyMax)
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

// TestTTYTask: a task terminal runs `railswitch task <name>` for one of the
// worktree's tasks (D175).
func TestTTYTask(t *testing.T) {
	_, srv := ttyFixture(t, `echo "argv:$*"`)
	c, _, err := dialTTY(t, srv, "/p/ftest/tty/b/task?task=test&args=a+%22b+c%22&cols=80&rows=24", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if out, _, _ := readAll(t, c, ""); !strings.Contains(out, "argv:-C /ws/ftest -w b task test a b c") {
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

// waitPID reads the pid the program wrote to file.
func waitPID(t *testing.T, file string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(file)
		if p, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
			return p
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no pid written")
	return 0
}

// ttyRows is GET /p/ftest/ttys.
func ttyRows(t *testing.T, f *fixture) string {
	t.Helper()
	w := f.do(req("GET", "/p/ftest/ttys", nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	return strings.TrimSpace(w.Body.String())
}

// TestTTYDetach (D205): a connection that goes leaves the program running
// and listed; the next one gets what was printed, and the first, if still
// there, is told why it lost the terminal. The panel's close ends it.
func TestTTYDetach(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	f, srv := ttyFixture(t, "echo $$ > "+pidFile+"; echo before; read x; echo after:$x; exec sleep 60")
	const path = "/p/ftest/tty/b/task?task=test&args=a%20b&cols=80&rows=24&bg=dark"
	c, _, err := dialTTY(t, srv, path, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := readAll(t, c, "before"); err != nil {
		t.Fatal(err)
	}
	c.Close(websocket.StatusNormalClosure, "")
	pid := waitPID(t, pidFile)
	time.Sleep(100 * time.Millisecond)
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("the program ended with its connection: %v", err)
	}
	if got, want := ttyRows(t, f), `[{"slug":"b","kind":"task","label":"test a b","url":"/p/ftest/tty/b/task?task=test&args=a%20b"}]`; got != want {
		t.Errorf("list %s, want %s", got, want)
	}

	c2, _, err := dialTTY(t, srv, path, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if out, _, err := readAll(t, c2, "before"); err != nil {
		t.Fatalf("no replay: %q, %v", out, err)
	}
	c3, _, err := dialTTY(t, srv, path, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := readAll(t, c2, ""); func() bool { code, _ := closeOf(err); return code != 4001 }() {
		t.Errorf("the displaced connection closed with %v, want 4001", err)
	}
	ctx := context.Background()
	if err := c3.Write(ctx, websocket.MessageBinary, []byte("x\r")); err != nil {
		t.Fatal(err)
	}
	// The mark between what was printed before and what is new: the page
	// answers the program's questions only after it.
	if out, text, err := readAll(t, c3, "after:x"); err != nil || text != `{"live":true}` {
		t.Fatalf("%q, text %q: %v", out, text, err)
	}
	if n := f.s.ttyOpen(); n != 1 {
		t.Errorf("%d terminals counted, want 1", n)
	}

	if err := c3.Write(ctx, websocket.MessageText, []byte(`{"close":true}`)); err != nil {
		t.Fatal(err)
	}
	waitGone(t, pidFile)
	deadline := time.Now().Add(2 * time.Second)
	for f.s.ttyOpen() != 0 || ttyRows(t, f) != "[]" {
		if time.Now().After(deadline) {
			t.Fatalf("%d terminals still counted, list %s", f.s.ttyOpen(), ttyRows(t, f))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestTTYEndedUnseen: a program that ends with no page attached keeps its
// last output and exit code for the next page, once.
func TestTTYEndedUnseen(t *testing.T) {
	f, srv := ttyFixture(t, "echo started; read x; echo result; exit 4")
	const path = "/p/ftest/tty/b/shell?cols=80&rows=24"
	c, _, err := dialTTY(t, srv, path, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := readAll(t, c, "started"); err != nil {
		t.Fatal(err)
	}
	if err := c.Write(context.Background(), websocket.MessageBinary, []byte("\r")); err != nil {
		t.Fatal(err)
	}
	c.CloseNow() // gone before the program ends
	deadline := time.Now().Add(5 * time.Second)
	for f.s.ttyOpen() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the program did not end")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(ttyRows(t, f), `"kind":"shell"`) {
		t.Fatalf("the ended terminal is not listed: %s", ttyRows(t, f))
	}
	c2, _, err := dialTTY(t, srv, path, "", true)
	if err != nil {
		t.Fatal(err)
	}
	out, text, _ := readAll(t, c2, "")
	if !strings.Contains(out, "result") || text != `{"exit":4}` {
		t.Errorf("out %q, text %q", out, text)
	}
	if got := ttyRows(t, f); got != "[]" {
		t.Errorf("list after its end was shown: %s", got)
	}
}

// TestTTYPing: a peer that never answers a ping is dropped; its program
// keeps running for the page that opens it next.
func TestTTYPing(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	f, srv := ttyFixture(t, "echo $$ > "+pidFile+"; exec sleep 60")
	f.s.ttyPing, f.s.ttyPong = 50*time.Millisecond, 50*time.Millisecond
	c, _, err := dialTTY(t, srv, "/p/ftest/tty/b/shell?cols=80&rows=24", "", true)
	if err != nil {
		t.Fatal(err)
	}
	pid := waitPID(t, pidFile)
	// never read: the client library answers pings only while reading
	time.Sleep(400 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Ping(ctx); err == nil {
		t.Error("the silent peer was not dropped")
	}
	if err := syscall.Kill(pid, 0); err != nil || f.s.ttyOpen() != 1 {
		t.Errorf("the program after its peer was dropped: %v, %d counted", err, f.s.ttyOpen())
	}
}

// TestTTYModes: the modes a program set are known across chunk
// boundaries, and a page that gets only the end of the output is sent them
// first.
func TestTTYModes(t *testing.T) {
	var m ttyModes
	for _, chunk := range []string{"a\x1b[?1049h\x1b[?1000;1006h b \x1b[?20", "04h\x1b[?25l\x1b[?1000l\x1b", "[?1h"} {
		m.scan([]byte(chunk))
	}
	if got, want := string(m.set()), "\x1b[?1h\x1b[?1006h\x1b[?1049h\x1b[?2004h"; got != want {
		t.Errorf("modes %q, want %q", got, want)
	}

	s := &ttySession{}
	s.modes.scan([]byte("\x1b[?1049h"))
	s.buf, s.cut = []byte("tail of a line\nscreen"), true
	if replay, _ := s.attach(); string(replay) != "\x1b[?1049hscreen" {
		t.Errorf("replay %q", replay)
	}
}

// TestTTYEndFromList (D205): the header counts the running terminals of
// every project, its list names each, and End there ends the program and
// tells the page showing it.
func TestTTYEndFromList(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	f, srv := ttyFixture(t, "echo $$ > "+pidFile+"; exec sleep 60")
	contains(t, "no terminal", f.do(req("GET", "/ttys", nil)).Body.String(), "0 of 8 running", "No terminal is running.")
	c, _, err := dialTTY(t, srv, "/p/ftest/tty/b/task?task=test&args=x&cols=80&rows=24", "", true)
	if err != nil {
		t.Fatal(err)
	}
	waitPID(t, pidFile)
	contains(t, "header", f.do(req("GET", "/p/ftest", nil)).Body.String(), `href="/ttys" data-dialog="ttys"`, "▣ 1/8")
	list := f.do(req("GET", "/ttys", nil)).Body.String()
	contains(t, "list", list, "1 of 8 running", `<a href="/p/ftest/b">b</a>`, `data-tty-go="/p/ftest/b" data-prefix="ftest" data-slug="b" data-tab="terminals"`, "task: test x", `action="/ttys/1/end" data-post data-close`)
	noCSPViolation(t, "list", list)

	if w := f.do(req("POST", "/ttys/9/end", nil)); w.Code != 404 {
		t.Errorf("an unknown terminal: %d", w.Code)
	}
	if w := f.do(req("POST", "/ttys/1/end", nil)); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if _, _, err := readAll(t, c, ""); func() bool { code, _ := closeOf(err); return code != 4003 }() {
		t.Errorf("the page's connection closed with %v, want 4003", err)
	}
	waitGone(t, pidFile)
	deadline := time.Now().Add(2 * time.Second)
	for f.s.ttyOpen() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the ended terminal is still counted")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if body := f.do(req("GET", "/p/ftest", nil)).Body.String(); strings.Contains(body, `data-dialog="ttys"`) {
		t.Error("the header still counts terminals")
	}
}
