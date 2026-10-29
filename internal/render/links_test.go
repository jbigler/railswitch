package render

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/config"
)

func TestLinksOrder(t *testing.T) {
	m, in := filial(t, func(p *config.Project) {
		p.Options["jobs"] = "sidekiq@1"
		p.Links = append(p.Links, config.Link{Label: "Docs", URL: "https://docs.example.com/{branch}"},
			config.Link{Label: "Login", URL: "{worktree.host}/login?return=https://x"})
	})
	got, err := m.Links(in)
	if err != nil {
		t.Fatal(err)
	}
	host := "master.filial.localhost"
	want := []Link{
		{"App", "http://" + host + "/", "app"},
		{"Admin", "http://" + host + "/admin", "project"},
		{"Docs", "https://docs.example.com/master", "project"},
		{"Login", "http://" + host + "/login?return=https://x", "project"},
		{"Mailer previews", "http://" + host + "/rails/mailers", "stack"},
		{"Routes", "http://" + host + "/rails/info/routes", "stack"},
		{"Storage console", "http://s3-ui." + host, "stack"},
		{"Sidekiq", "http://" + host + "/sidekiq", "stack"},
		{"Browser (VNC)", "http://vnc." + host + "/vnc.html?autoconnect=1&resize=scale", "stack"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Links =\n%+v\nwant\n%+v", got, want)
	}
}

func TestHideLinks(t *testing.T) {
	m, in := filial(t, func(p *config.Project) { p.HideLinks = []string{"Routes"} })
	got, err := m.Links(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range got {
		if l.Label == "Routes" {
			t.Errorf("Links has hidden Routes: %+v", got)
		}
	}
	if len(got) == 0 || got[0].Label != "App" {
		t.Errorf("Links = %+v", got)
	}

	err = buildErr(fixtureInputs(t, fixtureWS(t), func(p *config.Project) { p.HideLinks = []string{"Nope"} }))
	if err == nil || !strings.Contains(err.Error(), `hide_links: no stack link labelled "Nope"`) {
		t.Fatalf("Build = %v", err)
	}
}

func TestTasksMerge(t *testing.T) {
	m, _ := filial(t, func(p *config.Project) {
		p.Tasks = map[string]string{"test": "bin/rspec", "console": "", "lint": "bin/rubocop"}
	})
	want := []Task{
		{"lint", "bin/rubocop", "project"},
		{"test", "bin/rspec", "project"},
		{"test:system", "bin/rails test:system", "pack"},
	}
	if got := m.Tasks(); !reflect.DeepEqual(got, want) {
		t.Errorf("Tasks = %+v; want %+v", got, want)
	}
}
