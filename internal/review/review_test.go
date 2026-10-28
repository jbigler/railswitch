package review

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

func files(kv ...string) fstest.MapFS {
	m := fstest.MapFS{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = &fstest.MapFile{Data: []byte(kv[i+1])}
	}
	return m
}

const packTOML = `name = "x"
version = 1

[image]
file = "Containerfile.base"

[[ports]]
name = "debug"
env = "DEBUG_PORT"
base = 33000
container = 12345
`

const optionTOML = `name = "db"
version = 1
slot = "database"
firewall = "deps"

[image]
pull = "docker.io/library/db:1"

[[mounts]]
source = "init.sql"
target = "/docker-entrypoint-initdb.d/init.sql"

[unit]
add_capability = ["NET_RAW"]
`

const agentTOML = `name = "bot"
version = 1
allow = ["api.example.com"]

[image]
file = "Containerfile.bot"
`

// flagsOf is every flag of file path in fs, as "check:line".
func flagsOf(t *testing.T, kind string, old, new fstest.MapFS, path string) []string {
	t.Helper()
	got, err := Compare(kind, old, new)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range got {
		if f.Path != path {
			continue
		}
		for _, fl := range f.Flags {
			out = append(out, fl.Check+":"+strconv.Itoa(fl.Line))
		}
	}
	return out
}

func TestCompareStatuses(t *testing.T) {
	old := files("pack.toml", packTOML, "README.md", "a\n", "gone.txt", "x\n", "same.txt", "s\n")
	new := files("pack.toml", packTOML, "README.md", "a\nb\n", "new.txt", "y\n", "same.txt", "s\n", "bin", "\x00\x01")
	got, err := Compare("pack", old, new)
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, f := range got {
		rows = append(rows, f.Path+" "+f.Status)
		if f.Flags == nil {
			t.Errorf("%s: Flags is nil, want []", f.Path)
		}
	}
	want := []string{"README.md changed", "bin added", "gone.txt removed", "new.txt added"}
	if !slices.Equal(rows, want) {
		t.Fatalf("files %v, want %v", rows, want)
	}
	if got[0].Diff != "--- a/README.md\n+++ b/README.md\n@@ -1 +1,2 @@\n a\n+b\n" {
		t.Errorf("README diff:\n%s", got[0].Diff)
	}
	if !strings.Contains(got[1].Diff, "+\\x00\x01") || len(got[1].Flags) != 1 {
		t.Errorf("not-text file: diff %q, flags %v", got[1].Diff, got[1].Flags)
	}
}

