package web

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jbigler/railswitch/internal/jobs"
)

var updateActions = flag.Bool("update", false, "rewrite testdata/contract/actions.txt")

// actionsFile lists every argv the dashboard's tests start as a job (the
// actions of the dashboard contract, D197), one JSON array a line. The
// CLI's contract test runs each and fails on a usage error.
const actionsFile = contractDir + "/actions.txt"

// TestMain records the argv of every job the package's tests start, then
// compares the set with actionsFile and checks that every start call ran.
// A partial run (-run) checks nothing.
func TestMain(m *testing.M) {
	var mu sync.Mutex
	argvs, sites := map[string]bool{}, map[string]bool{}
	startHook = func(spec jobs.Spec) {
		mu.Lock()
		defer mu.Unlock()
		pcs := make([]uintptr, 1)
		if runtime.Callers(3, pcs) == 1 { // skip Callers, the hook, start
			fr, _ := runtime.CallersFrames(pcs).Next()
			sites[filepath.Base(fr.File)+":"+strconv.Itoa(fr.Line)] = true
		}
		if len(spec.Argv) > 0 && spec.Argv[0] == testExe {
			b, _ := json.Marshal(spec.Argv[1:])
			argvs[string(b)] = true
		}
	}
	code := m.Run()
	if code == 0 && flag.Lookup("test.run").Value.String() == "" {
		if err := checkActions(sorted(argvs), sites); err != nil {
			fmt.Fprintln(os.Stderr, "dashboard contract:", err)
			code = 1
		}
	}
	os.Exit(code)
}

func checkActions(argvs []string, sites map[string]bool) error {
	got := strings.Join(argvs, "\n") + "\n"
	if *updateActions {
		if err := os.WriteFile(actionsFile, []byte(got), 0o644); err != nil {
			return err
		}
	}
	want, err := os.ReadFile(actionsFile)
	if err != nil {
		return err
	}
	if got != string(want) {
		have := map[string]bool{}
		for _, l := range strings.Split(string(want), "\n") {
			have[l] = true
		}
		var diff []string
		for _, l := range argvs {
			if !have[l] {
				diff = append(diff, "+ "+l)
			}
			delete(have, l)
		}
		for l := range have {
			if l != "" {
				diff = append(diff, "- "+l)
			}
		}
		sort.Strings(diff)
		return fmt.Errorf("the actions the tests start differ from %s:\n%s\nrun go test ./internal/web -update, then go test ./internal/cli", actionsFile, strings.Join(diff, "\n"))
	}
	// Every start call ran under some test, so its argv is in the file.
	fset := token.NewFileSet()
	names, err := filepath.Glob("*.go")
	if err != nil {
		return err
	}
	var missed []string
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "start" {
				return true
			}
			from, to := fset.Position(call.Pos()).Line, fset.Position(call.End()).Line
			for l := from; l <= to; l++ {
				if sites[name+":"+strconv.Itoa(l)] {
					return true
				}
			}
			missed = append(missed, name+":"+strconv.Itoa(from))
			return true
		})
	}
	if len(missed) > 0 {
		return fmt.Errorf("no test starts the action at %s: its argv is unchecked", strings.Join(missed, ", "))
	}
	return nil
}
