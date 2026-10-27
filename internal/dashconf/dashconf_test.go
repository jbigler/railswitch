package dashconf

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/machine"
)

// specTable is the spec's "Colours" table: token → flexoki L, flexoki D,
// paper L, paper D, solar L, solar D, contrast L, contrast D.
var specTable = map[string][8]string{
	"bg":     {"#FFFCF0", "#100F0F", "#FFFFFF", "#18181B", "#FDF6E3", "#002B36", "#FFFFFF", "#000000"},
	"bg2":    {"#F2F0E5", "#1C1B1A", "#F4F4F5", "#27272A", "#EEE8D5", "#073642", "#F0F0F0", "#121212"},
	"sel":    {"#E6E4D9", "#282726", "#E4E4E7", "#3F3F46", "#E4DCC4", "#0B4A5A", "#DADADA", "#2A2A2A"},
	"border": {"#CECDC3", "#403E3C", "#D4D4D8", "#52525B", "#D3CBB2", "#2E5A66", "#7A7A7A", "#8A8A8A"},
	"text":   {"#100F0F", "#CECDC3", "#18181B", "#F4F4F5", "#073642", "#EEE8D5", "#000000", "#FFFFFF"},
	"muted":  {"#6F6E69", "#878580", "#62626B", "#A1A1AA", "#586E75", "#93A1A1", "#3D3D3D", "#D0D0D0"},
	"accent": {"#BC5215", "#DA702C", "#7C3AED", "#A78BFA", "#B03F0F", "#CB4B16", "#B3261E", "#FF8A65"},
	"blue":   {"#205EA6", "#4385BE", "#1D4ED8", "#60A5FA", "#1B6699", "#268BD2", "#0040A8", "#8AB4FF"},
	"green":  {"#536907", "#879A39", "#15803D", "#4ADE80", "#5C6B00", "#859900", "#1E5E00", "#7EE787"},
	"yellow": {"#8E6B01", "#D0A215", "#A16207", "#FACC15", "#7A5C00", "#B58900", "#6B4E00", "#FFD54F"},
	"purple": {"#5E409D", "#8B7EC8", "#7E22CE", "#C084FC", "#6C71C4", "#6C71C4", "#6A1B9A", "#D2A8FF"},
	"red":    {"#AF3029", "#D14D41", "#B91C1C", "#F87171", "#DC322F", "#DC322F", "#C62828", "#FF7B72"},
}

func dirs(t *testing.T) machine.Dirs {
	d := t.TempDir()
	return machine.Dirs{Config: filepath.Join(d, "config"), Data: filepath.Join(d, "data"), Quadlet: filepath.Join(d, "q")}
}

func TestPresetsComplete(t *testing.T) {
	if len(Tokens)+len(Fixed) != 12 || len(specTable) != 12 {
		t.Fatalf("token count: %d+%d", len(Tokens), len(Fixed))
	}
	for i, name := range PresetNames {
		for m, dark := range []bool{false, true} {
			f := &File{Light: Mode{Base: name}, Dark: Mode{Base: name}}
			c := f.Colors(dark)
			if len(c) != 12 {
				t.Errorf("%s dark=%v: %d tokens", name, dark, len(c))
			}
			for tok, row := range specTable {
				if c[tok] != row[i*2+m] {
					t.Errorf("%s dark=%v %s = %q, want %q", name, dark, tok, c[tok], row[i*2+m])
				}
			}
		}
	}
}

func TestRoundTrip(t *testing.T) {
	d := dirs(t)
	f, err := Load(d)
	if err != nil {
		t.Fatal(err)
	}
	if f.Light.Base != "flexoki" || f.Dark.Base != "flexoki" || len(f.Light.Overrides)+len(f.Dark.Overrides) != 0 {
		t.Fatalf("default: %+v", f)
	}
	if err := f.Set(true, "", map[string]string{"accent": "#da702d", "bg": "#100F0F"}); err != nil {
		t.Fatal(err)
	}
	if err := f.Save(d); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(Path(d))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{"schema = 1", "[colors.dark]", `accent = "#DA702D"`} {
		if !strings.Contains(s, want) {
			t.Errorf("file lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "bg =") {
		t.Errorf("unchanged token written:\n%s", s)
	}
	g, err := Load(d)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g.Colors(true), f.Colors(true)) || g.Dark.Overrides["accent"] != "#DA702D" || len(g.Dark.Overrides) != 1 {
		t.Errorf("reloaded %+v, want %+v", g, f)
	}
	g.Reset(true)
	if len(g.Dark.Overrides) != 0 || g.Dark.Base != "flexoki" {
		t.Errorf("reset: %+v", g.Dark)
	}
}

func TestSetRejects(t *testing.T) {
	for _, tc := range []struct {
		base string
		ov   map[string]string
		want string
	}{
		{"", map[string]string{"link": "#000000"}, "link"},
		{"", map[string]string{"bg": "#12345"}, "#12345"},
		{"", map[string]string{"red": "#000000"}, "red"},
		{"neon", nil, "neon"},
	} {
		f := &File{Light: Mode{Base: "flexoki"}, Dark: Mode{Base: "flexoki"}}
		err := f.Set(false, tc.base, tc.ov)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Set(%q, %v) = %v, want error naming %q", tc.base, tc.ov, err, tc.want)
		}
	}
}

func writeFile(t *testing.T, d machine.Dirs, s string) {
	if err := os.MkdirAll(d.Config, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(d), []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNewerSchema(t *testing.T) {
	d := dirs(t)
	writeFile(t, d, "schema = 2\n")
	if _, err := Load(d); err == nil || !strings.Contains(err.Error(), "newer railswitch") {
		t.Errorf("err = %v", err)
	}
}

func TestUnknownKey(t *testing.T) {
	d := dirs(t)
	writeFile(t, d, "schema = 1\n[colors.light]\nfoo = \"#000000\"\n")
	if _, err := Load(d); err == nil || !strings.Contains(err.Error(), "foo") {
		t.Errorf("err = %v", err)
	}
}

func TestContrast(t *testing.T) {
	if r := Contrast("#000000", "#FFFFFF"); math.Abs(r-21) > 1e-9 {
		t.Errorf("black/white = %v", r)
	}
	c := (&File{Light: Mode{Base: "flexoki"}}).Colors(false)
	if r := Contrast(c["text"], c["bg"]); r < 4.5 {
		t.Errorf("flexoki light text/bg = %v", r)
	}
	c = map[string]string{"bg": "#FFFFFF", "text": "#AAAAAA"}
	w := Warnings(c)
	if len(w) != 1 || !strings.HasPrefix(w[0], "text: contrast ") || !strings.HasSuffix(w[0], ":1 against bg (below 4.5:1)") {
		t.Errorf("warnings = %q", w)
	}
}
