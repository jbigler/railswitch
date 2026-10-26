package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// complete runs `railswitch __complete args…` and returns its candidates.
func complete(t *testing.T, args ...string) []string {
	t.Helper()
	code, out, errs := run(t, append([]string{"__complete"}, args...)...)
	if code != 0 || errs != "" {
		t.Fatalf("__complete %q: exit %d, stderr %q", args, code, errs)
	}
	return strings.Fields(out)
}

func TestCompleteCommands(t *testing.T) {
	t.Chdir(t.TempDir())
	if got, want := complete(t, "wt", ""), []string{"add", "ls", "rm"}; !slices.Equal(got, want) {
		t.Errorf("wt: %q; want %q", got, want)
	}
	if got, want := complete(t, ""), slices.Sorted(maps.Keys(commands)); !slices.Equal(got, want) {
		t.Errorf("top level: %q; want %q", got, want)
	}
	if got := complete(t, "w"); !slices.Equal(got, []string{"wt"}) {
		t.Errorf("w: %q; want [wt]", got)
	}
	if got, want := complete(t, "wt", "rm", "--f"), []string{"--force"}; !slices.Equal(got, want) {
		t.Errorf("wt rm --f: %q; want %q", got, want)
	}
	if got, want := complete(t, "-"), []string{"--json", "-C", "-w"}; !slices.Equal(got, want) {
		t.Errorf("global flags: %q; want %q", got, want)
	}
	if got, want := complete(t, "-C", "x", "--json", "secret", ""), []string{"add", "ls", "rm", "set"}; !slices.Equal(got, want) {
		t.Errorf("secret after global flags: %q; want %q", got, want)
	}
	if got, want := complete(t, "completion", ""), []string{"bash", "fish", "zsh"}; !slices.Equal(got, want) {
		t.Errorf("completion: %q; want %q", got, want)
	}
}

// TestCompletionFlagsMatch: completionFlags lists exactly the flags each
// command path defines, found by scanning internal/cli/*.go for flag sets
// (e.flags("<path>"), the global flag.NewFlagSet) and their definitions.
func TestCompletionFlagsMatch(t *testing.T) {
	scanned := scanFlags(t)
	for path, flags := range scanned {
		var want []string
		for name, isBool := range flags {
			spelling := "--" + name
			if len(name) == 1 {
				spelling = "-" + name
			}
			if !isBool {
				spelling += "="
			}
			want = append(want, spelling)
		}
		slices.Sort(want)
		got := slices.Sorted(slices.Values(completionFlags[path]))
		if !slices.Equal(got, want) {
			t.Errorf("completionFlags[%q] = %q; the code defines %q (a trailing = marks a flag that takes a value)", path, got, want)
		}
	}
	for path := range completionFlags {
		if _, ok := scanned[path]; !ok {
			t.Errorf("completionFlags[%q]: no such flag set defines a flag", path)
		}
	}
	if len(scanned) < 10 {
		t.Errorf("scan found flags for only %d command paths: %v", len(scanned), scanned)
	}
}

// scanFlags returns, per command path, each flag name the code defines and
// whether it is a bool flag.
func scanFlags(t *testing.T) map[string]map[string]bool {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	defKinds := map[string]bool{ // method → is a bool flag
		"Bool": true, "BoolVar": true, "BoolFunc": true,
		"String": false, "StringVar": false, "Int": false, "IntVar": false,
		"Int64": false, "Int64Var": false, "Uint": false, "UintVar": false,
		"Float64": false, "Float64Var": false, "Duration": false, "DurationVar": false,
		"Var": false, "Func": false, "TextVar": false,
	}
	out := map[string]map[string]bool{}
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			sets := map[string]string{} // flag set variable → command path ("?": not a literal)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if as, ok := n.(*ast.AssignStmt); ok && len(as.Lhs) == 1 && len(as.Rhs) == 1 {
					if id, ok := as.Lhs[0].(*ast.Ident); ok {
						if path, ok := flagSetPath(as.Rhs[0]); ok {
							sets[id.Name] = path
						}
					}
				}
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				recv, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				path, isSet := sets[recv.Name]
				isBool, isDef := defKinds[sel.Sel.Name]
				if !isSet || !isDef {
					return true
				}
				pos := fset.Position(call.Pos())
				if path == "?" {
					t.Errorf("%s: flag defined on a flag set whose command path is not a string literal", pos)
					return true
				}
				name := ""
				for _, a := range call.Args {
					if lit, ok := a.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						name, _ = strconv.Unquote(lit.Value)
						break
					}
				}
				if name == "" {
					t.Errorf("%s: flag name is not a string literal", pos)
					return true
				}
				if out[path] == nil {
					out[path] = map[string]bool{}
				}
				out[path][name] = isBool
				return true
			})
		}
	}
	return out
}