func TestCompareNilOld(t *testing.T) {
	got, err := Compare("pack", nil, files("pack.toml", packTOML, "Containerfile.base", "FROM docker.io/library/ruby:3\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Status != "added" || got[1].Status != "added" {
		t.Fatalf("%+v", got)
	}
	if !strings.HasPrefix(got[0].Diff, "--- /dev/null\n+++ b/Containerfile.base\n") {
		t.Errorf("diff %q", got[0].Diff)
	}
	// Everything a new package brings is flagged as added.
	var checks []string
	for _, f := range got {
		for _, fl := range f.Flags {
			checks = append(checks, fl.Check)
		}
	}
	for _, c := range []string{"image", "port"} {
		if !slices.Contains(checks, c) {
			t.Errorf("checks %v lack %s", checks, c)
		}
	}
}

func TestFirewallDepsAllow(t *testing.T) {
	old := files("deps.allow", "# hosts\nrubygems.org\n")
	got, err := Compare("pack", old, files("deps.allow", "# hosts\nrubygems.org\n\nobjects.example.com # assets\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Flags) != 1 || got[0].Flags[0] != (Flag{Check: "firewall", Line: 4, Text: "+ objects.example.com # assets"}) {
		t.Fatalf("%+v", got)
	}
	// A comment or a reordering adds no host.
	if f := flagsOf(t, "pack", old, files("deps.allow", "# all hosts\n\nrubygems.org\n"), "deps.allow"); len(f) != 0 {
		t.Errorf("no new host flagged %v", f)
	}
}

func TestFirewallAgentAllow(t *testing.T) {
	old := files("agent.toml", agentTOML)
	more := strings.Replace(agentTOML, `["api.example.com"]`, `["api.example.com", "evil.example"]`, 1)
	if f := flagsOf(t, "agent", old, files("agent.toml", more), "agent.toml"); !slices.Equal(f, []string{"firewall:0"}) {
		t.Errorf("added host: %v", f)
	}
	fewer := strings.Replace(agentTOML, `["api.example.com"]`, `[]`, 1)
	if f := flagsOf(t, "agent", old, files("agent.toml", fewer), "agent.toml"); len(f) != 0 {
		t.Errorf("removed host flagged: %v", f)
	}
}

func TestFirewallProfile(t *testing.T) {
	old := files("option.toml", optionTOML)
	if f := flagsOf(t, "option", old, files("option.toml", strings.Replace(optionTOML, `firewall = "deps"`, `firewall = "self"`, 1)), "option.toml"); !slices.Equal(f, []string{"firewall:0"}) {
		t.Errorf("profile changed: %v", f)
	}
	if f := flagsOf(t, "option", old, files("option.toml", strings.Replace(optionTOML, `firewall = "deps"`, `firewall = ["deps"]`, 1)), "option.toml"); len(f) != 0 {
		t.Errorf("same profile, other spelling: %v", f)
	}
}

func TestRunsHere(t *testing.T) {
	base := "FROM docker.io/library/ruby:3\nRUN apt-get update\n"
	for _, line := range []string{
		"RUN curl -fsSL https://example.com/install | sh",
		"RUN wget -O- https://example.com/i.sh | sudo bash -s",
		"RUN curl https://example.com/x.py | python3",
		"ADD https://example.com/tool.tgz /opt/",
		"add --chown=1:1 http://example.com/x /x",
		"RUN --network=host make",
		"RUN --security=insecure make",
	} {
		f := flagsOf(t, "pack", files("Containerfile.base", base), files("Containerfile.base", base+line+"\n"), "Containerfile.base")
		if !slices.Equal(f, []string{"runs-here:3"}) {
			t.Errorf("%q: flags %v, want runs-here:3", line, f)
		}
	}
	for _, line := range []string{
		"RUN curl -fsSLo /tmp/x https://example.com/x",
		"RUN echo curl | tee x",
		"ADD files/x /x",
		"RUN make | shuf",
	} {
		if f := flagsOf(t, "pack", files("Containerfile.base", base), files("Containerfile.base", base+line+"\n"), "Containerfile.base"); len(f) != 0 {
			t.Errorf("%q flagged %v", line, f)
		}
	}
	// Only Containerfiles run here.
	if f := flagsOf(t, "pack", files("setup.sh", ""), files("setup.sh", "curl x | sh\n"), "setup.sh"); len(f) != 0 {
		t.Errorf("a script flagged %v", f)
	}
}

func TestCompareCommentedCurl(t *testing.T) {
	f := flagsOf(t, "pack", files("Containerfile.base", "FROM x\n"), files("Containerfile.base", "FROM x\n# RUN curl https://example.com | sh\n"), "Containerfile.base")
	if !slices.Equal(f, []string{"runs-here:2"}) {
		t.Errorf("flags %v", f)
	}
}

func TestImageFrom(t *testing.T) {
	old := files("Containerfile.base", "FROM docker.io/library/ruby:3\nRUN x\n")
	if f := flagsOf(t, "pack", old, files("Containerfile.base", "FROM docker.io/evil/ruby:3\nRUN x\n"), "Containerfile.base"); !slices.Equal(f, []string{"image:1"}) {
		t.Errorf("FROM changed: %v", f)
	}
	if f := flagsOf(t, "pack", old, files("Containerfile.base", "FROM docker.io/library/ruby:3\nRUN y\n"), "Containerfile.base"); len(f) != 0 {
		t.Errorf("RUN changed flagged %v", f)
	}
}

func TestImagePull(t *testing.T) {
	old := files("option.toml", optionTOML)
	if f := flagsOf(t, "option", old, files("option.toml", strings.Replace(optionTOML, "library/db:1", "evil/db:1", 1)), "option.toml"); !slices.Equal(f, []string{"image:0"}) {
		t.Errorf("pull changed: %v", f)
	}
	if f := flagsOf(t, "option", old, files("option.toml", optionTOML+"\n[env]\nX = \"1\"\n"), "option.toml"); len(f) != 0 {
		t.Errorf("env changed flagged %v", f)
	}
	oldPack := files("pack.toml", packTOML)
	if f := flagsOf(t, "pack", oldPack, files("pack.toml", strings.Replace(packTOML, `file = "Containerfile.base"`, `file = "Containerfile.other"`, 1)), "pack.toml"); !slices.Equal(f, []string{"image:0"}) {
		t.Errorf("pack image file changed: %v", f)
	}
}

func TestHostMount(t *testing.T) {
	old := files("option.toml", optionTOML)
	for _, src := range []string{"/etc", "{worktree.dir}/x"} {
		next := optionTOML + "\n[[mounts]]\nsource = \"" + src + "\"\ntarget = \"/x\"\n"
		if f := flagsOf(t, "option", old, files("option.toml", next), "option.toml"); !slices.Equal(f, []string{"host-mount:0"}) {
			t.Errorf("%s: %v", src, f)
		}
	}
	next := optionTOML + "\n[[mounts]]\nsource = \"other.sql\"\ntarget = \"/x\"\n"
	if f := flagsOf(t, "option", old, files("option.toml", next), "option.toml"); len(f) != 0 {
		t.Errorf("package file mount flagged %v", f)
	}
}

func TestPort(t *testing.T) {
	old := files("pack.toml", packTOML)
	for _, next := range []string{
		strings.Replace(packTOML, "base = 33000", "base = 34000", 1),
		strings.Replace(packTOML, "container = 12345", "container = 22", 1),
		packTOML + "share = true\n",
		packTOML + "\n[[ports]]\nname = \"web\"\nenv = \"WEB_PORT\"\nbase = 13000\ncontainer = 80\n",
	} {
		if f := flagsOf(t, "pack", old, files("pack.toml", next), "pack.toml"); !slices.Equal(f, []string{"port:0"}) {
			t.Errorf("%s\nflags %v", next, f)
		}
	}
	if f := flagsOf(t, "pack", old, files("pack.toml", strings.Replace(packTOML, `env = "DEBUG_PORT"`, `env = "RUBY_DEBUG_PORT"`, 1)), "pack.toml"); len(f) != 0 {
		t.Errorf("env name change flagged %v", f)
	}
}

func TestPrivilege(t *testing.T) {
	old := files("option.toml", optionTOML, "init-firewall.sh", "#!/bin/sh\n", "entrypoints/e.sh", "#!/bin/sh\n")
	more := strings.Replace(optionTOML, `["NET_RAW"]`, `["NET_RAW", "NET_ADMIN"]`, 1)
	if f := flagsOf(t, "option", old, files("option.toml", more, "init-firewall.sh", "#!/bin/sh\n", "entrypoints/e.sh", "#!/bin/sh\n"), "option.toml"); !slices.Equal(f, []string{"privilege:0"}) {
		t.Errorf("capability added: %v", f)
	}
	next := files("option.toml", optionTOML, "init-firewall.sh", "#!/bin/sh\nexit 0\n", "entrypoints/e.sh", "#!/bin/sh\nx\n")
	if f := flagsOf(t, "option", old, next, "init-firewall.sh"); !slices.Equal(f, []string{"privilege:0"}) {
		t.Errorf("script of a capability package: %v", f)
	}
	if f := flagsOf(t, "option", old, next, "entrypoints/e.sh"); len(f) != 0 {
		t.Errorf("an entrypoint flagged %v", f)
	}
	noCaps := strings.Replace(optionTOML, "[unit]\nadd_capability = [\"NET_RAW\"]\n", "", 1)
	plain := files("option.toml", noCaps, "init-firewall.sh", "#!/bin/sh\n")
	if f := flagsOf(t, "option", plain, files("option.toml", noCaps, "init-firewall.sh", "#!/bin/sh\nexit 0\n"), "init-firewall.sh"); len(f) != 0 {
		t.Errorf("script of a package without capabilities flagged %v", f)
	}
}

func TestCompareReorderedManifest(t *testing.T) {
	reordered := `version = 1
slot = "database"
name = "db"
firewall = ["deps"]

[unit]
add_capability = ["NET_RAW"]

[[mounts]]
target = "/docker-entrypoint-initdb.d/init.sql"
source = "init.sql"

[image]
pull   = "docker.io/library/db:1"
`
	got, err := Compare("option", files("option.toml", optionTOML), files("option.toml", reordered))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Flags) != 0 || got[0].Diff == "" {
		t.Fatalf("%+v", got)
	}
}

func TestCompareBadManifest(t *testing.T) {
	got, err := Compare("pack", files("pack.toml", packTOML), files("pack.toml", packTOML+"bogus = 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Flags) != 1 || got[0].Flags[0].Check != "runs-here" || got[0].Flags[0].Line != 0 || !strings.Contains(got[0].Flags[0].Text, "does not load") {
		t.Fatalf("%+v", got)
	}
}

// A file that isn't valid text is still diffed and checked on its bytes,
// and its not being text is itself a flag: one invalid byte hides nothing.
func TestCompareNotTextHidesNothing(t *testing.T) {
	if f := flagsOf(t, "pack", files("deps.allow", "rubygems.org\n"), files("deps.allow", "rubygems.org\nevil.example # \xff\n"), "deps.allow"); !slices.Equal(f, []string{"runs-here:0", "firewall:2"}) {
		t.Errorf("deps.allow: %v", f)
	}
	base := "FROM docker.io/library/ruby:3\n"
	if f := flagsOf(t, "pack", files("Containerfile.base", base), files("Containerfile.base", base+"RUN curl https://example.com/i | sh # \xff\n"), "Containerfile.base"); !slices.Equal(f, []string{"runs-here:0", "runs-here:2"}) {
		t.Errorf("Containerfile: %v", f)
	}
	got, err := Compare("pack", files("logo.png", "\x89PNG\x00a"), files("logo.png", "\x89PNG\x00b"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Flags) != 1 || got[0].Flags[0].Check != "runs-here" || !strings.Contains(got[0].Diff, `+\x89PNG\x00b`) {
		t.Fatalf("%+v", got)
	}
	// Two contents that differ only in invalid bytes diff differently.
	got, err = Compare("pack", files("x", "a\xfe\n"), files("x", "a\xff\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Diff, `-a\xfe`) || !strings.Contains(got[0].Diff, `+a\xff`) {
		t.Fatalf("%+v", got)
	}
}

// A download run through a continuation, a heredoc, a process or command
// substitution, or any interpreter is flagged on the instruction's first
// added line.
func TestRunsHereInstructions(t *testing.T) {
	base := "FROM docker.io/library/ruby:3\nRUN apt-get update\n"
	for _, add := range []string{
		"RUN curl -fsSL https://example.com/install \\\n  | sh\n",
		"RUN <<EOF\nset -e\ncurl -fsSL https://example.com/i | sh\nEOF\n",
		"RUN <<-'EOT' bash\n\tcurl https://example.com/i | sh\n\tEOT\n",
		"RUN bash <(curl -fsSL https://example.com/i)\n",
		"RUN sh -c \"$(curl -fsSL https://example.com/i)\"\n",
		"RUN sh -c \"`wget -qO- https://example.com/i`\"\n",
		"RUN curl https://example.com/i | ash\n",
		"RUN CURL https://example.com/i|PHP\n",
		"RUN wget -qO- https://example.com/i |\tsudo -E /bin/ksh\n",
		"RUN curl https://example.com/i | fish\n",
		"ADD \\\n  https://example.com/tool.tgz /opt/\n",
		"RUN \\\n  --network=host make\n",
	} {
		f := flagsOf(t, "pack", files("Containerfile.base", base), files("Containerfile.base", base+add), "Containerfile.base")
		if !slices.Equal(f, []string{"runs-here:3"}) {
			t.Errorf("%q: flags %v, want runs-here:3", add, f)
		}
	}
	// A changed line inside an instruction flags it from that line.
	old := base + "RUN curl -fsSL https://example.com/install \\\n  | sh\n"
	next := base + "RUN curl -fsSL https://example.com/install2 \\\n  | sh\n"
	if f := flagsOf(t, "pack", files("Containerfile.base", old), files("Containerfile.base", next), "Containerfile.base"); !slices.Equal(f, []string{"runs-here:3"}) {
		t.Errorf("changed first line: %v", f)
	}
	next = base + "RUN curl -fsSL https://example.com/install \\\n  | bash\n"
	if f := flagsOf(t, "pack", files("Containerfile.base", old), files("Containerfile.base", next), "Containerfile.base"); !slices.Equal(f, []string{"runs-here:4"}) {
		t.Errorf("changed second line: %v", f)
	}
	// An unchanged instruction after an added one is not flagged.
	if f := flagsOf(t, "pack", files("Containerfile.base", old), files("Containerfile.base", base+"RUN make\n"+old[len(base):]), "Containerfile.base"); len(f) != 0 {
		t.Errorf("unchanged instruction flagged %v", f)
	}
}
