package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
