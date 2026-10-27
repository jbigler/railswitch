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
	if got := st.Options["database"]; got == nil || got.Option.Name != "postgres" || got.Service != "db" {
		t.Errorf("database = %+v", got)
	}
	if _, ok := st.Options["storage"]; ok {
		t.Errorf("storage = none is present")
	}
	if _, ok := st.Options["jobs"]; ok {
		t.Errorf("an absent slot is present")
	}
	if len(st.Tools) != 1 || st.Tools[0].Name != "nvim" {
		t.Errorf("Tools = %v", st.Tools)
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
