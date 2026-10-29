package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// commented is a valid project.toml with comments and no [image].
const commented = `schema = 1   # keep me

# a comment line
[project]
name   = "Demo"     # trailing comment
prefix = "demo"

[stack]
pack = "rails@1"
app  = "rails"

[options]
database = "postgres@1"   # the db
`

func writeProject(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "project.toml")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSetImageBaseKeepsComments(t *testing.T) {
	path := writeProject(t, commented)
	if err := SetImageBase(path, "containerfiles/Containerfile.base"); err != nil {
		t.Fatal(err)
	}
	got := readString(t, path)
	want := commented + "\n[image]\nbase = \"containerfiles/Containerfile.base\"\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	// Same value again: a no-op.
	if err := SetImageBase(path, "containerfiles/Containerfile.base"); err != nil {
		t.Fatal(err)
	}
	if got2 := readString(t, path); got2 != got {
		t.Fatalf("same-value set changed the file:\n%s", got2)
	}
}

func TestSetImageBaseInExistingTable(t *testing.T) {
	in := commented + "\n[image]   # the chain\naddons = [] # none yet\n\n# trailer\n"
	path := writeProject(t, in)
	if err := SetImageBase(path, "containerfiles/Containerfile.base"); err != nil {
		t.Fatal(err)
	}
	want := commented + "\n[image]   # the chain\naddons = [] # none yet\nbase = \"containerfiles/Containerfile.base\"\n\n# trailer\n"
	if got := readString(t, path); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestSetImageBaseOtherValue(t *testing.T) {
	in := commented + "\n[image]\nbase = \"containerfiles/Containerfile.mine\"\n"
	path := writeProject(t, in)
	err := SetImageBase(path, "containerfiles/Containerfile.base")
	if err == nil || !strings.Contains(err.Error(), "Containerfile.mine") {
		t.Fatalf("err = %v; want one naming Containerfile.mine", err)
	}
	if got := readString(t, path); got != in {
		t.Fatalf("file changed:\n%s", got)
	}
}

const withBase = commented + "\n[image]\nbase = \"containerfiles/Containerfile.base\"\n"

func TestAppendImageAddonOrder(t *testing.T) {
	path := writeProject(t, withBase+"addons = [\"containerfiles/Containerfile.a\"]   # mine\n")
	if err := AppendImageAddon(path, "containerfiles/Containerfile.b"); err != nil {
		t.Fatal(err)
	}
	want := withBase + "addons = [\"containerfiles/Containerfile.a\", \"containerfiles/Containerfile.b\"]   # mine\n"
	if got := readString(t, path); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	p, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.Image.Addons, " ") != "containerfiles/Containerfile.a containerfiles/Containerfile.b" {
		t.Errorf("addons = %q", p.Image.Addons)
	}
}

