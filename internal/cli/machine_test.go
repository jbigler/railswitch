package cli

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
	"github.com/jbigler/railswitch/internal/testfixture"
)

const minimalFixture = "../../testdata/fixtures/minimal"

// twoProjects is filial (worktrees master, feat; master shared) and the
// minimal fixture (prefix mini, worktree master), both locked and rendered.
func twoProjects(t *testing.T) (filial, mini string, dirs machine.Dirs) {
	t.Helper()
	filial, dirs = lockedWS(t)
	mini = testfixture.Workspace(t, minimalFixture)
	mustRun(t, "-C", mini, "lock")
	reg, err := machine.Open(dirs.Data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.AddProject("mini", mini, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.AddWorktree("mini", "master", nil); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	reg.Close()
	shareSlug(t, dirs, "master")
	mustRun(t, "-C", filial, "render")
	mustRun(t, "-C", mini, "render")
	return filial, mini, dirs
}

// machineHost is fakeHost with both machine units active and Traefik
// running, attached to networks.
func machineHost(networks ...string) *systest.Fake {
	f := fakeHost()
	f.On([]string{"podman", "container", "inspect", "--format", "{{.State.Running}}", "railswitch-traefik"}, sys.Result{Stdout: []byte("true\n")}, nil)
	f.On([]string{"systemctl", "--user", "show", "-p", "ActiveState,SubState"}, sys.Result{Stdout: []byte("ActiveState=active\nSubState=running\n")}, nil)
	f.On([]string{"podman", "container", "inspect", "--format", `{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}`, "railswitch-traefik"},
		sys.Result{Stdout: []byte(strings.Join(networks, " ") + " \n")}, nil)
	return f
}

func machineJSON(t *testing.T, f *systest.Fake) machineReport {
	t.Helper()
	code, out, errs := runWith(t, f, "--json", "machine")
	if code != 0 {
		t.Fatalf("machine --json: exit %d\n%s%s", code, out, errs)
	}
	var r machineReport
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return r
}

// disjoint fails when any value occurs twice.
func disjoint[T comparable](t *testing.T, what string, vs []T) {
	t.Helper()
	seen := map[T]bool{}
	for _, v := range vs {
		if seen[v] {
			t.Errorf("%s %v occurs twice in %v", what, v, vs)
		}
		seen[v] = true
	}
}

// TestMachineTwoProjects: the two projects' IDs, ports, subnets, router
// names and hosts are pairwise disjoint.
func TestMachineTwoProjects(t *testing.T) {
	filial, mini, _ := twoProjects(t)
	r := machineJSON(t, machineHost("railswitch_machine", "filial_proxy", "mini_proxy"))

	if len(r.Projects) != 2 || r.Projects[0].Prefix != "filial" || r.Projects[1].Prefix != "mini" {
		t.Fatalf("projects %+v", r.Projects)
	}
	var ids, ports []int
	var subnets, traefikIPs []string
	for i, p := range r.Projects {
		if p.Note != "" {
			t.Errorf("%s: note %q", p.Prefix, p.Note)
		}
		if want := []string{filial, mini}[i]; p.Workspace != want {
			t.Errorf("%s: workspace %q, want %q", p.Prefix, p.Workspace, want)
		}
		subnets, traefikIPs = append(subnets, p.Subnet), append(traefikIPs, p.TraefikIP)
		for _, w := range p.Worktrees {
			ids = append(ids, w.ID)
			if len(w.Ports) == 0 {
				t.Errorf("%s %s: no ports", p.Prefix, w.Slug)
			}
			ports = slices.AppendSeq(ports, maps.Values(w.Ports))
		}
	}
	if len(ids) != 3 {
		t.Fatalf("worktree IDs %v; want 3", ids)
	}
	disjoint(t, "ID", ids)
	disjoint(t, "port", ports)
	disjoint(t, "subnet", subnets)
	disjoint(t, "Traefik IP", traefikIPs)

	fm := r.Projects[0].Worktrees[0]
	if fm.Slug != "master" || fm.Ports["app"] != 13000+fm.ID || fm.SharedURL != "https://box.tail1234.ts.net:"+strconv.Itoa(13000+fm.ID)+"/" {
		t.Errorf("filial master %+v", fm)
	}
	if ff := r.Projects[0].Worktrees[1]; ff.SharedURL != "" {
		t.Errorf("filial feat shared: %+v", ff)
	}

	var hosts, routers []string
	byProject := map[string]int{}
	for _, rt := range r.Routes {
		hosts, routers = append(hosts, rt.Host), append(routers, rt.Router)
		byProject[rt.Project]++
		if !strings.HasPrefix(rt.Router, rt.Project+"-"+rt.Worktree+"-") || rt.Service == "" {
			t.Errorf("route %+v", rt)
		}
	}
	if byProject["filial"] == 0 || byProject["mini"] == 0 {
		t.Fatalf("routes %+v", r.Routes)
	}
	if !slices.IsSorted(hosts) {
		t.Errorf("routes not sorted by host: %v", hosts)
	}
	disjoint(t, "host", hosts)
	disjoint(t, "router", routers)
	if !slices.Contains(hosts, "master.mini.localhost") || !slices.Contains(hosts, "s3.feat.filial.localhost") {
		t.Errorf("hosts %v", hosts)
	}

	for _, n := range r.Machine.TraefikNetworks {
		if !n.Attached {
			t.Errorf("network %+v not attached", n)
		}
	}
	if r.Machine.Traefik.State != "active/running" || r.Machine.Dozzle.Unit != "railswitch-dozzle.service" {
		t.Errorf("machine %+v", r.Machine)
	}

	members := map[string][]string{}
	for _, n := range r.Reach.Networks {
		members[n.Network] = n.Members
	}
	for net, want := range map[string][]string{
		"railswitch_machine": {"Traefik", "Dozzle"},
		"mini_proxy":         {"Traefik", "rails"},
		"mini-master-dev":    {"rails", "deps"},
	} {
		if got := members[net]; !slices.Equal(got, want) {
			t.Errorf("reach %s: %v, want %v", net, got, want)
		}
	}
	if fp := members["filial_proxy"]; len(fp) < 2 || fp[0] != "Traefik" || !slices.Contains(fp, "rails") || slices.Contains(fp, "db") {
		t.Errorf("reach filial_proxy: %v", fp)
	}
	if fd := members["filial-feat-dev"]; !slices.Contains(fd, "db") || slices.Contains(fd, "Traefik") {
		t.Errorf("reach filial-feat-dev: %v", fd)
	}
	if !slices.Equal(r.Reach.Notes, []string{reachesAll, reachRefused}) {
		t.Errorf("reach notes %q", r.Reach.Notes)
	}
}

// TestMachineJSON: the field names of every level.
func TestMachineJSON(t *testing.T) {
	twoProjects(t)
	code, out, errs := runWith(t, machineHost("railswitch_machine"), "--json", "machine")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errs)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	keys := func(v any) []string {
		return slices.Sorted(maps.Keys(v.(map[string]any)))
	}
	first := func(v any) any { return v.([]any)[0] }
	check := func(what string, v any, want ...string) {
		t.Helper()
		if got := keys(v); !slices.Equal(got, want) {
			t.Errorf("%s keys %v, want %v", what, got, want)
		}
	}
	check("document", doc, "machine", "projects", "reach", "routes")
	project := first(doc["projects"])
	check("project", project, "note", "prefix", "subnet", "traefik_ip", "workspace", "worktrees")
	check("worktree", first(project.(map[string]any)["worktrees"]), "id", "ports", "shared_url", "slug")
	check("route", first(doc["routes"]), "host", "project", "router", "service", "worktree")
	m := doc["machine"]
	check("machine", m, "dozzle", "traefik", "traefik_networks")
	check("traefik", m.(map[string]any)["traefik"], "state", "unit")
	check("traefik network", first(m.(map[string]any)["traefik_networks"]), "attached", "network", "project")
	check("reach", doc["reach"], "networks", "notes")
	check("reach network", first(doc["reach"].(map[string]any)["networks"]), "members", "network")
}

// TestMachineFlagsDetached: a registered project's proxy network Traefik
// isn't on is flagged; a project whose workspace doesn't load is listed
// from the registry with a note.
func TestMachineFlagsDetached(t *testing.T) {
	_, mini, _ := twoProjects(t)
	code, out, errs := runWith(t, machineHost("railswitch_machine", "filial_proxy"), "machine")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errs)
	}
	line := func(sub string) string {
		for l := range strings.Lines(out) {
			if strings.Contains(l, sub) && strings.Contains(l, "attached") {
				return l
			}
		}
		t.Fatalf("no network line for %s in\n%s", sub, out)
		return ""
	}
	if l := line("mini_proxy"); !strings.Contains(l, "not attached: railswitch up") {
		t.Errorf("mini_proxy: %q", l)
	}
	if l := line("filial_proxy"); strings.Contains(l, "not attached") {
		t.Errorf("filial_proxy: %q", l)
	}
	for _, want := range []string{
		"Traefik reaches railswitch_machine, filial_proxy; mini_proxy not attached: railswitch up\n",
		"filial_proxy: Traefik, rails",
		"railswitch_machine: Traefik, Dozzle\n",
		"mini_proxy: rails (Traefik not attached: railswitch up)\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, reachesAll) {
		t.Errorf("claims %q with mini_proxy detached:\n%s", reachesAll, out)
	}
	r := machineJSON(t, machineHost("railswitch_machine", "filial_proxy"))
	for _, n := range r.Reach.Networks {
		if n.Network == "mini_proxy" && !slices.Equal(n.Members, []string{"rails"}) {
			t.Errorf("mini_proxy members %v", n.Members)
		}
	}

	// A project.toml above the removed one must not be loaded in its place.
	if err := os.Rename(filepath.Join(mini, "project.toml"), filepath.Join(filepath.Dir(mini), "project.toml")); err != nil {
		t.Fatal(err)
	}
	r = machineJSON(t, machineHost("railswitch_machine", "filial_proxy"))
	p := r.Projects[1]
	if p.Prefix != "mini" || p.Note != "workspace not loaded: no project.toml in "+mini || len(p.Worktrees) != 1 || p.Worktrees[0].Slug != "master" || p.Subnet == "" {
		t.Errorf("unloadable mini: %+v", p)
	}
	for _, n := range r.Reach.Networks {
		if strings.HasPrefix(n.Network, "mini") {
			t.Errorf("reach lists %s without a model", n.Network)
		}
	}
	if attached := r.Machine.TraefikNetworks; !slices.ContainsFunc(attached, func(n traefikNetwork) bool { return n.Network == "mini_proxy" && !n.Attached }) {
		t.Errorf("traefik networks %+v", attached)
	}
}

// TestMachineTraefikStopped: a stopped Traefik container's configured
// networks don't count as attached.
func TestMachineTraefikStopped(t *testing.T) {
	twoProjects(t)
	f := machineHost("railswitch_machine", "filial_proxy", "mini_proxy")
	f.On([]string{"podman", "container", "inspect", "--format", "{{.State.Running}}", "railswitch-traefik"}, sys.Result{Stdout: []byte("false\n")}, nil)
	r := machineJSON(t, f)
	for _, n := range r.Machine.TraefikNetworks {
		if n.Attached {
			t.Errorf("%s attached with Traefik stopped", n.Network)
		}
	}
	for _, n := range r.Reach.Networks {
		if slices.Contains(n.Members, "Traefik") {
			t.Errorf("reach %s: %v", n.Network, n.Members)
		}
	}
	if want := "Traefik reaches no network; railswitch_machine, filial_proxy, mini_proxy not attached: railswitch up"; r.Reach.Notes[0] != want {
		t.Errorf("note %q", r.Reach.Notes[0])
	}
}
