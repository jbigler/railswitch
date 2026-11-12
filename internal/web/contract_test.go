package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const contractDir = "../../testdata/contract"

// contractPages are the pages the contract test requests: together they
// run every read the dashboard makes, but those in contractSkip.
var contractPages = []string{
	"/p/filial", "/p/filial/feat", "/p/filial/feat/tasks", "/p/filial/feat/agent-default",
	"/p/filial/feat/snapshots", "/p/filial/feat/pr", "/p/filial/feat/queue", "/p/filial/feat/remove",
	"/p/filial/notes?n=a.md", "/p/filial/settings", "/p/filial/settings/blocked", "/p/filial/tidy",
	"/p/filial/images", "/p/filial/images/edit/base", "/p/filial/library?ref=rails@1",
	"/p/filial/library/rails@1/pack.toml", "/p/filial/review", "/p/filial/virtual-office",
	"/p/filial/virtual-office/cyberpunk/", "/office/set.json", "/office/tv.json",
	"/p/filial/library?template=filial-stack@1", "/p/filial/library/import?dir=/home/user/redis&as=myredis",
	"/machine", "/machine/templates/import?dir=/home/user/given", "/new", "/colors",
}

// contractPosts are the POST requests behind reads, by contract file:
// those of the new-project form, and those that take stdin, each built
// from the file's arguments and stdin, so the hash and the text are the
// file's and the rest is the dashboard's.
var contractPosts = map[string]func(args []string, stdin string) *http.Request{
	"project-detect": func([]string, string) *http.Request {
		return req("POST", "/new/detect", form("repo", "file:///ws/worktrees/master"))
	},
	"new-dry-run": func([]string, string) *http.Request {
		return req("POST", "/new/review", form("repo", "file:///ws/worktrees/master", "prefix", "shop", "dir", "/home/user/shop", "template", "filial-stack@1"))
	},
	"settings-preview": func(_ []string, stdin string) *http.Request {
		return jsonPost("/p/filial/settings/preview", stdin)
	},
	"containerfile-check": func(args []string, stdin string) *http.Request {
		return req("POST", "/p/filial/images/check/pg-client", form("hash", args[6], "text", stdin))
	},
	"pack-file-check": func(args []string, stdin string) *http.Request {
		return req("POST", "/p/filial/library/check/local:probe@1/agent.toml", form("hash", args[8], "text", stdin))
	},
}

// contractSkip are the readJSON calls the contract does not cover, as
// "<file>:<function>", each with its reason.
var contractSkip = map[string]string{
	"libraries.go:libraryTrust":        "a POST; library ls is read by the machine page",
	"library.go:libraryNew":            "a POST; pack ls is read by the library page",
	"review.go:reviewAccept":           "a POST; lock --review is read by the review page",
	"office.go:cyberPage":              "reads office set ls only for an installed set; officeDefault reads it",
	"newproject.go:newCreate":          "a POST; new --dry-run is read by the review step",
	"packageimport.go:packImport":      "a POST; pack import is read by the import page",
	"templateimport.go:templateImport": "a POST; template import is read by the import page",
}

// contractAbsent are the JSON keys a dashboard struct declares that the
// Filial fixture's documents never hold, each with its reason: an entry
// "<contract file> <path>" covers every key that starts with it. A key
// here is not checked.
var contractAbsent = map[string]string{
	"agent-ls [].default.":                      "no agent has a default",
	"agent-usage .rows[].removed":               "omitempty: no removed worktree",
	"containerfile-check .used":                 "containerfile write prints no used key; pack file write does",
	"pack-file-check .steps[].":                 "the edit changes no image",
	"settings-preview .install.":                "the edit installs no library package",
	"new-dry-run .install.":                     "the template installs no library package",
	"pack-import .bundles_compared_with":        "omitempty: the folder is one package, not a bundle",
	"pack-import .ejected":                      "omitempty: a package import ejects nothing",
	"pack-import .packages[].files[].flags[].":  "no file of the package is flagged",
	"pack-import .replaces.":                    "the package replaces none",
	"template-import .compared_with":            "omitempty: a package import's key",
	"template-import .ejected":                  "omitempty: the template ejects nothing",
	"template-import .replaces.":                "the template replaces none",
	"template-show .ask[].answer":               "omitempty: show has no answers",
	"library-ls .libraries[].":                  "no library",
	"lock-review .packages[].":                  "the lock is current: nothing to review",
	"office-set-ls .sets[].reason":              "omitempty: every set is valid",
	"office-tv .jellyfin.":                      "no TV source",
	"overview .signer_warnings[].":              "no library, so no signer warning",
	"overview .template.":                       "the project came from no template",
	"overview .worktrees[].agent.usage.context": "omitempty: the status file has none",
	"overview .worktrees[].agent.usage.session": "omitempty: the status file has none",
	"overview .worktrees[].agent.usage.started": "omitempty: the status file has none",
	"overview .worktrees[].tests.last.":         "no test task has run",
	"pack-ls .":                                 "omitempty library: only built-ins",
	"pack-ls-project .":                         "omitempty library: only built-ins",
	"pack-show .pin.":                           "rails@1 pins nothing",
	"pack-show .service":                        "omitempty: a pack has none",
	"pack-show .slot":                           "omitempty: a pack has none",
	"pr-show .threads[].":                       "no review thread (the type is the CLI's own prs.View)",
	"project-show .tasks[].":                    "omitempty fresh and terminal: no task sets them",
	"signer-ls .libraries[].":                   "no library",
	"task-ls [].":                               "omitempty fresh and terminal: no task sets them",
	"tidy .items[].":                            "nothing to tidy",
}