func TestAppendImageAddonForms(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"no key", withBase, withBase + "addons = [\"containerfiles/Containerfile.b\"]\n"},
		{"empty", withBase + "addons = []\n", withBase + "addons = [\"containerfiles/Containerfile.b\"]\n"},
		{"multi-line trailing comma",
			withBase + "addons = [\n  \"containerfiles/Containerfile.a\", # a\n]\n",
			withBase + "addons = [\n  \"containerfiles/Containerfile.a\", \"containerfiles/Containerfile.b\" # a\n]\n"},
		{"multi-line no comma",
			withBase + "addons = [\n  \"containerfiles/Containerfile.a\"\n]\n",
			withBase + "addons = [\n  \"containerfiles/Containerfile.a\", \"containerfiles/Containerfile.b\"\n]\n"},
		{"no [image]", commented, ""}, // base missing: the result fails Validate
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeProject(t, tc.in)
			err := AppendImageAddon(path, "containerfiles/Containerfile.b")
			if tc.want == "" {
				if err == nil || !strings.Contains(err.Error(), "image.base") {
					t.Fatalf("err = %v; want a Validate error naming image.base", err)
				}
				if got := readString(t, path); got != tc.in {
					t.Fatalf("file changed:\n%s", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := readString(t, path); got != tc.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

func TestAppendImageAddonDuplicate(t *testing.T) {
	path := writeProject(t, withBase+"addons = [\"containerfiles/Containerfile.a\"]\n")
	err := AppendImageAddon(path, "containerfiles/Containerfile.a")
	if err == nil || !strings.Contains(err.Error(), "Containerfile.a") {
		t.Fatalf("err = %v; want one naming the existing entry", err)
	}
}

func TestEditRefusesInline(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"inline", strings.Replace(commented, "# a comment line\n", "image = { base = \"x\" }\n", 1)},
		{"dotted root", strings.Replace(commented, "# a comment line\n", "image.base = \"containerfiles/Containerfile.x\"\n", 1)},
		{"dotted quoted", strings.Replace(commented, "# a comment line\n", "\"image\" . base = \"containerfiles/Containerfile.x\"\n", 1)},
		{"subtable", commented + "\n[image.extra]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeProject(t, tc.in)
			for _, err := range []error{
				SetImageBase(path, "containerfiles/Containerfile.base"),
				AppendImageAddon(path, "containerfiles/Containerfile.b"),
			} {
				if err == nil || !strings.Contains(err.Error(), "by hand") {
					t.Errorf("err = %v; want one saying by hand", err)
				}
			}
			if got := readString(t, path); got != tc.in {
				t.Fatalf("file changed:\n%s", got)
			}
		})
	}
}

// TestEditSkipsStrings: brackets, quotes and "[image]" inside strings and
// multi-line strings are not structure.
func TestEditSkipsStrings(t *testing.T) {
	in := strings.Replace(commented, "# a comment line\n", "[env]\nA = \"\"\"\n[image] base = 'x' \\\"\"\"\"\nB = '[image] # \"'\nC = '''\n[image]'''\n", 1)
	path := writeProject(t, in)
	if err := SetImageBase(path, "containerfiles/Containerfile.base"); err != nil {
		t.Fatal(err)
	}
	if got, want := readString(t, path), in+"\n[image]\nbase = \"containerfiles/Containerfile.base\"\n"; got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestAddSecretKeepsComments(t *testing.T) {
	commented := commented + "\n[image]  # the chain\nbase = \"containerfiles/Containerfile.base\"\n"
	path := writeProject(t, commented)
	if err := AddSecret(path, "x", Secret{Services: []string{"rails"}}); err != nil {
		t.Fatal(err)
	}
	got := readString(t, path)
	if want := commented + "\n[secrets.x]\nservices = [\"rails\"]\n"; got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if err := AddSecret(path, "x", Secret{Services: []string{"rails"}}); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("second add: got %v", err)
	}
	if readString(t, path) != got {
		t.Fatal("refused add changed the file")
	}
	if err := AddSecret(path, "y", Secret{Services: []string{"rails", "worker"}, Env: "Y_KEY", Scope: "machine"}); err != nil {
		t.Fatal(err)
	}
	if want := got + "\n[secrets.y]\nservices = [\"rails\", \"worker\"]\nenv = \"Y_KEY\"\nscope = \"machine\"\n"; readString(t, path) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", readString(t, path), want)
	}
	if err := AddSecret(path, "Bad", Secret{Services: []string{"rails"}}); err == nil {
		t.Fatal("invalid name: want error")
	}
}

// edited is a valid project.toml with a comment above and after every edited
// table or key.
const edited = `schema = 1

# hide
hide_links = ["Alpha", "Beta"]   # hidden ones
# after hide

[project]
name   = "Demo"
prefix = "demo"

[stack]
pack = "rails@1"
app  = "rails"

[image]
base = "containerfiles/Containerfile.base"

# options above
[options]
# before database
database = "postgres@1"   # the db
cache = "valkey@1"
# options after

# env above
[env]
# before A
A = "1"   # a
B = "2"
# env after

# link one above
[[links]]
# in one
label = "One"
url   = "http://one"   # one url
# link one after

# link two above
[[links]]
label = "Two"
url   = "http://two"
# link two after
`

