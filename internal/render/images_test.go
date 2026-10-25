package render

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/config"
)

const vipsAddon = `# a user addon
ARG BASE_IMAGE
FROM ${BASE_IMAGE}
USER root
RUN apt-get update -qq && apt-get install -y --no-install-recommends libvips-tools
USER appuser
`

var hash8Re = regexp.MustCompile(`-[0-9a-f]{8}$`)

func withVips(p *config.Project) {
	p.Image.Addons = []string{"containerfiles/Containerfile.pg-client", "containerfiles/Containerfile.vips"}
}

// chainTags builds master's instance of ws with the vips addon and returns
// its build tags by step name.
func chainTags(t *testing.T, ws string) map[string]string {
	t.Helper()
	in := mustInstance(t, mustBuild(t, fixtureInputs(t, ws, withVips)), "master")
	out := map[string]string{}
	for _, b := range in.Builds {
		out[b.Name] = b.Tag
	}
	return out
}

func TestImageChain(t *testing.T) {
	ws := fixtureWS(t)
	writeFile(t, filepath.Join(ws, "containerfiles", "Containerfile.vips"), vipsAddon)
	m := mustBuild(t, fixtureInputs(t, ws, withVips))
	in := mustInstance(t, m, "master")

	var names []string
	for _, b := range in.Builds {
		names = append(names, b.Name)
		dir := filepath.Join(ws, "containerfiles")
		if filepath.Dir(b.File) != dir || b.Context != dir {
			t.Errorf("%s: File %s Context %s; want both under %s", b.Name, b.File, b.Context, dir)
		}
		if !hash8Re.MatchString(b.Tag) {
			t.Errorf("%s: tag %q does not end in -<hash8>", b.Name, b.Tag)
		}
	}
	want := []string{"base", "addon pg-client", "addon vips", "nvim", "playwright"}
	if !slices.Equal(names, want) {
		t.Fatalf("Builds = %v; want %v", names, want)
	}
	b := in.Builds
	if _, ok := b[0].Args["BASE_IMAGE"]; ok {
		t.Errorf("base step has BASE_IMAGE")
	}
	if b[0].Args["RUBY_VERSION"] != "3.3.6" || b[0].Args["NODE_VERSION"] != "22.11.0" {
		t.Errorf("base args = %v", b[0].Args)
	}
	for i := 1; i <= 3; i++ {
		prev := b[i-1].Tag
		if got := b[i].Args["BASE_IMAGE"]; got != prev {
			t.Errorf("%s BASE_IMAGE = %q; want %q", b[i].Name, got, prev)
		}
	}
	if in.Values["image.base"] != b[2].Tag {
		t.Errorf("image.base = %q; want the addon vips tag %q", in.Values["image.base"], b[2].Tag)
	}
	if b[3].Args["BASE_IMAGE"] != in.Values["image.base"] {
		t.Errorf("nvim BASE_IMAGE is not image.base")
	}
	if !strings.HasPrefix(in.Values["image.base"], "localhost/filial/base:ruby3.3.6-node22.11.0-") {
		t.Errorf("image.base = %q", in.Values["image.base"])
	}
	for _, svc := range []string{"rails", "deps"} {
		if in.Values["image."+svc] != in.Values["image.base"] {
			t.Errorf("image.%s = %q; want image.base", svc, in.Values["image."+svc])
		}
	}
	if !strings.HasPrefix(b[3].Tag, "localhost/filial/nvim:ruby3.3.6-node22.11.0-") || in.Values["image.nvim"] != b[3].Tag {
		t.Errorf("nvim tag %q, image.nvim %q", b[3].Tag, in.Values["image.nvim"])
	}

	pw := b[4]
	if _, ok := pw.Args["BASE_IMAGE"]; ok {
		t.Errorf("playwright has BASE_IMAGE")
	}
	if pw.Args["PLAYWRIGHT_VERSION"] != "1.54.0" {
		t.Errorf("PLAYWRIGHT_VERSION = %q", pw.Args["PLAYWRIGHT_VERSION"])
	}
	if !strings.HasPrefix(pw.Tag, "localhost/filial/playwright:1.54.0-") {
		t.Errorf("playwright tag = %q", pw.Tag)
	}
	if !slices.Equal(pw.From, []string{"mcr.microsoft.com/playwright:v1.54.0-noble"}) {
		t.Errorf("playwright From = %v", pw.From)
	}
	if in.Values["image.browser"] != pw.Tag {
		t.Errorf("image.browser = %q; want %q", in.Values["image.browser"], pw.Tag)
	}

	// One byte of the vips addon: that step and what builds on it.
	before := chainTags(t, ws)
	writeFile(t, filepath.Join(ws, "containerfiles", "Containerfile.vips"), vipsAddon+"#\n")
	after := chainTags(t, ws)
	for name, changed := range map[string]bool{"base": false, "addon pg-client": false, "addon vips": true, "nvim": true, "playwright": false} {
		if (before[name] != after[name]) != changed {
			t.Errorf("vips edit: %s tag changed = %v; want %v", name, before[name] != after[name], changed)
		}
	}

	// One byte of the base: every chain and tool tag, never the standalone one.
	base := filepath.Join(ws, "containerfiles", "Containerfile.base")
	data, err := os.ReadFile(base)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, base, string(data)+"#\n")
	final := chainTags(t, ws)
	for name, changed := range map[string]bool{"base": true, "addon pg-client": true, "addon vips": true, "nvim": true, "playwright": false} {
		if (after[name] != final[name]) != changed {
			t.Errorf("base edit: %s tag changed = %v; want %v", name, after[name] != final[name], changed)
		}
	}
}

