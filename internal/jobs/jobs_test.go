package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/sys"
)

var wantScope = []string{"systemd-run", "--user", "--scope", "--collect", "--quiet", "--"}

// scopeRunner records every argv and runs it for real without the
// systemd-run prefix (no systemd in tests).
type scopeRunner struct {
	mu     sync.Mutex
	calls  [][]string
	stdins [][]byte // nil when the call had no stdin
}

func (s *scopeRunner) Run(ctx context.Context, c sys.Cmd) (sys.Result, error) {
	full := append([]string{c.Name}, c.Args...)
	var in []byte
	if c.Stdin != nil {
		in, _ = io.ReadAll(c.Stdin)
		c.Stdin = bytes.NewReader(in)
	}
	s.mu.Lock()
	s.calls = append(s.calls, full)
	s.stdins = append(s.stdins, in)
	s.mu.Unlock()
	if len(full) <= len(wantScope) || !slices.Equal(full[:len(wantScope)], wantScope) {
		return sys.Result{}, errors.New("not wrapped in a scope")
	}
	c.Name, c.Args = full[len(wantScope)], full[len(wantScope)+1:]
	return sys.Exec{}.Run(ctx, c)
}

func newHub(t *testing.T) (*Hub, *scopeRunner, string) {
	t.Helper()
	r := &scopeRunner{}
	dir := filepath.Join(t.TempDir(), "jobs")
	h, err := New(r, dir, func(context.Context) []string {
		return append(os.Environ(), "RS_TEST=injected")
	})
	if err != nil {
		t.Fatal(err)
	}
	return h, r, dir
}

func sh(project, slug, script string) Spec {
	return Spec{Project: project, Slug: slug, Label: "sh", Argv: []string{"sh", "-c", script}}
}

func find(h *Hub, project, id string) (Snapshot, bool) {
	for _, j := range h.Jobs(project, "") {
		if j.ID == id {
			return j, true
		}
	}
	return Snapshot{}, false
}

