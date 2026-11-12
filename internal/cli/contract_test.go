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
	"github.com/jbigler/railswitch/internal/testfixture"
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
	ws, dirs := gitWS(t)
	// A trusted library, acme, with copies of built-in packages.
	addLibrary(t, dirs, "acme")
	testfixture.Library(t, dirs, "acme", acmeID, "options/redis", "packs/rails", "agents/claude")
	// Tasks that run in a new container and in a terminal (D171).
	editProject(t, ws, "schema = 1\n", "schema = 1\nfresh_tasks = [\"test\"]\nterminal_tasks = [\"console\"]\n")
	// A repository project detect can read a stack from.
	master := filepath.Join(ws, "worktrees", "master")
	writeFile(t, filepath.Join(master, "Gemfile"), "source \"https://rubygems.org\"\ngem \"rails\"\ngem \"pg\"\ngem \"redis\"\n")
	writeFile(t, filepath.Join(master, "config", "application.rb"), "")
	gitIn(t, master, "add", "Gemfile", "config")
	gitIn(t, master, "commit", "-q", "-m", "Gemfile")
	// The machine: the project exported as a template, and two folders
	// someone could hand over, a template and a package.
	home := os.Getenv("HOME")
	mustRun(t, "-C", ws, "template", "export", "--name", "filial-stack")
	mustRun(t, "-C", ws, "template", "export", "--name", "given", "--to", filepath.Join(home, "given"))
	mustRun(t, "-C", ws, "pack", "export", "redis@1", "--to", filepath.Join(home, "redis"))
	mustRun(t, "-C", ws, "render")
	f := fakeHost()
	f.Passthrough = []string{"git", "ssh-keygen"} // ssh-keygen verifies the library
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
	status := `{"v":1,"state":"working","message":"busy","ts":1792919287,"usage":{"input":100,"output":1,"cache_read":0,"cache_write":0,"session":"s1","context":5000,"started":1792919000}}`
	if err := os.WriteFile(filepath.Join(ws, ".railswitch", "status", "feat", "claude.json"), []byte(status), 0o600); err != nil {
		t.Fatal(err)
	}
	// A test run, and a TV source for the office.
	testfixture.WriteFile(t, filepath.Join(ws, ".railswitch", "tests", "feat", "last.json"), `{"exit_code":0,"started_at":"2026-01-06T09:00:00Z","ended_at":"2026-01-06T09:01:00Z","commit":"abc"}`+"\n")
	mustRunStdin(t, f, "", "office", "tv", "jellyfin", "set", "--url", "https://media.lan/", "--playlist", "Lounge")

	// What one read alone needs: each returns its undo.
	probe := filepath.Join(ws, "agents", "probe", "Containerfile.probe")
	before := map[string]func() func(){
		// tidy: feat's PR is merged and its app stopped days ago.
		"tidy": func() func() {
			res, err := (sys.Exec{}).Run(t.Context(), sys.Cmd{Name: "git", Args: []string{"-C", filepath.Join(ws, "worktrees", "feat"), "rev-parse", "HEAD"}})
			if err != nil {
				t.Fatal(err)
			}
			f.On([]string{"gh", "pr", "list"}, sys.Result{Stdout: []byte(`[{"number": 7, "title": "Feat", "url": "https://github.com/o/n/pull/7", "headRefName": "feat", "headRefOid": "` + strings.TrimSpace(string(res.Stdout)) + `", "isDraft": false, "state": "MERGED", "reviewDecision": "", "statusCheckRollup": []}]`)}, nil)
			f.On([]string{"systemctl", "--user", "show", "--timestamp=us+utc"}, sys.Result{Stdout: []byte("Id=filial-feat-rails.service\nInactiveEnterTimestamp=Tue 2026-01-06 09:00:00.000000 UTC\n")}, nil)
			return func() { f.On([]string{"gh", "pr", "list"}, sys.Result{Stdout: []byte(ghFixture)}, nil) }
		},
		// lock --review: a local package changed since the lock.
		"lock-review": func() func() {
			text := readFile(t, probe)
			writeFile(t, probe, text+"RUN curl -fsSL https://example.com/install.sh | sh\n")
			return func() { writeFile(t, probe, text) }
		},
	}
	// The values that differ from run to run: the temp dirs, every date,
	// a queued message's time, and the library's new key.
	paths := strings.NewReplacer(
		ws, "/ws",
		os.Getenv("XDG_CONFIG_HOME"), "/xdg/config",
		os.Getenv("XDG_DATA_HOME"), "/xdg/data",
		home, "/home/user",
	)
	real := strings.NewReplacer("/ws", ws, "/home/user", home) // a contract file's arguments, here
	dates := regexp.MustCompile(`20\d\d-\d\d-\d\d(T[0-9:.]+(Z|[+-]\d\d:\d\d))?`)
	added := regexp.MustCompile(`"added":\d+`) // a queued message's time
	fingerprint := regexp.MustCompile(`SHA256:[A-Za-z0-9+/]{43}`)
	key := regexp.MustCompile(`ssh-ed25519 [A-Za-z0-9+/=]+`)
	install := regexp.MustCompile(`"install":\{"hash":"sha256:[0-9a-f]{64}"`) // covers the library's signature
	bytesRe := regexp.MustCompile(`"bytes":\d+`)
	clean := func(out string) string {
		out = install.ReplaceAllString(out, `"install":{"hash":"sha256:`+strings.Repeat("0", 64)+`"`)
		out = added.ReplaceAllString(paths.Replace(out), `"added":0`)
		out = fingerprint.ReplaceAllString(out, "SHA256:"+strings.Repeat("A", 43))
		out = key.ReplaceAllString(out, "ssh-ed25519 AAAA")
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
		name := strings.TrimSuffix(filepath.Base(file), ".golden")
		t.Run(name, func(t *testing.T) {
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
				args[i] = real.Replace(a)
			}
			// gh api graphql answers two questions: a PR's merge state for
			// pr show, the review apps for the rest.
			graphql := reviewApps
			if slices.Contains(args, "show") && slices.Contains(args, "pr") {
				graphql = ghMergeFixture
			}
			f.On([]string{"gh", "api", "graphql"}, sys.Result{Stdout: []byte(graphql)}, nil)
			if b := before[name]; b != nil {
				defer b()()
			}
			code, out, errs := runStdin(t, f, stdin, append([]string{"--json"}, args...)...)
			if code != 0 {
				t.Fatalf("exit %d: %s", code, errs)
			}
			got := clean(out)
			if name == "tidy" { // a checkout's size: git's objects differ by a few bytes
				got = bytesRe.ReplaceAllString(got, `"bytes":0`)
			}
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
	// The actions: every argv the dashboard's tests start as a job
	// (internal/web's TestMain writes the file). A usage error (exit 2)
	// means the dashboard passes arguments the CLI no longer takes. Any
	// other failure is fine: the fake host runs nothing, git included.
	t.Run("actions", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join(contractDir, "actions.txt"))
		if err != nil {
			t.Fatal(err)
		}
		here := strings.NewReplacer("/ws/ftest", ws, "/ws", ws, "/home/u", home)
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var args []string
			if err := json.Unmarshal([]byte(line), &args); err != nil {
				t.Fatalf("%s: %v", line, err)
			}
			for i, a := range args {
				args[i] = here.Replace(a)
				if i > 0 && args[i-1] == "-w" {
					args[i] = "feat" // the fixture's worktree
				}
				if v, ok := actionNames[args[i]]; ok {
					args[i] = v
				}
			}
			code, _, errs := runStdin(t, fakeHost(), "", args...)
			_, state := actionState[line]
			switch {
			case code == 2 && !state:
				t.Errorf("the dashboard runs %s, a usage error: %s", line, errs)
			case code != 2 && state:
				t.Errorf("actionState names %s, which is no usage error", line)
			}
		}
		for line := range actionState {
			if !strings.Contains(string(data), line+"\n") {
				t.Errorf("actionState names %s, which is not in actions.txt", line)
			}
		}
	})
}

// actionNames maps a name the dashboard's tests use to the fixture's.
var actionNames = map[string]string{"web": "rails", "token": "github_token"}

// actionState are the actions that end as a usage error for the state the
// fixture lacks, not for their arguments.
var actionState = map[string]string{
	`["-C","/ws/ftest","-w","b","agent","default","apply","claude"]`: "the agent has no default",
	`["-C","/ws/ftest","-w","b","agent","setup","--again","claude"]`: "the agent has no setup commands",
	`["-C","/ws/ftest","-w","b","restart","web"]`:                    "the service is not running",
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