func TestImageChainMissingFile(t *testing.T) {
	for _, tc := range []struct {
		file, eject string
		off         func(*config.Project)
	}{
		{"Containerfile.nvim", "railswitch containerfile eject nvim", func(p *config.Project) { p.Tools.Nvim = false; delete(p.Secrets, "github_token") }},
		{"Containerfile.playwright", "railswitch containerfile eject playwright", func(p *config.Project) { p.Options["browser"] = "none" }},
		{"Containerfile.pg-client", "railswitch containerfile eject pg-client", func(p *config.Project) { p.Image.Addons = nil }},
	} {
		ws := fixtureWS(t)
		if err := os.Remove(filepath.Join(ws, "containerfiles", tc.file)); err != nil {
			t.Fatal(err)
		}
		err := buildErr(fixtureInputs(t, ws, nil))
		if err == nil || !strings.Contains(err.Error(), tc.eject) {
			t.Errorf("without %s: error = %v; want %q", tc.file, err, tc.eject)
		}
		if err := buildErr(fixtureInputs(t, ws, tc.off)); err != nil {
			t.Errorf("without %s, not used: %v", tc.file, err)
		}
	}
}

func TestFromQualified(t *testing.T) {
	_, in := filial(t, nil)
	want := []string{"docker.io/library/node:22.11.0-slim", "docker.io/library/ruby:3.3.6-slim"}
	if got := in.Builds[0].From; !slices.Equal(got, want) {
		t.Errorf("base From = %v; want %v", got, want)
	}
	// nvim: its helper stage's FROM, never ${BASE_IMAGE}.
	for _, b := range in.Builds {
		if b.Name == "nvim" && !slices.Equal(b.From, []string{"docker.io/library/debian:trixie-slim"}) {
			t.Errorf("nvim From = %v", b.From)
		}
	}

	ws := fixtureWS(t)
	writeFile(t, filepath.Join(ws, "containerfiles", "Containerfile.base"), "FROM ruby:3.3-slim\nUSER appuser\n")
	err := buildErr(fixtureInputs(t, ws, nil))
	if err == nil || !strings.Contains(err.Error(), "Containerfile.base") || !strings.Contains(err.Error(), "fully qualified") {
		t.Errorf("short FROM: error = %v", err)
	}

	// scratch, stage names and digests are fine; an ARG default is expanded.
	ws = fixtureWS(t)
	writeFile(t, filepath.Join(ws, "containerfiles", "Containerfile.base"), `ARG DEBIAN=trixie
FROM docker.io/library/debian:${DEBIAN}-slim AS os
FROM scratch AS empty
FROM quay.io/x/y@sha256:`+strings.Repeat("0", 64)+`
FROM os
USER appuser
`)
	in = mustInstance(t, mustBuild(t, fixtureInputs(t, ws, nil)), "master")
	want = []string{"docker.io/library/debian:trixie-slim", "quay.io/x/y@sha256:" + strings.Repeat("0", 64)}
	if got := in.Builds[0].From; !slices.Equal(got, want) {
		t.Errorf("From = %v; want %v", got, want)
	}
}

