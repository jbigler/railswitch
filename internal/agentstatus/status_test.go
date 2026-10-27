package agentstatus

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStatusRead(t *testing.T) {
	dir := t.TempDir()
	write := func(name, data string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	valid := write("valid.json", `{"v":1,"state":"working","message":"editing","ts":1792919287}`)
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(valid, link); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo.json")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	big := `{"v":1,"state":"working","message":"` + strings.Repeat("x", 70000) + `","ts":1}`
	for _, c := range []struct {
		name, path, state string
	}{
		{"valid", valid, "working"},
		{"v2", write("v2.json", `{"v":2,"state":"working","message":"","ts":1}`), "off"},
		{"bogus state", write("bogus.json", `{"v":1,"state":"bogus","message":"","ts":1}`), "off"},
		{"oversized", write("big.json", big), "off"},
		{"invalid", write("invalid.json", `{"v":1,`), "off"},
		{"missing", filepath.Join(dir, "nope.json"), "off"},
		{"symlink", link, "off"},
		{"fifo", fifo, "off"},
		{"extra fields", write("extra.json", `{"v":1,"state":"question","message":"m","ts":5,"x":true}`), "question"},
	} {
		done := make(chan Status, 1)
		go func() { done <- Read(c.path) }()
		select {
		case s := <-done:
			if s.State != c.state {
				t.Errorf("%s: state %q; want %q", c.name, s.State, c.state)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: Read blocked", c.name)
		}
	}
	if s := Read(valid); s.V != 1 || s.Message != "editing" || s.TS != 1792919287 {
		t.Errorf("valid = %+v", s)
	}
}

func TestStatusReply(t *testing.T) {
	for _, c := range []struct {
		state, msg string
		want       bool
	}{
		{"done", "Ship it?", true},
		{"done", "ok?)", true},
		{"done", "ok", false},
		{"question", "x?", false},
	} {
		if got := (Status{V: 1, State: c.state, Message: c.msg}).Reply(); got != c.want {
			t.Errorf("%s %q: Reply() = %v; want %v", c.state, c.msg, got, c.want)
		}
	}
}