func waitDone(t *testing.T, h *Hub, s Snapshot) Snapshot {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		j, ok := find(h, s.Spec.Project, s.ID)
		if !ok {
			t.Fatalf("job %s gone", s.ID)
		}
		if j.State != StateRunning {
			return j
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s still running", s.ID)
	return Snapshot{}
}

func run(t *testing.T, h *Hub, s Spec) Snapshot {
	t.Helper()
	j, err := h.Start(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	return waitDone(t, h, j)
}

func TestStartWrapsInScope(t *testing.T) {
	h, r, _ := newHub(t)
	s := sh("p", "a", "exit 0")
	run(t, h, s)
	want := append(slices.Clone(wantScope), s.Argv...)
	if len(r.calls) != 1 || !slices.Equal(r.calls[0], want) {
		t.Fatalf("calls = %q, want [%q]", r.calls, want)
	}
}

func TestLogFileMode(t *testing.T) {
	r := &scopeRunner{}
	dir := filepath.Join(t.TempDir(), "jobs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "old.log")
	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := New(r, dir, func(context.Context) []string { return os.Environ() })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale log survived New: %v", err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m := fi.Mode().Perm(); m != 0o700 {
		t.Errorf("dir mode = %o, want 700", m)
	}
	j := run(t, h, sh("p", "a", "echo hi"))
	fi, err = os.Lstat(filepath.Join(dir, j.ID+".log"))
	if err != nil {
		t.Fatal(err)
	}
	if m := fi.Mode(); m != 0o600 {
		t.Errorf("log mode = %v, want -rw-------", m)
	}
}

func TestStdioIsTheLogFile(t *testing.T) {
	h, _, dir := newHub(t)
	j := run(t, h, sh("p", "a", `readlink /proc/self/fd/0 /proc/self/fd/1 /proc/self/fd/2`))
	log := filepath.Join(dir, j.ID+".log")
	if want := []string{"/dev/null", log, log}; !slices.Equal(j.Lines, want) {
		t.Errorf("child stdio = %q, want %q (no pipe through the hub)", j.Lines, want)
	}
}

func TestOutputCaps(t *testing.T) {
	h, _, _ := newHub(t)
	start := func(s Spec) Snapshot {
		t.Helper()
		j, err := h.Start(context.Background(), s)
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	lines := start(sh("p", "lines", `i=0; while [ $i -lt 6000 ]; do echo line$i; i=$((i+1)); done`))
	long := start(sh("p", "long", `head -c 102400 /dev/zero | tr '\0' a; echo; echo next`))
	ansi := start(sh("p", "ansi", `printf '\033[31mred\033[0m\n'; printf '\033]0;title\007osc\n'; printf 'a\377b\n'`))

	j := waitDone(t, h, lines)
	if len(j.Lines) != KeepLines || j.Lines[0] != "line1000" || j.Lines[len(j.Lines)-1] != "line5999" {
		t.Errorf("lines: %d kept, first %q, last %q; want 5000, line1000..line5999", len(j.Lines), j.Lines[0], j.Lines[len(j.Lines)-1])
	}
	if !j.Dropped {
		t.Error("Dropped = false after the line cap")
	}

	j = waitDone(t, h, long)
	if len(j.Lines) != 2 || len(j.Lines[0]) != MaxLine || strings.Trim(j.Lines[0], "a") != "" || j.Lines[1] != "next" {
		t.Errorf("long: %d lines, first %d bytes; want 2 lines, first %d bytes of a, then next", len(j.Lines), len(j.Lines[0]), MaxLine)
	}

	j = waitDone(t, h, ansi)
	if want := []string{"red", "osc", "a?b"}; !slices.Equal(j.Lines, want) {
		t.Errorf("ansi lines = %q, want %q", j.Lines, want)
	}
}

// TestFloodCapped: a job flooding its log ends OK with the drop marked and
// the kept lines capped, and the file was truncated on the way (it holds
// less than the 40 MiB written). How far past the backstop the file gets
// depends on scheduling, so TestPollBackstop checks that limit exactly.
func TestFloodCapped(t *testing.T) {
	h, _, dir := newHub(t)
	s, err := h.Start(context.Background(), sh("p", "a", `yes "$(printf '%01023d' 0)" | head -c 41943040`))
	if err != nil {
		t.Fatal(err)
	}
	j := waitDone(t, h, s)
	if j.State != StateOK {
		t.Errorf("state = %s", j.State)
	}
	if !j.Dropped {
		t.Error("Dropped = false")
	}
	kept := 0
	for _, l := range j.Lines {
		kept += len(l)
	}
	if kept > KeepBytes || len(j.Lines) > KeepLines {
		t.Errorf("kept %d bytes in %d lines, want ≤ %d bytes and ≤ %d lines", kept, len(j.Lines), KeepBytes, KeepLines)
	}
	b, err := os.ReadFile(filepath.Join(dir, s.ID+".log"))
	if err != nil || !strings.Contains(string(b), droppedLine) {
		t.Errorf("log has no dropped mark (err %v)", err)
	}
	if len(b) >= 41943040 {
		t.Errorf("log is %d bytes: never truncated", len(b))
	}
}

// TestPollBackstop: a log past the backstop that the tail hasn't read is
// truncated by the next poll, to the drop mark alone.
func TestPollBackstop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "j.log")
	lt, err := openLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lt.Close()
	chunk := bytes.Repeat([]byte("x"), 1<<20)
	for written := 0; written <= backstop; written += len(chunk) {
		if _, err := lt.w.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	_, dropped, _, err := lt.poll()
	if err != nil || !dropped {
		t.Fatalf("poll: dropped %v, err %v", dropped, err)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != droppedLine {
		t.Errorf("after the poll the log is %d bytes, want only the drop mark (err %v)", len(b), err)
	}
	if lt.off != 0 {
		t.Errorf("off = %d after the truncation", lt.off)
	}
}

func TestBusy(t *testing.T) {
	h, _, _ := newHub(t)
	ctx := context.Background()
	a := sh("p", "a", "sleep 30")
	a.Label = "up"
	ja, err := h.Start(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if !h.Running() {
		t.Error("Running = false with a running job")
	}
	if _, err := h.Start(ctx, sh("p", "a", "true")); !errors.Is(err, ErrBusy) || !strings.Contains(err.Error(), "up") {
		t.Errorf("same slug: err = %v, want ErrBusy naming up", err)
	}
	if _, err := h.Start(ctx, sh("p", "", "true")); !errors.Is(err, ErrBusy) {
		t.Errorf("project job while a slug runs: err = %v, want ErrBusy", err)
	}
	other := run(t, h, sh("p", "b", "true"))
	if other.State != StateOK {
		t.Errorf("other slug: state %s", other.State)
	}
	if j := run(t, h, sh("q", "", "true")); j.State != StateOK {
		t.Errorf("other project: state %s", j.State)
	}
	if err := h.Cancel(ja.ID); err != nil {
		t.Fatal(err)
	}
	waitDone(t, h, ja)

	jp, err := h.Start(ctx, sh("p", "", "sleep 30"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Start(ctx, sh("p", "c", "true")); !errors.Is(err, ErrBusy) {
		t.Errorf("slug job while the project job runs: err = %v, want ErrBusy", err)
	}
	if err := h.Cancel(jp.ID); err != nil {
		t.Fatal(err)
	}
	waitDone(t, h, jp)
	if h.Running() {
		t.Error("Running = true after all jobs ended")
	}
}

func TestRetention(t *testing.T) {
	h, _, dir := newHub(t)
	var ids []string
	for range 25 {
		ids = append(ids, run(t, h, sh("p", "a", "echo x")).ID)
	}
	js := h.Jobs("p", "")
	if len(js) != KeepJobs {
		t.Fatalf("kept %d jobs, want %d", len(js), KeepJobs)
	}
	if js[0].ID != ids[24] || js[len(js)-1].ID != ids[5] {
		t.Errorf("kept %s..%s, want newest first %s..%s", js[0].ID, js[len(js)-1].ID, ids[24], ids[5])
	}
	logs, _ := filepath.Glob(filepath.Join(dir, "*.log"))
	if len(logs) != KeepJobs {
		t.Errorf("%d logs on disk, want %d", len(logs), KeepJobs)
	}
	for _, id := range ids[:5] {
		if _, err := os.Stat(filepath.Join(dir, id+".log")); !os.IsNotExist(err) {
			t.Errorf("log of dropped job %s: %v", id, err)
		}
	}
	if got := h.Jobs("p", "b"); len(got) != 0 {
		t.Errorf("Jobs(p, b) = %d jobs, want 0", len(got))
	}
	if got := h.Jobs("p", "a"); len(got) != KeepJobs {
		t.Errorf("Jobs(p, a) = %d jobs, want %d", len(got), KeepJobs)
	}
}

func TestCancel(t *testing.T) {
	h, _, _ := newHub(t)
	s, err := h.Start(context.Background(), sh("p", "a", "echo started; sleep 30"))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	start := time.Now()
	if err := h.Cancel(s.ID); err != nil {
		t.Fatal(err)
	}
	j := waitDone(t, h, s)
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("cancel took %v", d)
	}
	if j.State != StateCancelled {
		t.Errorf("state = %s, want cancelled", j.State)
	}
	if err := h.Cancel("nope"); err == nil {
		t.Error("Cancel(unknown) = nil")
	}
}

func TestStartCtxDoesNotEndJob(t *testing.T) {
	h, _, _ := newHub(t)
	ctx, cancel := context.WithCancel(context.Background())
	s, err := h.Start(ctx, sh("p", "a", "sleep 0.3; echo done"))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if j := waitDone(t, h, s); j.State != StateOK || !slices.Equal(j.Lines, []string{"done"}) {
		t.Errorf("state %s lines %q, want ok [done]", j.State, j.Lines)
	}
}

func TestStates(t *testing.T) {
	h, _, _ := newHub(t)
	ok := run(t, h, sh("p", "a", "exit 0"))
	if ok.State != StateOK || ok.Exit != 0 || ok.Ended.IsZero() || ok.Started.IsZero() {
		t.Errorf("exit 0: %+v", ok)
	}
	failed := run(t, h, sh("p", "a", "exit 4"))
	if failed.State != StateFailed || failed.Exit != 4 {
		t.Errorf("exit 4: state %s exit %d, want failed 4", failed.State, failed.Exit)
	}
}

func TestEnv(t *testing.T) {
	h, _, _ := newHub(t)
	j := run(t, h, sh("p", "a", `echo "$NO_COLOR $RS_TEST"`))
	if !slices.Equal(j.Lines, []string{"1 injected"}) {
		t.Errorf("lines = %q, want [1 injected]", j.Lines)
	}
}

func TestSubscribe(t *testing.T) {
	h, _, _ := newHub(t)
	ch, unsub := h.Subscribe()
	s, err := h.Start(context.Background(), sh("p", "a", "echo hi"))
	if err != nil {
		t.Fatal(err)
	}
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Job.ID != s.ID {
				t.Fatalf("event for %s", ev.Job.ID)
			}
			if ev.Job.State == StateOK {
				if !slices.Equal(ev.Job.Lines, []string{"hi"}) {
					t.Errorf("final lines = %q", ev.Job.Lines)
				}
				unsub()
				if _, open := <-ch; open {
					// a pending event may still be delivered; the channel must close after it
					if _, open := <-ch; open {
						t.Error("channel still open after unsubscribe")
					}
				}
				return
			}
		case <-timeout:
			t.Fatal("no final event")
		}
	}
}

func TestSlowSubscriber(t *testing.T) {
	h, _, _ := newHub(t)
	ch, unsub := h.Subscribe()
	defer unsub()
	var last Snapshot
	for range 3 {
		last = run(t, h, sh("p", "a", `i=0; while [ $i -lt 3000 ]; do echo $i; i=$((i+1)); done`))
	}
	// Never read while the jobs ran: they still finished, and the latest
	// state of each job is still delivered.
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Job.ID == last.ID && ev.Job.State == StateOK {
				return
			}
		case <-timeout:
			t.Fatal("final state of the last job never delivered")
		}
	}
}

func TestSnapshotLinesCopied(t *testing.T) {
	h, _, _ := newHub(t)
	j := run(t, h, sh("p", "a", "echo a; echo b"))
	j.Lines[0] = "changed"
	if again, _ := find(h, "p", j.ID); again.Lines[0] != "a" {
		t.Errorf("hub lines changed through a snapshot: %q", again.Lines)
	}
}

// TestStartInputStdin: the payload reaches the child's stdin and appears
// nowhere else a reader of the hub can look; Start still gives /dev/null.
func TestStartInputStdin(t *testing.T) {
	h, r, dir := newHub(t)
	payload := []byte("s3cret\n'$(x)\n")
	snap, err := h.StartInput(context.Background(), sh("p", "", `n=$(wc -c); echo "read $n bytes"`), payload)
	if err != nil {
		t.Fatal(err)
	}
	if v := fmt.Sprintf("%+v", snap); strings.Contains(v, "s3cret") {
		t.Errorf("start snapshot leaks: %s", v)
	}
	done := waitDone(t, h, snap)
	if done.State != StateOK || !slices.Contains(done.Lines, fmt.Sprintf("read %d bytes", len(payload))) {
		t.Fatalf("job %+v", done)
	}
	r.mu.Lock()
	got := r.stdins[0]
	r.mu.Unlock()
	if !bytes.Equal(got, payload) {
		t.Errorf("stdin %q, want %q", got, payload)
	}
	js, err := json.Marshal(done)
	if err != nil {
		t.Fatal(err)
	}
	log, err := os.ReadFile(filepath.Join(dir, done.ID+".log"))
	if err != nil {
		t.Fatal(err)
	}
	for name, view := range map[string]string{"%+v": fmt.Sprintf("%+v", done), "json": string(js), "log": string(log)} {
		if strings.Contains(view, "s3cret") {
			t.Errorf("%s leaks the payload: %s", name, view)
		}
	}

	plain, err := h.Start(context.Background(), sh("p", "", "true"))
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, h, plain)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stdins[1] != nil {
		t.Errorf("Start stdin %q, want none", r.stdins[1])
	}
}