func mustDoc(t *testing.T, data string) {
	t.Helper()
	path := writeProject(t, data)
	p, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v\n%s", err, data)
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("Validate: %v\n%s", err, data)
	}
}

func replaceOnce(t *testing.T, s, old, new string) string {
	t.Helper()
	if strings.Count(s, old) != 1 {
		t.Fatalf("%q occurs %d times", old, strings.Count(s, old))
	}
	return strings.Replace(s, old, new, 1)
}

func TestSetOptionKeepsComments(t *testing.T) {
	got, err := SetOption(edited, "cache", "redis@2", true)
	if err != nil {
		t.Fatal(err)
	}
	if want := replaceOnce(t, edited, `cache = "valkey@1"`, `cache = "redis@2"`); got != want {
		t.Errorf("got\n%s", got)
	}
	mustDoc(t, got)

	got, err = SetOption(edited, "database", "none", true)
	if err != nil {
		t.Fatal(err)
	}
	if want := replaceOnce(t, edited, `database = "postgres@1"`, `database = "none"`); got != want {
		t.Errorf("trailing comment lost:\n%s", got)
	}

	got, err = SetOption(edited, "storage", "minio@1", true)
	if err != nil {
		t.Fatal(err)
	}
	if want := replaceOnce(t, edited, "cache = \"valkey@1\"\n", "cache = \"valkey@1\"\nstorage = \"minio@1\"\n"); got != want {
		t.Errorf("insert lands after the last key:\n%s", got)
	}
}

