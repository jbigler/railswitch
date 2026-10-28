package web

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

const libraryCatalog = `{"packs":[{"ref":"web@1","name":"web"},{"ref":"local:clj@1","name":"clj"}],
 "options":[{"ref":"kv@1","name":"kv","slot":"cache","works_with":["*"]},{"ref":"pg@1","name":"pg","slot":"database","works_with":["*"]},{"ref":"local:kv2@1","name":"kv2","slot":"cache","works_with":["*"]}],
 "agents":[{"ref":"bot@1","name":"bot","default":true}]}`

const kv2Show = `{"ref":"local:kv2@1","kind":"option","source":"local","path":"/ws/ftest/options/kv2","slot":"cache","service":"kv",
 "runtimes":[],"ports":[{"name":"kv","base":16379,"container":6379}],"pin":{"name":"kv","default":"7"},"gives":["KV_URL"],
 "readme":"Point the app at KV_URL.","files":[
  {"path":"README.md","size":24,"executable":false},
  {"path":"entrypoints/kv.sh","size":120,"executable":true},
  {"path":"option.toml","size":300,"executable":false}]}`

const kvShow = `{"ref":"kv@1","kind":"option","source":"built in","path":"","slot":"cache","service":"kv","runtimes":[],"ports":[],"pin":null,"gives":["KV_URL"],"readme":"","files":[]}`

const webShow = `{"ref":"web@1","kind":"pack","source":"built in","path":"","app":{"service":"web","server":"bin/dev","port":3000,"health":"curl -fsS http://localhost:3000/up"},
 "runtimes":[{"name":"ruby","from":[".ruby-version"],"fallback":""}],"ports":[{"name":"app","base":13000,"container":3000}],"pin":null,"gives":[],"readme":"","files":[]}`

var libFileKey = "--json -C /ws/ftest pack file show local:kv2@1 entrypoints/kv.sh"

var libCheckKey = "--json -C /ws/ftest pack file write local:kv2@1 entrypoints/kv.sh --if-hash " + testHash + " --dry-run"

func libraryFixture(t *testing.T) *fixture {
	f := settingsFixture(t)
	f.cli.set("--json -C /ws/ftest pack ls", libraryCatalog)
	// cache is a slot, so local:kv2@1 is not used; the lock also names the
	// pack and the agent.
	f.cli.set("--json -C /ws/ftest project show", strings.Replace(projectShowJSON, `"file":`, `"used":["web@1","kv@1","stats@1","bot@1"],"file":`, 1))
	f.cli.set("--json -C /ws/ftest pack show local:kv2@1", kv2Show)
	f.cli.set("--json -C /ws/ftest pack show kv@1", kvShow)
	f.cli.set("--json -C /ws/ftest pack show web@1", webShow)
	f.cli.set(libFileKey, `{"path":"entrypoints/kv.sh","text":"#!/bin/sh\nexec kv-server\n","hash":"`+testHash+`"}`)
	f.cli.set("--json -C /ws/ftest pack file show kv@1 option.toml", `{"path":"option.toml","text":"slot = \"cache\"\n","hash":"`+testHash+`"}`)
	f.cli.set(libCheckKey, `{"hash":"`+newHash+`","used":true,"steps":[{"worktree":"main","name":"base","old_tag":"a","new_tag":"b"}],"restart":["main"],"errors":[]}`)
	return f
}

// section is body from the element holding marker to its list's end.
func section(body, marker string) string {
	i := strings.Index(body, marker)
	if i < 0 {
		return ""
	}
	s := body[i:]
	if j := strings.Index(s, "</ul>"); j >= 0 {
		s = s[:j]
	}
	return s
}