// TestDashboardContract is the dashboard's side of the dashboard contract
// (D197). testdata/contract holds, per read command, the
// arguments the dashboard passes and the document the CLI prints
// (internal/cli's TestDashboardContract keeps them true). Here the pages
// run on those documents, and the test fails when the dashboard runs a
// read with no contract file, declares a JSON key its document lacks, has
// a readJSON call no page reached, or a contract file nothing read.
func TestDashboardContract(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(contractDir, "*.golden"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no contract files: %v", err)
	}
	f := newFixture(t)
	names := map[string]string{}  // fakeCLI key -> contract file's name
	stdins := map[string]string{} // fakeCLI key -> the stdin its contract file has
	var posts []*http.Request
	f.cli.out = map[string]string{}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		head, doc, _ := bytes.Cut(data, []byte("\n"))
		args, stdin, err := contractHead(head)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		key := "--json " + strings.Join(args, " ")
		name := strings.TrimSuffix(filepath.Base(file), ".golden")
		names[key] = name
		f.cli.out[key] = string(doc)
		if post := contractPosts[name]; post != nil {
			posts = append(posts, post(args, stdin))
			if stdin != "" {
				stdins[key] = stdin
			}
		} else if stdin != "" {
			t.Errorf("%s has stdin, but contractPosts has no request for it", file)
		}
	}

	var mu sync.Mutex
	declared, seen := map[string]bool{}, map[string]bool{}
	read := map[string]bool{}  // contract files a readJSON decoded
	sites := map[string]bool{} // "<file>:<line>" of the readJSON calls that ran
	decodeHook = func(args []string, raw []byte, v any) {
		mu.Lock()
		defer mu.Unlock()
		pcs := make([]uintptr, 8)
		frames := runtime.CallersFrames(pcs[:runtime.Callers(3, pcs)]) // skip Callers, the hook, readJSON
		if fr, _ := frames.Next(); fr.File != "" {
			sites[filepath.Base(fr.File)+":"+strconv.Itoa(fr.Line)] = true
		}
		name := names["--json "+strings.Join(args, " ")]
		read[name] = true
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Errorf("%s: %v", name, err)
			return
		}
		typ := reflect.TypeOf(v).Elem()
		declaredKeys(typ, name+" ", declared, nil)
		seenKeys(typ, doc, name+" ", seen)
	}
	t.Cleanup(func() { decodeHook = nil })

	// The PR poll reads only for a project a page asked to refresh.
	if w := f.do(req("POST", "/p/filial/prs/refresh", form())); w.Code >= 400 {
		t.Fatalf("prs/refresh: %d %s", w.Code, w.Body)
	}
	f.s.pollOnce(t.Context())
	for _, p := range contractPages {
		if w := f.do(req("GET", p, nil)); w.Code >= 400 {
			t.Errorf("GET %s: %d %s", p, w.Code, w.Body)
		}
	}

	for _, r := range posts {
		if w := f.do(r); w.Code >= 400 {
			t.Errorf("POST %s: %d %s", r.URL.Path, w.Code, w.Body)
		}
	}

	for i, c := range f.cli.Calls() {
		if want, ok := stdins[strings.Join(c, " ")]; ok && string(f.cli.stdins[i]) != want {
			t.Errorf("the dashboard ran %q with stdin %q; its contract file has %q", strings.Join(c, " "), f.cli.stdins[i], want)
		}
	}
	for _, c := range f.cli.Calls() {
		if key := strings.Join(c, " "); c[0] == "--json" && names[key] == "" {
			t.Errorf("the dashboard read %q, which has no contract file: add one in %s with line 1 %s, then run go test ./internal/cli -run TestDashboardContract -update", key, contractDir, jsonArgs(c[1:]))
		}
	}
	for _, name := range names {
		if !read[name] {
			t.Errorf("no page read contract file %s: add its page to contractPages, or remove the file", name)
		}
	}
	used := map[string]bool{}
	for _, k := range sorted(declared) {
		if seen[k] {
			continue
		}
		absent := false
		for p := range contractAbsent {
			if strings.HasPrefix(k, p) {
				absent, used[p] = true, true
			}
		}
		if !absent {
			t.Errorf("the dashboard reads key %q, which the CLI's document lacks (renamed? else name it in contractAbsent)", k)
		}
	}
	for p := range contractAbsent {
		if !used[p] {
			t.Errorf("contractAbsent names %q, but the document has every key under it", p)
		}
	}
	calls := readJSONCalls(t)
	for _, c := range calls {
		ran := false
		for l := c.from; l <= c.to; l++ {
			ran = ran || sites[c.file+":"+strconv.Itoa(l)]
		}
		_, skip := contractSkip[c.file+":"+c.fn]
		switch {
		case !ran && !skip:
			t.Errorf("%s:%d: no page in contractPages reaches this readJSON in %s (add its page, or name %q in contractSkip)", c.file, c.from, c.fn, c.file+":"+c.fn)
		case ran && skip:
			t.Errorf("contractSkip names %s:%s, but a page reached it", c.file, c.fn)
		}
	}
	for k := range contractSkip {
		if !slices.ContainsFunc(calls, func(c readCall) bool { return c.file+":"+c.fn == k }) {
			t.Errorf("contractSkip names %q, which has no readJSON call", k)
		}
	}
}

