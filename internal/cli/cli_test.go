package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/machine"
)

// TestMain points procRoot at a dir whose net/tcp lists no socket: no test
// sees the host's listening ports unless it sets some with listening; and
// dashboardAddr at a server answering as the dashboard.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "railswitch-proc")
	if err != nil {
		panic(err)
	}
	if err := writeProcTCP(dir); err != nil {
		panic(err)
	}
	procRoot = dir
	// The dashboard's /healthz, as railswitch's answers it: no test asks
	// the host's port 7700.
	dash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != machine.DashboardHost {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		io.WriteString(w, "ok test\n")
	}))
	dashboardAddr = strings.TrimPrefix(dash.URL, "http://")
	code := m.Run()
	dash.Close()
	os.RemoveAll(dir)
	os.Exit(code)
}

// writeProcTCP writes <dir>/net/tcp with ports as listening TCP sockets on
// 127.0.0.1.
func writeProcTCP(dir string, ports ...int) error {
	var b strings.Builder
	b.WriteString("  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n")
	for i, p := range ports {
		fmt.Fprintf(&b, "%4d: 0100007F:%04X 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 %d 1\n", i, p, 40000+i)
	}
	if err := os.MkdirAll(filepath.Join(dir, "net"), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "net", "tcp"), []byte(b.String()), 0o644)
}

// listening makes ports listening TCP sockets (127.0.0.1) for this test.
func listening(t *testing.T, ports ...int) {
	t.Helper()
	dir := t.TempDir()
	if err := writeProcTCP(dir, ports...); err != nil {
		t.Fatal(err)
	}
	old := procRoot
	procRoot = dir
	t.Cleanup(func() { procRoot = old })
}

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Main(context.Background(), args, strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

func TestVersion(t *testing.T) {
	code, out, _ := run(t, "version")
	if code != 0 || out != "railswitch dev\n" {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestVersionJSON(t *testing.T) {
	code, out, _ := run(t, "--json", "version")
	if code != 0 || out != "{\"version\":\"dev\"}\n" {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, errs := run(t, "nope")
	if code != 2 || !strings.Contains(errs, `unknown command "nope"`) {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
}