func TestAddonContract(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       []string
	}{
		{"last FROM", "ARG BASE_IMAGE\nFROM ${BASE_IMAGE} AS b\nFROM debian:trixie\n", []string{"Containerfile.vips", "FROM ${BASE_IMAGE}"}},
		{"no ARG", "FROM ${BASE_IMAGE}\nUSER appuser\n", []string{"Containerfile.vips", "ARG BASE_IMAGE"}},
		{"USER root", "ARG BASE_IMAGE\nFROM ${BASE_IMAGE}\nUSER root\nRUN true\n", []string{"Containerfile.vips", "USER"}},
	} {
		ws := fixtureWS(t)
		writeFile(t, filepath.Join(ws, "containerfiles", "Containerfile.vips"), tc.body)
		err := buildErr(fixtureInputs(t, ws, withVips))
		if err == nil {
			t.Errorf("%s: no error", tc.name)
			continue
		}
		for _, w := range tc.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: error %q lacks %q", tc.name, err, w)
			}
		}
	}
	// USER 1000 and 1000:1000 are fine; a continued line still counts.
	for _, user := range []string{"1000", "1000:1000", "appuser"} {
		ws := fixtureWS(t)
		writeFile(t, filepath.Join(ws, "containerfiles", "Containerfile.vips"), "ARG BASE_IMAGE\nFROM \\\n  ${BASE_IMAGE}\nUSER "+user+"\n")
		if err := buildErr(fixtureInputs(t, ws, withVips)); err != nil {
			t.Errorf("USER %s: %v", user, err)
		}
	}
}

// Controller ruling 3 / Review Focus 1: a hostile version file never reaches a
// tag or build arg; the chain falls back with a warning.
func TestHostileVersionNeverInTag(t *testing.T) {
	ws := fixtureWS(t)
	writeFile(t, filepath.Join(ws, "master", ".ruby-version"), "3.3.6; rm -rf ~\n")
	in := mustInstance(t, mustBuild(t, fixtureInputs(t, ws, nil)), "master")
	base := in.Builds[0]
	if base.Args["RUBY_VERSION"] != "3.4.2" {
		t.Errorf("RUBY_VERSION = %q; want the fallback 3.4.2", base.Args["RUBY_VERSION"])
	}
	if !strings.HasPrefix(base.Tag, "localhost/filial/base:ruby3.4.2-node22.11.0-") {
		t.Errorf("base tag = %q", base.Tag)
	}
	for _, b := range in.Builds {
		for k, v := range b.Args {
			if strings.Contains(v, "rm -rf") {
				t.Errorf("%s arg %s = %q", b.Name, k, v)
			}
		}
		if strings.Contains(b.Tag, "rm") {
			t.Errorf("%s tag = %q", b.Name, b.Tag)
		}
	}
	for k, v := range in.Values {
		if strings.Contains(v, "rm -rf") {
			t.Errorf("Values[%s] = %q", k, v)
		}
	}
	if len(in.Warnings) == 0 || !strings.Contains(strings.Join(in.Warnings, "\n"), ".ruby-version") {
		t.Errorf("Warnings = %q; want one naming .ruby-version", in.Warnings)
	}
}

func TestBadPin(t *testing.T) {
	err := buildErr(fixtureInputs(t, fixtureWS(t), func(p *config.Project) { p.Versions["postgres"] = "16; rm -rf ~" }))
	if err == nil || !strings.Contains(err.Error(), "postgres") {
		t.Errorf("error = %v; want an invalid pin error", err)
	}
}
