package lifecycle

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// hangupScript ends its command once the pipe's write end closes: with a
// SIGHUP, or a SIGKILL when the command ignores that.
func TestHangupEndsCommand(t *testing.T) {
	for name, argv := range map[string][]string{
		"hup":         {"sleep", "60"},
		"ignores hup": {"sh", "-c", `trap '' HUP; sleep 60 & wait`},
	} {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		argv = hangup(argv)
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.ExtraFiles = []*os.File{r}
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // as podman runs it: its own session
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		r.Close()
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			t.Fatalf("%s: exited before the hang-up: %v", name, err)
		case <-time.After(300 * time.Millisecond):
		}
		w.Close()
		select {
		case <-done:
		case <-time.After(6 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			t.Fatalf("%s: still running 6 s after the write end closed", name)
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// A command that ends on its own returns its exit code at once.
func TestHangupKeepsExitCode(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	argv := hangup([]string{"sh", "-c", "exit 7"})
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.ExtraFiles = []*os.File{r}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	start := time.Now()
	err = cmd.Run()
	r.Close()
	if x, ok := err.(*exec.ExitError); !ok || x.ExitCode() != 7 {
		t.Fatalf("err %v; want exit 7", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %v; want an immediate return", d)
	}
}