func TestLibraryPage(t *testing.T) {
	f := libraryFixture(t)
	w := f.do(req("GET", "/p/ftest/library", nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	body := w.Body.String()
	contains(t, "library", body,
		`data-view="library"`, "<h2>Packs</h2>", "<h2>Options</h2>", "<h2>Agents</h2>",
		`href="/p/ftest/library?ref=local%3Akv2%401"`, `href="/p/ftest/library?ref=bot%401"`,
		`action="/p/ftest/library/new"`, `<option value="web@1">web@1</option>`, `<option value="kv@1">kv@1</option>`,
		`<option value="database">database</option>`,
	)
	for ref, used := range map[string]bool{"web@1": true, "kv@1": true, "bot@1": true, "local:kv2@1": false, "pg@1": false, "local:clj@1": false} {
		row := section(body, `data-ref="`+ref+`"`)
		row = row[:strings.Index(row, "</li>")]
		if got := strings.Contains(row, "used by this project"); got != used {
			t.Errorf("%s: used mark %t, want %t: %s", ref, got, used, row)
		}
	}
	if strings.Contains(body, "Files") {
		t.Error("nothing picked, yet a detail shows")
	}
	noCSPViolation(t, "library", body)

	body = f.do(req("GET", "/p/ftest/library?ref=local:kv2@1", nil)).Body.String()
	contains(t, "local detail", body,
		`<li class="cur" data-ref="local:kv2@1">`, "/ws/ftest/options/kv2", "KV_URL", "Point the app at KV_URL.", "16379",
		`href="/p/ftest/library/local:kv2@1/entrypoints/kv.sh"`, `href="/p/ftest/library/local:kv2@1/option.toml"`,
		`data-add-file`, `data-base="/p/ftest/library/local:kv2@1/"`,
	)
	body = f.do(req("GET", "/p/ftest/library?ref=web@1", nil)).Body.String()
	contains(t, "built-in detail", body, "bin/dev", "curl -fsS http://localhost:3000/up", ".ruby-version",
		`href="/p/ftest/library/web@1/pack.toml"`)
	if strings.Contains(body, "data-add-file") {
		t.Error("a built-in offers Add file")
	}

	// pack show failing: the list, and the error in the detail.
	f.cli.fail("--json -C /ws/ftest pack show local:clj@1", fmt.Errorf("packs/clj/pack.toml: line 2: bad"))
	w = f.do(req("GET", "/p/ftest/library?ref=local:clj@1", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "line 2: bad") || !strings.Contains(w.Body.String(), `href="/p/ftest/library/local:clj@1/pack.toml"`) {
		t.Errorf("broken package: %d %s", w.Code, w.Body)
	}
	if w := f.do(req("GET", "/p/ftest/library?ref=nope@1", nil)); w.Code != 404 {
		t.Errorf("unknown ref: %d", w.Code)
	}

	// The editor of a used local file: Check required, the restart list.
	f.cli.set("--json -C /ws/ftest project show", strings.Replace(projectShowJSON, `"file":`, `"used":["local:kv2@1"],"file":`, 1))
	body = f.do(req("GET", "/p/ftest/library/local:kv2@1/entrypoints/kv.sh", nil)).Body.String()
	contains(t, "editor", body, `data-view="editor"`, "#!/bin/sh\nexec kv-server\n</textarea>",
		`data-check="/p/ftest/library/check/local:kv2@1/entrypoints/kv.sh"`, `data-check-required`,
		`action="/p/ftest/library/save/local:kv2@1/entrypoints/kv.sh"`, `action="/p/ftest/library/rm/local:kv2@1/entrypoints/kv.sh"`,
		`name="ack" value="1"`, `href="/p/ftest/library?ref=local%3Akv2%401"`)
	if strings.Contains(body, "Changes from the default") {
		t.Error("a package file shows the default panel")
	}
	noCSPViolation(t, "library editor", body)

	// An unused local package is still checked: its dry run says whether it loads.
	f.cli.set("--json -C /ws/ftest project show", projectShowJSON)
	body = f.do(req("GET", "/p/ftest/library/local:kv2@1/entrypoints/kv.sh", nil)).Body.String()
	contains(t, "unused editor", body, `data-check="/p/ftest/library/check/local:kv2@1/entrypoints/kv.sh"`, `data-check-required`)

	w = f.do(req("POST", "/p/ftest/library/check/local:kv2@1/entrypoints/kv.sh", form("hash", testHash, "text", "x\r\n")))
	if in, _ := f.cli.stdinOf(libCheckKey); w.Code != 200 || string(in) != "x\n" {
		t.Errorf("check: %d %s, stdin %q", w.Code, w.Body, in)
	}
	contains(t, "check", w.Body.String(), "data-ok", "Rebuild and restart:")
}

func TestLibraryBuiltinReadOnly(t *testing.T) {
	f := libraryFixture(t)
	body := f.do(req("GET", "/p/ftest/library/kv@1/option.toml", nil)).Body.String()
	contains(t, "built-in file", body, "slot = &#34;cache&#34;\n</textarea>", " readonly>", "built in · read-only")
	for _, no := range []string{"data-save", `name="ack"`, "data-check", "Delete file", "Load from file"} {
		if strings.Contains(body, no) {
			t.Errorf("a built-in's file offers %q", no)
		}
	}
	for _, path := range []string{"/p/ftest/library/save/kv@1/option.toml", "/p/ftest/library/check/kv@1/option.toml", "/p/ftest/library/rm/kv@1/option.toml"} {
		if w := f.do(req("POST", path, form("hash", testHash, "text", "x", "ack", "1"))); w.Code != 422 {
			t.Errorf("%s: %d", path, w.Code)
		}
	}
	time.Sleep(10 * time.Millisecond)
	if got := f.run.Argvs(); len(got) != 0 {
		t.Errorf("jobs for a built-in: %q", got)
	}
}

func TestLibrarySave(t *testing.T) {
	f := libraryFixture(t)
	text := "#!/bin/sh\necho '$(id)'\n"
	for i, c := range []struct {
		path string
		form kv
		argv []string
	}{
		{"entrypoints/kv.sh", kv{"hash", testHash, "text", text, "ack", "1", "restart", "1"},
			[]string{testExe, "-C", "/ws/ftest", "pack", "file", "write", "local:kv2@1", "entrypoints/kv.sh", "--if-hash", testHash, "--restart"}},
		{"entrypoints/new.sh", kv{"hash", "", "text", text, "ack", "1"},
			[]string{testExe, "-C", "/ws/ftest", "pack", "file", "write", "local:kv2@1", "entrypoints/new.sh", "--if-hash", ""}},
		{"README.md", kv{"hash", testHash, "text", text},
			[]string{testExe, "-C", "/ws/ftest", "pack", "file", "write", "local:kv2@1", "README.md", "--if-hash", testHash}},
	} {
		w := f.do(req("POST", "/p/ftest/library/save/local:kv2@1/"+c.path, form(c.form...)))
		if w.Code != 202 {
			t.Fatalf("%s: %d %s", c.path, w.Code, w.Body)
		}
		if got := f.run.wait(t, i+1)[i]; !slices.Equal(got, c.argv) {
			t.Errorf("argv %q, want %q", got, c.argv)
		}
		if in := f.run.Stdins()[i]; string(in) != text {
			t.Errorf("stdin %q, want %q", in, text)
		}
		if js := f.s.c.Jobs.Jobs("ftest", ""); js[0].Spec.Slug != "" {
			t.Errorf("not a project-level job: %+v", js[0])
		}
		f.ended(t)
	}

	// The acknowledgement, for every file but README.md.
	for _, path := range []string{"entrypoints/kv.sh", "option.toml", "Containerfile.kv"} {
		if w := f.do(req("POST", "/p/ftest/library/save/local:kv2@1/"+path, form("hash", testHash, "text", text))); w.Code != 400 {
			t.Errorf("%s without ack: %d", path, w.Code)
		}
	}

	f.do(req("POST", "/p/ftest/library/rm/local:kv2@1/entrypoints/kv.sh", form("hash", testHash)))
	if got, want := f.run.wait(t, 4)[3], []string{testExe, "-C", "/ws/ftest", "pack", "file", "rm", "local:kv2@1", "entrypoints/kv.sh", "--if-hash", testHash}; !slices.Equal(got, want) {
		t.Errorf("rm argv %q, want %q", got, want)
	}
	f.ended(t)
	for _, c := range []struct{ name, hash string }{{"no hash", ""}, {"flag", "--force"}} {
		if w := f.do(req("POST", "/p/ftest/library/rm/local:kv2@1/entrypoints/kv.sh", form("hash", c.hash))); w.Code != 422 {
			t.Errorf("rm %s: %d", c.name, w.Code)
		}
	}

	// Add file: an empty editor; Check with no hash.
	body := f.do(req("GET", "/p/ftest/library/local:kv2@1/entrypoints/new.sh?new=1", nil)).Body.String()
	contains(t, "new file", body, `name="hash" value=""`, "new file", `aria-label="entrypoints/new.sh">`+"\n</textarea>")
	if strings.Contains(body, "Delete file") {
		t.Error("a new file offers Delete")
	}
	time.Sleep(10 * time.Millisecond)
	if n := len(f.run.Argvs()); n != 4 {
		t.Errorf("%d jobs, want 4", n)
	}
}

func TestLibraryNew(t *testing.T) {
	f := libraryFixture(t)
	for i, c := range []struct {
		form kv
		argv []string
	}{
		{kv{"kind", "pack", "name", "shop", "from", "web@1"}, []string{"pack", "eject", "web@1", "--as", "shop"}},
		{kv{"kind", "pack", "name", "shop", "from", ""}, []string{"pack", "new", "shop"}},
		{kv{"kind", "option", "name", "kv3", "from", "kv@1", "slot", "database"}, []string{"pack", "eject", "kv@1", "--as", "kv3"}},
		{kv{"kind", "option", "name", "kv3", "from", "", "slot", "cache"}, []string{"pack", "new", "--option", "cache", "kv3"}},
	} {
		w := f.do(req("POST", "/p/ftest/library/new", form(c.form...)))
		if w.Code != 202 {
			t.Fatalf("%v: %d %s", c.form, w.Code, w.Body)
		}
		want := append([]string{testExe, "-C", "/ws/ftest"}, c.argv...)
		if got := f.run.wait(t, i+1)[i]; !slices.Equal(got, want) {
			t.Errorf("argv %q, want %q", got, want)
		}
		f.ended(t)
	}
	for _, c := range []struct {
		name string
		form kv
	}{
		{"bad kind", kv{"kind", "agent", "name", "x", "from", ""}},
		{"bad name", kv{"kind", "pack", "name", "Shop", "from", ""}},
		{"flag name", kv{"kind", "pack", "name", "--as", "from", ""}},
		{"option as pack source", kv{"kind", "pack", "name", "x", "from", "kv@1"}},
		{"local source", kv{"kind", "option", "name", "x", "from", "local:kv2@1"}},
		{"unknown slot", kv{"kind", "option", "name", "x", "from", "", "slot", "nope"}},
		{"no slot", kv{"kind", "option", "name", "x", "from", ""}},
	} {
		if w := f.do(req("POST", "/p/ftest/library/new", form(c.form...))); w.Code != 422 {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body)
		}
	}
	time.Sleep(10 * time.Millisecond)
	if n := len(f.run.Argvs()); n != 4 {
		t.Errorf("%d jobs, want 4", n)
	}
}

func TestLibraryPathRefused(t *testing.T) {
	f := libraryFixture(t)
	for _, c := range []struct{ ref, path string }{
		{"local:kv2@1", "..%2Fx"},
		{"local:kv2@1", "entrypoints/..%2F..%2Fproject.toml"},
		{"local:kv2@1", "foo/bar"},
		{"local:kv2@1", ".hidden"},
		{"local:kv2@1", "entrypoints/.x"},
		{"local:kv2@1", "entrypoints/a/b"},
		{"local:kv2@1", "-rf"},
		{"local:kv2@1", "a%20b"},
		{"-x@1", "option.toml"},
		{"local:Kv@1", "option.toml"},
		{"kv", "option.toml"},
	} {
		for _, r := range []*http.Request{
			req("GET", "/p/ftest/library/"+c.ref+"/"+c.path, nil),
			req("POST", "/p/ftest/library/check/"+c.ref+"/"+c.path, form("hash", testHash, "text", "x")),
			req("POST", "/p/ftest/library/save/"+c.ref+"/"+c.path, form("hash", testHash, "text", "x", "ack", "1")),
			req("POST", "/p/ftest/library/rm/"+c.ref+"/"+c.path, form("hash", testHash)),
		} {
			if w := f.do(r); w.Code != 404 {
				t.Errorf("%s %s: %d", r.Method, r.URL, w.Code)
			}
		}
	}
	if calls := f.cli.Calls(); len(calls) != 0 {
		t.Errorf("CLI calls for a bad path: %q", calls)
	}
	time.Sleep(10 * time.Millisecond)
	if got := f.run.Argvs(); len(got) != 0 {
		t.Errorf("jobs for a bad path: %q", got)
	}
}

func TestLibraryGuard(t *testing.T) {
	f := libraryFixture(t)
	for _, path := range []string{
		"/p/ftest/library/check/local:kv2@1/option.toml", "/p/ftest/library/save/local:kv2@1/option.toml",
		"/p/ftest/library/rm/local:kv2@1/entrypoints/kv.sh", "/p/ftest/library/new",
	} {
		r := req("POST", path, form("hash", testHash, "text", "x", "ack", "1", "kind", "pack", "name", "shop", "from", ""))
		for _, c := range []struct {
			name   string
			mutate func(*http.Request)
			code   int
		}{
			{"no cookie", func(r *http.Request) { r.Header.Del("Cookie") }, 401},
			{"foreign Origin", func(r *http.Request) { r.Header.Set("Origin", "http://b.ftest.localhost") }, 403},
			{"no Origin", func(r *http.Request) { r.Header.Del("Origin") }, 403},
		} {
			rr := r.Clone(context.Background())
			c.mutate(rr)
			if w := f.do(rr); w.Code != c.code {
				t.Errorf("%s %s: %d, want %d", path, c.name, w.Code, c.code)
			}
		}
	}
	for _, path := range []string{"/p/ftest/library", "/p/ftest/library/local:kv2@1/option.toml"} {
		r := req("GET", path, nil)
		r.Header.Del("Cookie")
		if w := f.do(r); w.Code != 401 {
			t.Errorf("GET %s without cookie: %d", path, w.Code)
		}
	}
	if calls := f.cli.Calls(); len(calls) != 0 {
		t.Errorf("CLI calls on refusal: %q", calls)
	}
	time.Sleep(10 * time.Millisecond)
	if got := f.run.Argvs(); len(got) != 0 {
		t.Errorf("jobs on refusal: %q", got)
	}
}