func TestSetOptionNoneSlotAndExtra(t *testing.T) {
	got, err := SetOption(edited, "queue", "sidekiq@1", false)
	if err != nil {
		t.Fatal(err)
	}
	got, err = SetOption(got, "queue", "none", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != edited {
		t.Errorf("set then none on an extra key is not the identity:\n%s", got)
	}
	got, err = SetOption(edited, "absent", "none", false)
	if err != nil || got != edited {
		t.Errorf("none on an absent extra key: %v, changed=%v", err, got != edited)
	}
}

func TestSetOptionNoOptionsTable(t *testing.T) {
	data := replaceOnce(t, commented, "[options]\ndatabase = \"postgres@1\"   # the db\n", "")
	got, err := SetOption(data, "cache", "valkey@1", true)
	if err != nil {
		t.Fatal(err)
	}
	if want := data + "\n[options]\ncache = \"valkey@1\"\n"; got != want {
		t.Errorf("got\n%s", got)
	}
}

func TestEnvSetUnset(t *testing.T) {
	got, err := SetEnv(edited, "A", "9")
	if err != nil {
		t.Fatal(err)
	}
	if want := replaceOnce(t, edited, `A = "1"`, `A = "9"`); got != want {
		t.Errorf("got\n%s", got)
	}
	got, err = SetEnv(edited, "C", "3")
	if err != nil {
		t.Fatal(err)
	}
	if want := replaceOnce(t, edited, "B = \"2\"\n", "B = \"2\"\nC = \"3\"\n"); got != want {
		t.Errorf("got\n%s", got)
	}
	got, err = UnsetEnv(edited, "A")
	if err != nil {
		t.Fatal(err)
	}
	if want := replaceOnce(t, edited, "A = \"1\"   # a\n", ""); got != want {
		t.Errorf("got\n%s", got)
	}
	mustDoc(t, got)
	if _, err := UnsetEnv(edited, "NOPE"); err == nil {
		t.Error("unset of an absent name is not an error")
	}
	got, err = SetEnv(commented, "X", "1")
	if err != nil {
		t.Fatal(err)
	}
	if want := commented + "\n[env]\nX = \"1\"\n"; got != want {
		t.Errorf("got\n%s", got)
	}
}

func TestAddRemoveLink(t *testing.T) {
	got, err := AddLink(edited, "Three", "http://three")
	if err != nil {
		t.Fatal(err)
	}
	if want := edited + "\n[[links]]\nlabel = \"Three\"\nurl = \"http://three\"\n"; got != want {
		t.Errorf("got\n%s", got)
	}
	mustDoc(t, got)
	if _, err := AddLink(edited, "One", "http://x"); err == nil {
		t.Error("duplicate label accepted")
	}

	got, err = RemoveLink(edited, "One")
	if err != nil {
		t.Fatal(err)
	}
	if want := replaceOnce(t, edited, "[[links]]\n# in one\nlabel = \"One\"\nurl   = \"http://one\"   # one url\n", ""); got != want {
		t.Errorf("got\n%s", got)
	}
	mustDoc(t, got)
	got, err = RemoveLink(edited, "Two")
	if err != nil {
		t.Fatal(err)
	}
	if want := replaceOnce(t, edited, "[[links]]\nlabel = \"Two\"\nurl   = \"http://two\"\n", ""); got != want {
		t.Errorf("got\n%s", got)
	}
	if _, err := RemoveLink(edited, "Nope"); err == nil {
		t.Error("remove of an absent label is not an error")
	}
}

func TestHideShowLink(t *testing.T) {
	got, err := HideLink(edited, "Gamma")
	if err != nil {
		t.Fatal(err)
	}
	if want := replaceOnce(t, edited, `"Beta"]`, `"Beta", "Gamma"]`); got != want {
		t.Errorf("got\n%s", got)
	}
	got, err = ShowLink(edited, "Alpha")
	if err != nil {
		t.Fatal(err)
	}
	if want := replaceOnce(t, edited, `["Alpha", "Beta"]`, `["Beta"]`); got != want {
		t.Errorf("got\n%s", got)
	}
	got, err = ShowLink(edited, "Beta")
	if err != nil {
		t.Fatal(err)
	}
	if want := replaceOnce(t, edited, `["Alpha", "Beta"]`, `["Alpha"]`); got != want {
		t.Errorf("got\n%s", got)
	}
	mustDoc(t, got)
	if _, err := HideLink(edited, "Alpha"); err == nil {
		t.Error("hide of a hidden label")
	}
	if _, err := ShowLink(edited, "Gamma"); err == nil {
		t.Error("show of a shown label")
	}
	// No hide_links yet: the key lands before the first table.
	got, err = HideLink(commented, "Z")
	if err != nil {
		t.Fatal(err)
	}
	if want := replaceOnce(t, commented, "schema = 1   # keep me\n", "schema = 1   # keep me\nhide_links = [\"Z\"]\n"); got != want {
		t.Errorf("got\n%s", got)
	}
	got, err = HideLink("# c\n[project]\nname = \"x\"\n", "Z")
	if err != nil {
		t.Fatal(err)
	}
	if want := "# c\nhide_links = [\"Z\"]\n\n[project]\nname = \"x\"\n"; got != want {
		t.Errorf("got\n%s", got)
	}
	multi := replaceOnce(t, edited, `hide_links = ["Alpha", "Beta"]   # hidden ones`, "hide_links = [\n  \"Alpha\",\n  \"Beta\",   # b\n]")
	got, err = ShowLink(multi, "Alpha")
	if err != nil {
		t.Fatal(err)
	}
	if want := replaceOnce(t, multi, "  \"Alpha\",\n", ""); got != want {
		t.Errorf("got\n%s", got)
	}
	got, err = ShowLink(multi, "Beta")
	if err != nil {
		t.Fatal(err)
	}
	if want := replaceOnce(t, multi, "  \"Beta\",   # b\n", "  # b\n"); got != want {
		t.Errorf("got\n%s", got)
	}
}

func TestEditBlockedShapes(t *testing.T) {
	inline := "schema = 1\nenv = { X = \"1\" }\n"
	links := "schema = 1\nlinks = [{ label = \"A\", url = \"u\" }]\n"
	cases := []struct {
		name string
		data string
		do   func(string) (string, error)
		line int
	}{
		{"inline env set", inline, func(d string) (string, error) { return SetEnv(d, "Y", "2") }, 2},
		{"inline env unset", inline, func(d string) (string, error) { return UnsetEnv(d, "X") }, 2},
		{"inline options", "schema = 1\noptions = { cache = \"valkey@1\" }\n", func(d string) (string, error) { return SetOption(d, "cache", "none", true) }, 2},
		{"dotted options", "schema = 1\noptions.cache = \"valkey@1\"\n", func(d string) (string, error) { return SetOption(d, "cache", "x@1", true) }, 2},
		{"dotted env", "schema = 1\nenv.X = \"1\"\n", func(d string) (string, error) { return SetEnv(d, "Y", "1") }, 2},
		{"inline links add", links, func(d string) (string, error) { return AddLink(d, "B", "u") }, 2},
		{"inline links remove", links, func(d string) (string, error) { return RemoveLink(d, "A") }, 2},
		{"hide_links not an array", "schema = 1\nhide_links = \"A\"\n", func(d string) (string, error) { return HideLink(d, "B") }, 2},
	}
	for _, c := range cases {
		_, err := c.do(c.data)
		var be *BlockedError
		if !errors.As(err, &be) {
			t.Errorf("%s: %v, want *BlockedError", c.name, err)
			continue
		}
		if be.Line != c.line {
			t.Errorf("%s: line %d, want %d", c.name, be.Line, c.line)
		}
		if want := fmt.Sprintf("project.toml:%d: ", c.line); !strings.HasPrefix(err.Error(), want) || !strings.HasSuffix(err.Error(), ": edit project.toml directly") {
			t.Errorf("%s: message %q", c.name, err)
		}
	}
}

func TestEditQuotesValues(t *testing.T) {
	val := "a\"b\\c{x}\tz" // a newline is a Validate error, not an edit's
	got, err := SetEnv(edited, "Q", val)
	if err != nil {
		t.Fatal(err)
	}
	mustDoc(t, got)
	p, err := decode("project.toml", []byte(got))
	if err != nil {
		t.Fatal(err)
	}
	if p.Env["Q"] != val {
		t.Errorf("env round trip: %q", p.Env["Q"])
	}
	label, url := "L\"x\\y", "{worktree.host}/a\"b\\c"
	got, err = AddLink(edited, label, url)
	if err != nil {
		t.Fatal(err)
	}
	mustDoc(t, got)
	p, err = decode("project.toml", []byte(got))
	if err != nil {
		t.Fatal(err)
	}
	if l := p.Links[len(p.Links)-1]; l.Label != label || l.URL != url {
		t.Errorf("link round trip: %+v", l)
	}
	got, err = RemoveLink(got, label)
	if err != nil {
		t.Fatal(err)
	}
	if got != edited {
		t.Errorf("add then remove a quoted link:\n%s", got)
	}
	got, err = HideLink(edited, label)
	if err != nil {
		t.Fatal(err)
	}
	if got, err = ShowLink(got, label); err != nil || got != edited {
		t.Errorf("hide then show a quoted label is not the identity: %v", err)
	}
}

// A link added then removed leaves the file as it was: the blank line
// AddLink puts before its table goes with it.
func TestAddThenRemoveLinkRoundTrips(t *testing.T) {
	for _, data := range []string{
		"schema = 1\n\n[image]\nbase = \"containerfiles/Containerfile.base\"\n",
		"schema = 1\n\n[[links]]\nlabel = \"A\"\nurl = \"http://a\"\n\n[env]\nX = \"1\"\n",
	} {
		added, err := AddLink(data, "Flipper", "{worktree.host}/flipper")
		if err != nil {
			t.Fatal(err)
		}
		got, err := RemoveLink(added, "Flipper")
		if err != nil {
			t.Fatal(err)
		}
		if got != data {
			t.Errorf("add then remove:\n%q\nwant\n%q", got, data)
		}
	}
}

func TestSetPack(t *testing.T) {
	in := "schema = 1\n\n[stack]\npack = \"rails@1\"   # the pack\napp = \"rails\"\n"
	out, err := SetPack(in, "local:rails@1")
	if err != nil || out != "schema = 1\n\n[stack]\npack = \"local:rails@1\"   # the pack\napp = \"rails\"\n" {
		t.Errorf("%q %v", out, err)
	}
	if _, err := SetPack("schema = 1\n", "x@1"); err == nil {
		t.Error("no [stack] pack: no error")
	}
}

func TestFirewallEdits(t *testing.T) {
	in := "schema = 1\n\n[firewall]\nallow.deps = [\"github.com\"]   # for gems\n\n[image]\nbase = \"x\"\n"
	out, err := SetFirewallMode(in, "enforce")
	if err != nil || out != "schema = 1\n\n[firewall]\nallow.deps = [\"github.com\"]   # for gems\nmode = \"enforce\"\n\n[image]\nbase = \"x\"\n" {
		t.Fatalf("mode: %q %v", out, err)
	}
	if back, err := SetFirewallMode(out, ""); err != nil || back != in {
		t.Errorf("unset mode: %q %v", back, err)
	}
	out, err = AddFirewallAllow(out, "app", "api.example.com")
	if err != nil || !strings.Contains(out, "mode = \"enforce\"\nallow.app = [\"api.example.com\"]\n") {
		t.Fatalf("add new list: %q %v", out, err)
	}
	if out, err = AddFirewallAllow(out, "deps", "gems.example.com"); err != nil || !strings.Contains(out, `allow.deps = ["github.com", "gems.example.com"]   # for gems`) {
		t.Fatalf("add to list: %q %v", out, err)
	}
	if _, err := AddFirewallAllow(out, "deps", "github.com"); err == nil {
		t.Error("a duplicate host was added")
	}
	if out, err = RemoveFirewallAllow(out, "deps", "github.com"); err != nil || !strings.Contains(out, `allow.deps = ["gems.example.com"]`) {
		t.Errorf("remove: %q %v", out, err)
	}
	if out, err := RemoveFirewallAllow(out, "app", "api.example.com"); err != nil || strings.Contains(out, "allow.app") {
		t.Errorf("remove the last: %q %v", out, err)
	}
	if _, err := AddFirewallAllow(out, "agent-x", "h"); err == nil {
		t.Error("an unknown profile was edited")
	}
	if _, err := AddFirewallAllow("[firewall.allow]\napp = []\n", "app", "h"); err == nil {
		t.Error("a [firewall.allow] table was edited")
	}
	if out, err := SetFirewallMode("schema = 1\n", "log"); err != nil || out != "schema = 1\n\n[firewall]\nmode = \"log\"\n" {
		t.Errorf("new table: %q %v", out, err)
	}
}

func TestSetImageAddons(t *testing.T) {
	in := "[image]\nbase = \"containerfiles/Containerfile.base\"\naddons = [\"containerfiles/Containerfile.pg-client\"]   # options\n"
	out, err := SetImageAddons(in, []string{"containerfiles/Containerfile.mariadb-client", "containerfiles/Containerfile.extra"})
	if err != nil || !strings.Contains(out, `addons = ["containerfiles/Containerfile.mariadb-client", "containerfiles/Containerfile.extra"]   # options`) {
		t.Errorf("set: %q %v", out, err)
	}
	if out, err := SetImageAddons(in, nil); err != nil || strings.Contains(out, "addons") {
		t.Errorf("empty: %q %v", out, err)
	}
	if out, err := SetImageAddons("[image]\nbase = \"b\"\n", []string{"containerfiles/Containerfile.x"}); err != nil || !strings.Contains(out, "addons = [\"containerfiles/Containerfile.x\"]") {
		t.Errorf("insert: %q %v", out, err)
	}
}

func TestSetHooks(t *testing.T) {
	in := "[stack]\npack = \"rails@1\"\n\n[hooks]   # mine\nstart = [\"a\"]   # keep\n"
	out, err := SetHooks(in, "start", []string{`echo "hi"`, "b"})
	if err != nil || !strings.Contains(out, `start = ["echo \"hi\"", "b"]   # keep`) || !strings.Contains(out, "[hooks]   # mine") {
		t.Errorf("set: %q %v", out, err)
	}
	if out, err := SetHooks(in, "start", nil); err != nil || strings.Contains(out, "start") || !strings.Contains(out, "[hooks]") {
		t.Errorf("remove: %q %v", out, err)
	}
	out, err = SetHooks("[stack]\npack = \"rails@1\"\n", "deps", []string{"gem install retest"})
	if err != nil || !strings.Contains(out, "[hooks]\ndeps = [\"gem install retest\"]") {
		t.Errorf("insert: %q %v", out, err)
	}
	var p Project
	if _, err := toml.Decode(out, &p); err != nil || !slices.Equal(p.Hooks.Deps, []string{"gem install retest"}) {
		t.Errorf("decoded %+v %v", p.Hooks, err)
	}
	for _, c := range []struct {
		list string
		cmds []string
		want string
	}{
		{"later", []string{"x"}, `hooks list "later"`},
		{"start", []string{"a\nb"}, "hooks.start[0]: value contains a newline"},
		{"create", []string{"ok", " "}, "hooks.create[1]: is empty"},
		{"deps", []string{"-x"}, "hooks.deps[0]: starts with -"},
		{"start", []string{"echo a\x00b"}, "hooks.start[0]: contains a control character"},
		{"start", []string{"printf '\x1b[2J'"}, "hooks.start[0]: contains a control character"},
	} {
		if _, err := SetHooks(in, c.list, c.cmds); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("SetHooks(%s, %q) = %v; want %q", c.list, c.cmds, err, c.want)
		}
	}
	if _, err := SetHooks("hooks.start = [\"a\"]\n", "start", []string{"b"}); err == nil {
		t.Error("a dotted hooks key was edited")
	}
}

