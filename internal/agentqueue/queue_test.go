package agentqueue

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQueue(t *testing.T) {
	ws := t.TempDir()
	q, err := Read(ws, "feat")
	if err != nil || len(q.Items) != 0 || q.Items == nil {
		t.Fatalf("no queue: %+v, %v", q, err)
	}
	now := time.Unix(100, 0)
	for _, text := range []string{"a", "b", "c"} {
		if _, err := Add(ws, "feat", text, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := Update(ws, "feat", func(q *Queue) error { return Remove(q, 2, false) }); err != nil {
		t.Fatal(err)
	}
	if err := Update(ws, "feat", func(q *Queue) error { return Remove(q, 2, false) }); err == nil {
		t.Error("removed a missing item")
	}
	it, err := Add(ws, "feat", "d", now)
	if err != nil || it.ID != 4 { // ids are never reused
		t.Fatalf("add after rm: %+v, %v", it, err)
	}
	q, _ = Read(ws, "feat")
	var texts []string
	for _, it := range q.Items {
		texts = append(texts, it.Text)
	}
	if strings.Join(texts, "") != "acd" {
		t.Errorf("order: %q", texts)
	}
	for _, p := range []string{Dir(ws, "feat"), file(ws, "feat")} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if want := os.FileMode(0o600); fi.IsDir() {
			want = 0o700 | os.ModeDir
			if fi.Mode() != want {
				t.Errorf("%s: %v; want %v", p, fi.Mode(), want)
			}
		} else if fi.Mode() != want {
			t.Errorf("%s: %v; want %v", p, fi.Mode(), want)
		}
	}
	if _, err := Add(ws, "feat", strings.Repeat("x", MaxText+1), now); err == nil {
		t.Error("an item over MaxText was queued")
	}
	for range MaxItems - 3 {
		if _, err := Add(ws, "feat", "x", now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Add(ws, "feat", "x", now); err == nil || !strings.Contains(err.Error(), "holds") {
		t.Errorf("past MaxItems: %v", err)
	}
	// a newer railswitch's file is refused, not rewritten
	if err := os.WriteFile(file(ws, "new"), nil, 0o600); err == nil {
		t.Fatal("no dir yet, want an error")
	}
	if err := os.MkdirAll(Dir(ws, "new"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(Dir(ws, "new"), "queue.json"), []byte(`{"schema":2,"items":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(ws, "new", "x", now); err == nil || !strings.Contains(err.Error(), "newer railswitch") {
		t.Errorf("schema 2: %v", err)
	}
}

// Only one delivery at a time: a second TryDeliver says no at once.
func TestTryDeliver(t *testing.T) {
	ws := t.TempDir()
	unlock, ok, err := TryDeliver(ws, "feat")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, ok, err := TryDeliver(ws, "feat"); err != nil || ok {
		t.Fatalf("second: %v, %v", ok, err)
	}
	unlock()
	if u, ok, err := TryDeliver(ws, "feat"); err != nil || !ok {
		t.Fatalf("after unlock: %v, %v", ok, err)
	} else {
		u()
	}
}