// declaredKeys adds the path of every JSON key t declares, as json.Unmarshal
// reads it; seenKeys those of them doc holds. A slice adds "[]" to the
// path, a map "{}".
func declaredKeys(t reflect.Type, path string, out map[string]bool, stack []reflect.Type) {
	if slices.Contains(stack, t) || reflect.PointerTo(t).Implements(reflect.TypeFor[json.Unmarshaler]()) {
		return
	}
	stack = append(stack, t)
	switch t.Kind() {
	case reflect.Pointer:
		declaredKeys(t.Elem(), path, out, stack)
	case reflect.Slice, reflect.Array:
		declaredKeys(t.Elem(), path+"[]", out, stack)
	case reflect.Map:
		declaredKeys(t.Elem(), path+"{}", out, stack)
	case reflect.Struct:
		for _, fl := range jsonFields(t) {
			if fl.name != "" {
				out[path+"."+fl.name] = true
				declaredKeys(fl.typ, path+"."+fl.name, out, stack)
			} else {
				declaredKeys(fl.typ, path, out, stack)
			}
		}
	}
}

func seenKeys(t reflect.Type, doc any, path string, out map[string]bool) {
	if reflect.PointerTo(t).Implements(reflect.TypeFor[json.Unmarshaler]()) {
		return
	}
	switch t.Kind() {
	case reflect.Pointer:
		seenKeys(t.Elem(), doc, path, out)
	case reflect.Slice, reflect.Array:
		list, _ := doc.([]any)
		for _, e := range list {
			seenKeys(t.Elem(), e, path+"[]", out)
		}
	case reflect.Map:
		m, _ := doc.(map[string]any)
		for _, e := range m {
			seenKeys(t.Elem(), e, path+"{}", out)
		}
	case reflect.Struct:
		m, ok := doc.(map[string]any)
		if !ok {
			return
		}
		for _, fl := range jsonFields(t) {
			if fl.name == "" {
				seenKeys(fl.typ, doc, path, out)
			} else if v, ok := m[fl.name]; ok {
				out[path+"."+fl.name] = true
				seenKeys(fl.typ, v, path+"."+fl.name, out)
			}
		}
	}
}

type jsonField struct {
	name string // "": an embedded struct, its keys its parent's
	typ  reflect.Type
}

func jsonFields(t reflect.Type) []jsonField {
	var out []jsonField
	for i := range t.NumField() {
		fl := t.Field(i)
		name, _, _ := strings.Cut(fl.Tag.Get("json"), ",")
		switch {
		case name == "-" || !fl.IsExported() && !fl.Anonymous:
		case fl.Anonymous && name == "":
			out = append(out, jsonField{"", fl.Type})
		case name == "":
			out = append(out, jsonField{fl.Name, fl.Type})
		default:
			out = append(out, jsonField{name, fl.Type})
		}
	}
	return out
}

type readCall struct {
	file, fn string
	from, to int // its lines
}

// readJSONCalls are the package's readJSON calls outside its tests.
func readJSONCalls(t *testing.T) []readCall {
	t.Helper()
	fset := token.NewFileSet()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var out []readCall
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if ix, ok := call.Fun.(*ast.IndexExpr); ok {
					if id, ok := ix.X.(*ast.Ident); ok && id.Name == "readJSON" {
						out = append(out, readCall{name, fd.Name.Name, fset.Position(call.Pos()).Line, fset.Position(call.End()).Line})
					}
				}
				return true
			})
		}
	}
	if len(out) == 0 {
		t.Fatal("found no readJSON call")
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].file+strconv.Itoa(out[i].from) < out[j].file+strconv.Itoa(out[j].from)
	})
	return out
}

func sorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func jsonArgs(args []string) string {
	b, _ := json.Marshal(args)
	return string(b)
}

// contractHead reads a contract file's first line: a JSON array of
// arguments, or {"args": [...], "stdin": "..."}.
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
