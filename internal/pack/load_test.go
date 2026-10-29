package pack

import (
	"strconv"
	"strings"
	"testing"
)

// TestLinkPlaceholders: a package link's url gets the project-link
// placeholder check at load.
func TestLinkPlaceholders(t *testing.T) {
	fsys := minimal()
	fsys["options/o/option.toml"].Data = append(fsys["options/o/option.toml"].Data,
		[]byte("[[links]]\nlabel = \"X\"\nurl = \"{worktree.hots}/x\"\n")...)
	_, err := Load(fsys)
	if err == nil || !strings.Contains(err.Error(), "options/o/option.toml") || !strings.Contains(err.Error(), "{worktree.hots}") {
		t.Fatalf("Load = %v; want an error naming options/o/option.toml and {worktree.hots}", err)
	}

	fsys = minimal()
	fsys["options/o/option.toml"].Data = append(fsys["options/o/option.toml"].Data,
		[]byte("[[links]]\nlabel = \"X\"\nurl = \"x.{worktree.host}/{worktree.slug}/{branch}\"\n")...)
	if _, err := Load(fsys); err != nil {
		t.Fatalf("Load with allowed placeholders = %v", err)
	}
}

// TestTaskNameLs: a pack task named ls could never run (`task ls` lists
// the tasks): Load refuses it.
func TestTaskNameLs(t *testing.T) {
	fsys := minimal()
	fsys["packs/p/pack.toml"].Data = append(fsys["packs/p/pack.toml"].Data, []byte("\n[tasks]\nls = \"ls -la\"\n")...)
	_, err := Load(fsys)
	if err == nil || !strings.Contains(err.Error(), "packs/p/pack.toml") || !strings.Contains(err.Error(), `tasks.ls: "ls" is a railswitch task subcommand`) {
		t.Fatalf("Load = %v", err)
	}
	fsys = minimal()
	fsys["packs/p/pack.toml"].Data = append(fsys["packs/p/pack.toml"].Data, []byte("\n[tasks]\nlint = \"bin/lint\"\n")...)
	if _, err := Load(fsys); err != nil {
		t.Fatalf("Load with a task = %v", err)
	}
}

// TestDetectRulesLoad: the built-ins carry the detection rules of D87, and
// an option rule fires on a matching line, never on a commented one.
func TestDetectRulesLoad(t *testing.T) {
	c, err := LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	p := c.Packs[Ref{Name: "rails", Version: 1}]
	if got := strings.Join(p.Detect.Files, ","); got != "Gemfile,config/application.rb" {
		t.Errorf("rails@1 detect files = %q", got)
	}
	for _, tc := range []struct {
		option, line, evidence string
	}{
		{"postgres", `gem "pg", "~> 1.1"`, "Gemfile: pg"},
		{"mariadb", `gem "mysql2", "~> 0.5"`, "Gemfile: mysql2"},
		{"redis", `  gem 'redis', '>= 4.0'`, "Gemfile: redis"},
		{"rustfs", `gem "aws-sdk-s3", require: false`, "Gemfile: aws-sdk-s3"},
		{"sidekiq", `gem 'sidekiq'`, "Gemfile: sidekiq"},
		{"playwright", `gem "capybara-playwright-driver"`, "Gemfile: capybara-playwright-driver"},
		{"playwright", `gem "playwright-ruby-client"`, "Gemfile: playwright"},
	} {
		o := c.Options[Ref{Name: tc.option, Version: 1}]
		hit := -1
		for i, d := range o.Detect {
			if d.File == "Gemfile" && d.Regexp().MatchString(tc.line) {
				hit = i
				break
			}
		}
		if hit < 0 {
			t.Errorf("%s@1: no rule matches %q", tc.option, tc.line)
			continue
		}
		if ev := o.Detect[hit].EvidenceText(); ev != tc.evidence {
			t.Errorf("%s@1 evidence = %q; want %q", tc.option, ev, tc.evidence)
		}
		for _, d := range o.Detect {
			if commented := "# " + tc.line; d.Regexp().MatchString(commented) {
				t.Errorf("%s@1: rule %q matches the comment %q", tc.option, d.Match, commented)
			}
		}
	}
}

// TestDetectEvidenceDefault: a rule without evidence reads "<file>: <match>".
func TestDetectEvidenceDefault(t *testing.T) {
	fsys := minimal()
	fsys["options/o/option.toml"].Data = append(fsys["options/o/option.toml"].Data,
		[]byte("[[detect]]\nfile = \"lib/x.txt\"\nmatch = 'x'\n")...)
	c, err := Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if ev := c.Options[Ref{Name: "o", Version: 1}].Detect[0].EvidenceText(); ev != "lib/x.txt: x" {
		t.Errorf("evidence = %q", ev)
	}
}

// TestDetectRulesRefused: a rule path must be a clean relative path inside
// the repository, usable as a literal sparse-checkout pattern, and a match
// must compile.
func TestDetectRulesRefused(t *testing.T) {
	for _, bad := range []string{"../x", "/etc/x", "a//b", "./a", "a/", ".", "", "a/*.rb", "!a", "#a", `a\b`, "a b", "a/../b"} {
		fsys := minimal()
		fsys["packs/p/pack.toml"].Data = append(fsys["packs/p/pack.toml"].Data,
			[]byte("[detect]\nfiles = ["+strconv.Quote(bad)+"]\n")...)
		if _, err := Load(fsys); err == nil || !strings.Contains(err.Error(), "packs/p/pack.toml") || !strings.Contains(err.Error(), "detect.files[0]") {
			t.Errorf("pack detect file %q: Load = %v", bad, err)
		}
		fsys = minimal()
		fsys["options/o/option.toml"].Data = append(fsys["options/o/option.toml"].Data,
			[]byte("[[detect]]\nfile = "+strconv.Quote(bad)+"\nmatch = 'x'\n")...)
		if _, err := Load(fsys); err == nil || !strings.Contains(err.Error(), "options/o/option.toml") || !strings.Contains(err.Error(), "detect[0].file") {
			t.Errorf("option detect file %q: Load = %v", bad, err)
		}
	}
	for _, bad := range []string{"(", ""} {
		fsys := minimal()
		fsys["options/o/option.toml"].Data = append(fsys["options/o/option.toml"].Data,
			[]byte("[[detect]]\nfile = \"Gemfile\"\nmatch = "+strconv.Quote(bad)+"\n")...)
		if _, err := Load(fsys); err == nil || !strings.Contains(err.Error(), "detect[0].match") {
			t.Errorf("match %q: Load = %v", bad, err)
		}
	}
}