// flagSetPath is the command path of a flag set expression: e.flags("<path>")
// or flag.NewFlagSet("railswitch[ <path>]", …); "?" when the name is not a
// literal.
func flagSetPath(x ast.Expr) (string, bool) {
	call, ok := x.(*ast.CallExpr)
	if !ok || len(call.Args) == 0 {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (sel.Sel.Name != "flags" && sel.Sel.Name != "NewFlagSet") {
		return "", false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "?", true
	}
	s, _ := strconv.Unquote(lit.Value)
	if sel.Sel.Name == "NewFlagSet" {
		s = strings.TrimPrefix(strings.TrimPrefix(s, "railswitch"), " ")
	}
	return s, true
}

func TestCompleteSlugs(t *testing.T) {
	ws, _ := lockedWS(t)
	t.Chdir(ws)
	if got, want := complete(t, "-w", ""), []string{"feat", "master"}; !slices.Equal(got, want) {
		t.Errorf("-w: %q; want %q", got, want)
	}
	if got, want := complete(t, "-w", "f"), []string{"feat"}; !slices.Equal(got, want) {
		t.Errorf("-w f: %q; want %q", got, want)
	}
	if got, want := complete(t, "wt", "rm", "--force", ""), []string{"feat", "master"}; !slices.Equal(got, want) {
		t.Errorf("wt rm: %q; want %q", got, want)
	}
	if got := complete(t, "wt", "rm", "feat", ""); len(got) != 0 {
		t.Errorf("wt rm feat: %q; want nothing (one argument)", got)
	}
	t.Chdir(t.TempDir())
	if got, want := complete(t, "-C", ws, "-w", ""), []string{"feat", "master"}; !slices.Equal(got, want) {
		t.Errorf("-C ws -w: %q; want %q", got, want)
	}
}

func TestCompleteWorkspaceNames(t *testing.T) {
	ws, _ := lockedWS(t)
	t.Chdir(ws)
	svcs := complete(t, "logs", "")
	if want := []string{"browser", "cache", "db", "deps", "nvim", "rails", "storage"}; !slices.Equal(svcs, want) {
		t.Errorf("logs: %q; want %q", svcs, want)
	}
	if got := complete(t, "fw", "allow", ""); !slices.Equal(got, svcs) {
		t.Errorf("fw allow: %q; want %q", got, svcs)
	}
	if got := complete(t, "exec", ""); len(got) != 0 {
		t.Errorf("exec: %q; want nothing", got)
	}
	cfs := complete(t, "containerfile", "eject", "")
	if !slices.Contains(cfs, "base") {
		t.Errorf("containerfile eject: %q; want base among them", cfs)
	}
	if got, want := complete(t, "secret", "rm", ""), []string{"github_token", "twilio_auth_token"}; !slices.Equal(got, want) {
		t.Errorf("secret rm: %q; want %q", got, want)
	}
	if got, want := complete(t, "project", "rm", ""), []string{"filial"}; !slices.Equal(got, want) {
		t.Errorf("project rm: %q; want %q", got, want)
	}
}

func TestCompleteNoStderr(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	t.Chdir(tmp)
	for _, args := range [][]string{
		{"-w", ""}, {"wt", "rm", ""}, {"logs", ""}, {"secret", "set", ""},
		{"containerfile", "show", ""}, {"project", "rm", ""}, {"-C", "/nonexistent", "-w", ""},
	} {
		code, out, errs := run(t, append([]string{"__complete"}, args...)...)
		if code != 0 || out != "" || errs != "" {
			t.Errorf("__complete %q outside a workspace: exit %d, stdout %q, stderr %q; want 0 and nothing", args, code, out, errs)
		}
	}
	if _, err := os.Stat(filepath.Join(tmp, "data")); !os.IsNotExist(err) {
		t.Errorf("completion created the data dir: %v", err)
	}
}

func TestCompletionScripts(t *testing.T) {
	shells := map[string][]string{"bash": {"bash", "-n"}, "zsh": {"zsh", "-n"}, "fish": {"fish", "-n"}}
	for _, sh := range []string{"bash", "zsh", "fish"} {
		t.Run(sh, func(t *testing.T) {
			code, out, errs := run(t, "completion", sh)
			if code != 0 || errs != "" {
				t.Fatalf("completion %s: exit %d, stderr %q", sh, code, errs)
			}
			if !strings.Contains(out, "__complete") {
				t.Fatalf("completion %s does not call __complete:\n%s", sh, out)
			}
			argv := shells[sh]
			if _, err := exec.LookPath(argv[0]); err != nil {
				t.Skipf("%s is not installed", argv[0])
			}
			file := filepath.Join(t.TempDir(), "completion."+sh)
			if err := os.WriteFile(file, []byte(out), 0o644); err != nil {
				t.Fatal(err)
			}
			if b, err := exec.Command(argv[0], append(argv[1:], file)...).CombinedOutput(); err != nil {
				t.Errorf("%s %s: %v\n%s", strings.Join(argv, " "), file, err, b)
			}
		})
	}
	if code, _, errs := run(t, "completion", "tcsh"); code != 2 || errs == "" {
		t.Errorf("completion tcsh: exit %d, stderr %q; want a usage error", code, errs)
	}
}
