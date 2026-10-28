package pack

import (
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/config"
)

func resolveProject(options map[string]string, nvim bool) *config.Project {
	var tools config.Tools
	if nvim {
		tools.Nvim = &config.NvimTool{}
	}
	return &config.Project{
		Schema:  1,
		Project: config.Meta{Prefix: "filial", BaseBranch: "master"},
		Stack:   config.StackRef{Pack: "rails@1", App: "rails"},
		Options: options,
		Tools:   tools,
	}
}

func TestResolve(t *testing.T) {
	cat := builtins(t)
	st, err := cat.Resolve(resolveProject(map[string]string{
		"database": "postgres@1", "cache": "redis@1", "storage": "none", "browser": "playwright@1",
	}, true))
	if err != nil {
		t.Fatal(err)
	}
	if st.Pack.Name != "rails" {
		t.Errorf("Pack = %s", st.Pack.Name)
	}
	if got := st.Option("database"); got == nil || got.Option.Name != "postgres" || got.Service != "db" {
		t.Errorf("database = %+v", got)
	}
	if st.Option("storage") != nil {
		t.Errorf("storage = none is present")
	}
	if st.Option("jobs") != nil {
		t.Errorf("an absent slot is present")
	}
	if len(st.Tools) != 1 || st.Tools[0].Name != "nvim" {
		t.Errorf("Tools = %v", st.Tools)
	}
}

// A key that is not a slot adds a service of that name; the slots come
// first in config.Slots order, then the other keys by name.
func TestResolveExtraKeys(t *testing.T) {
	st, err := builtins(t).Resolve(resolveProject(map[string]string{
		"zz": "rustfs@1", "sessions": "redis@1", "database": "postgres@1",
	}, false))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, ch := range st.Options {
		got = append(got, ch.Key+"="+ch.Service)
	}
	if want := "database=db sessions=sessions zz=zz"; strings.Join(got, " ") != want {
		t.Errorf("options = %v; want %s", got, want)
	}
}

func TestResolveGivesClash(t *testing.T) {
	cat := builtins(t)
	cat.Options[Ref{Name: "rustfs", Version: 1}].Gives["REDIS_URL"] = "x"
	_, err := cat.Resolve(resolveProject(map[string]string{"cache": "redis@1", "storage": "rustfs@1"}, false))
	if err == nil || !strings.Contains(err.Error(), "env REDIS_URL is given by options.cache (redis) and options.storage (rustfs)") {
		t.Errorf("err = %v", err)
	}
}

func TestResolveErrors(t *testing.T) {
	cat := builtins(t)
	for _, tc := range []struct {
		name    string
		options map[string]string
		app     string
		want    []string
	}{
		{"unknown ref", map[string]string{"browser": "nope@1"}, "rails", []string{"nope@1", "playwright@1"}},
		{"wrong slot", map[string]string{"cache": "postgres@1"}, "rails", []string{"postgres@1", "redis@1"}},
		{"needs", map[string]string{"jobs": "sidekiq@1", "cache": "none"}, "rails", []string{"sidekiq needs cache"}},
		{"duplicate service", map[string]string{"database": "postgres@1"}, "db", []string{"duplicate service id db"}},
		{"option twice", map[string]string{"cache": "redis@1", "sessions": "redis@1"}, "rails", []string{"options.sessions", "already fills options.cache"}},
		{"unknown extra", map[string]string{"search": "nope@1"}, "rails", []string{"options.search: unknown option nope@1"}},
		{"extra is the app", map[string]string{"rails": "redis@1"}, "rails", []string{"duplicate service id rails"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := resolveProject(tc.options, false)
			p.Stack.App = tc.app
			_, err := cat.Resolve(p)
			if err == nil {
				t.Fatal("Resolve: no error")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err, w)
				}
			}
		})
	}
}

func TestResolveWorksWith(t *testing.T) {
	cat := builtins(t)
	cat.Options[Ref{Name: "sidekiq", Version: 1}].WorksWith = []string{"hanami"}
	_, err := cat.Resolve(resolveProject(map[string]string{"cache": "redis@1", "jobs": "sidekiq@1"}, false))
	if err == nil || !strings.Contains(err.Error(), "does not work with pack rails") {
		t.Fatalf("Resolve = %v; want a works_with error", err)
	}
}

func TestResolveUnknownPack(t *testing.T) {
	p := resolveProject(nil, false)
	p.Stack.Pack = "django@1"
	_, err := builtins(t).Resolve(p)
	if err == nil || !strings.Contains(err.Error(), "rails@1") {
		t.Fatalf("Resolve = %v; want an error listing rails@1", err)
	}
}