// TestSetAgentSetup (D133): sets, replaces and removes [agents.<n>] setup;
// an unknown agent or a command a hook couldn't be is refused.
func TestSetAgentSetup(t *testing.T) {
	base := "schema = 1\n\n[agents.claude]\nuse = \"claude@1\"\n\n[env]\nA = \"1\"\n"
	out, err := SetAgentSetup(base, "claude", []string{"gh extension install github/gh-stack", `echo "x"`})
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse([]byte(out))
	if err != nil || !slices.Equal(p.Agents["claude"].Setup, []string{"gh extension install github/gh-stack", `echo "x"`}) || p.Env["A"] != "1" {
		t.Fatalf("set: %+v, %v\n%s", p, err, out)
	}
	if out, err = SetAgentSetup(out, "claude", []string{"one"}); err != nil {
		t.Fatal(err)
	}
	if p, _ = Parse([]byte(out)); !slices.Equal(p.Agents["claude"].Setup, []string{"one"}) {
		t.Errorf("replace:\n%s", out)
	}
	if out, err = SetAgentSetup(out, "claude", nil); err != nil || strings.Contains(out, "setup") {
		t.Errorf("remove: %v\n%s", err, out)
	}
	if _, err := SetAgentSetup(base, "nope", []string{"x"}); err == nil {
		t.Error("an unknown agent was accepted")
	}
	if _, err := SetAgentSetup(base, "claude", []string{"-rf /"}); err == nil {
		t.Error("a command starting with - was accepted")
	}
}
