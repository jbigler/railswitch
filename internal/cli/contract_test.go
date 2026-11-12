package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
)

var updateContract = flag.Bool("update", false, "rewrite testdata/contract")

const contractDir = "../../testdata/contract"

// TestDashboardContract is the CLI's side of the dashboard contract (D197):
// each file in testdata/contract is a read command the dashboard runs
// (line 1: its arguments after --json, as a JSON array, the workspace as
// /ws; or {"args": [...], "stdin": "..."} for a read that takes stdin) and
// the document the CLI prints for it on the Filial fixture.
// internal/web's TestDashboardContract decodes the same files.
func TestDashboardContract(t *testing.T) {
	ws, _ := gitWS(t)
	mustRun(t, "-C", ws, "render")
	f := fakeHost()
	f.Passthrough = []string{"git"}
	f.On([]string{"podman", "ps"}, sys.Result{Stdout: []byte("[]")}, nil)
	created(f)
	// GitHub: feat has PR 7, in a gh stack.
	f.On([]string{"gh", "pr", "list"}, sys.Result{Stdout: []byte(ghFixture)}, nil)
	f.On([]string{"gh", "pr", "view", "7"}, sys.Result{Stdout: []byte(ghViewFixture)}, nil)
	f.On([]string{"gh", "extension", "list"}, sys.Result{Stdout: []byte("gh stack\tgithub/gh-stack\tv0.3.0\n")}, nil)
	f.On([]string{"gh", "stack", "view", "--json"}, sys.Result{Stdout: []byte(`{"trunk":"master","currentBranch":"feat","branches":[{"name":"feat","isCurrent":true,"pr":{"number":7,"state":"OPEN"}},{"name":"next"}]}`)}, nil)
	// The firewall: one container with a rejected address, a refused name
	// and a LAN exception.
	f.On([]string{filepath.Join(ws, ".railswitch", "firewall", "firewall.sh"), "show"}, sys.Result{Stdout: []byte("C\tfilial-master-rails\tfirewalled\tapp\tlog\nR\tfilial-master-rails\t104.16.0.1\ttcp\t443\tapi.example.com\nN\tfilial-master-rails\tproxy.example.com\t4\nL\tfilial-master-rails\t192.168.1.20\t443\tsearch.home.example.com\n")}, nil)
	reviewApps := `{"data":{"repository":{"p7":{"commits":{"nodes":[{"commit":{"deployments":{"nodes":[{"state":"ACTIVE","latestStatus":{"state":"SUCCESS","environmentUrl":"https://app-pr-7.example.dev"}}]}}}]}}}}}`
	// What the bare fixture lacks: a note, a database snapshot, a queued
	// message, an agent that reported.
	mustRunStdin(t, f, "a note\n", "-C", ws, "notes", "write", "a.md", "--if-hash", "")
	f.On([]string{"podman", "container", "inspect", "--format", "{{.State.Running}}", "filial-feat-db"}, sys.Result{Stdout: []byte("true\n")}, nil)
	f.On([]string{"podman", "exec", "filial-feat-db"}, sys.Result{Stdout: []byte("PGDMP")}, nil)
	f.On([]string{"podman", "exec", "filial-feat-db", "psql"}, sys.Result{Stdout: []byte("filial_dev\n")}, nil)
	mustRunStdin(t, f, "", "-C", ws, "-w", "feat", "db", "snapshot", "s1")
	mustRunStdin(t, f, "", "-C", ws, "-w", "feat", "agent", "queue", "add", "fix", "the", "build")
	status := `{"v":1,"state":"working","message":"busy","ts":1792919287,"usage":{"input":100,"output":1,"cache_read":0,"cache_write":0}}`
	if err := os.WriteFile(filepath.Join(ws, ".railswitch", "status", "feat", "claude.json"), []byte(status), 0o600); err != nil {
		t.Fatal(err)
	}
	// The values that differ from run to run: the temp dirs, every date,
	// and a queued message's time.
	paths := strings.NewReplacer(
		ws, "/ws",
		os.Getenv("XDG_CONFIG_HOME"), "/xdg/config",
		os.Getenv("XDG_DATA_HOME"), "/xdg/data",
		os.Getenv("HOME"), "/home/user",
	)
	dates := regexp.MustCompile(`20\d\d-\d\d-\d\d(T[0-9:.]+(Z|[+-]\d\d:\d\d))?`)
	added := regexp.MustCompile(`"added":\d+`) // a queued message's time
	clean := func(out string) string {
		out = added.ReplaceAllString(paths.Replace(out), `"added":0`)
		return dates.ReplaceAllStringFunc(out, func(d string) string {
			if len(d) == len("2026-01-01") {
				return "2026-01-01"
			}
			return "2026-01-01T00:00:00Z"
		})
	}
	files, err := filepath.Glob(filepath.Join(contractDir, "*.golden"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no contract files: %v", err)
	}
	for _, file := range files {
		t.Run(strings.TrimSuffix(filepath.Base(file), ".golden"), func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			head, want, _ := bytes.Cut(data, []byte("\n"))
			args, stdin, err := contractHead(head)
			if err != nil {
				t.Fatal(err)
			}
			for i, a := range args {
				args[i] = strings.ReplaceAll(a, "/ws", ws)
			}
			// gh api graphql answers two questions: a PR's merge state for
			// pr show, the review apps for the rest.
			graphql := reviewApps
			if slices.Contains(args, "show") && slices.Contains(args, "pr") {
				graphql = ghMergeFixture
			}
			f.On([]string{"gh", "api", "graphql"}, sys.Result{Stdout: []byte(graphql)}, nil)
			code, out, errs := runStdin(t, f, stdin, append([]string{"--json"}, args...)...)
			if code != 0 {
				t.Fatalf("exit %d: %s", code, errs)
			}
			got := clean(out)
			if *updateContract {
				if err := os.WriteFile(file, append(append(head, '\n'), got...), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			if got != string(want) {
				t.Errorf("differs from %s (go test ./internal/cli -run TestDashboardContract -update, then go test ./internal/web):\n--- got\n%s\n--- want\n%s", file, got, want)
			}
		})
	}
}

// contractHead reads a contract file's first line.
func contractHead(head []byte) (args []string, stdin string, err error) {
	if err = json.Unmarshal(head, &args); err == nil {
		return args, "", nil
	}
	var h struct {
		Args  []string `json:"args"`
		Stdin string   `json:"stdin"`
	}
	if err = json.Unmarshal(head, &h); err != nil || len(h.Args) == 0 {
		return nil, "", fmt.Errorf("line 1 is neither a JSON array of arguments nor {\"args\", \"stdin\"}: %v", err)
	}
	return h.Args, h.Stdin, nil
}
