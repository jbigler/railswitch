package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/podman"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/workspace"
)

// machineReport is `railswitch machine` and its --json (docs/json.md).
type machineReport struct {
	Projects []machineProject `json:"projects"`
	Routes   []machineRoute   `json:"routes"`
	Machine  machineState     `json:"machine"`
	Reach    machineReach     `json:"reach"`
}

type machineProject struct {
	Prefix    string            `json:"prefix"`
	Workspace string            `json:"workspace"`
	Subnet    string            `json:"subnet"`
	TraefikIP string            `json:"traefik_ip"`
	Worktrees []machineWorktree `json:"worktrees"`
	Note      string            `json:"note"` // why the workspace's model didn't load; "" when it did
}

type machineWorktree struct {
	Slug      string         `json:"slug"`
	ID        int            `json:"id"`
	Ports     map[string]int `json:"ports"`      // port name → host port (base + ID)
	SharedURL string         `json:"shared_url"` // "" when not shared
}

type machineRoute struct {
	Host     string `json:"host"`
	Project  string `json:"project"`
	Worktree string `json:"worktree"`
	Service  string `json:"service"`
	Router   string `json:"router"`
}

type machineState struct {
	Traefik         unitState        `json:"traefik"`
	Dozzle          unitState        `json:"dozzle"`
	TraefikNetworks []traefikNetwork `json:"traefik_networks"`
}

type unitState struct {
	Unit  string `json:"unit"`
	State string `json:"state"` // "<ActiveState>/<SubState>"
}

type traefikNetwork struct {
	Network  string `json:"network"`
	Project  string `json:"project"` // "" for the machine network
	Attached bool   `json:"attached"`
}

type machineReach struct {
	Networks []reachNetwork  `json:"networks"`
	Notes    []string        `json:"notes"`
	Isolated map[string]bool `json:"isolated"` // network → isolate=strict, for each railswitch network that exists
}

type reachNetwork struct {
	Network string   `json:"network"`
	Members []string `json:"members"`
}

// reachesAll is the reach note when Traefik is on every network it must be
// on; reachRefused the note on cross-project traffic when every railswitch
// network that exists is isolate=strict, reachNoNetworks when none exists
// (reachNote).
const (
	reachesAll      = "Traefik reaches every project network"
	reachRefused    = "a project's containers reach another project only through Traefik, which refuses them (D50); networks are isolate=strict, and the Traefik API and Dozzle answer only host clients"
	reachNoNetworks = "no railswitch network exists yet: railswitch up creates them isolate=strict (D50)"
)

// traefikReach is the reach note on Traefik: reachesAll when it is on every
// network of nets, else the networks it is on and those it isn't.
func traefikReach(nets []traefikNetwork) string {
	var on, off []string
	for _, n := range nets {
		if n.Attached {
			on = append(on, n.Network)
		} else {
			off = append(off, n.Network)
		}
	}
	if len(off) == 0 {
		return reachesAll
	}
	reaches := "no network"
	if len(on) > 0 {
		reaches = strings.Join(on, ", ")
	}
	return "Traefik reaches " + reaches + "; " + strings.Join(off, ", ") + " not attached: railswitch up"
}

// reachNote is reachNoNetworks when no railswitch network of iso exists,
// reachRefused when every one that exists has isolate=strict, else the
// networks that don't and how to fix them.
func reachNote(nets []string, iso map[string]bool) string {
	var not []string
	exist := false
	for _, n := range nets {
		ok, exists := iso[n]
		exist = exist || exists
		if exists && !ok {
			not = append(not, n)
		}
	}
	switch {
	case !exist:
		return reachNoNetworks
	case len(not) == 0:
		return reachRefused
	}
	return "networks not isolate=strict (created before Stage 3): " + strings.Join(not, ", ") + "; other projects' containers reach them directly, not only through Traefik; " + machine.RecreateNetworks
}

// railswitch machine: every registered project with its worktrees, ports
// and routes; the machine units and Traefik's networks; who reaches whom.
// It reads the registry without locking or creating it.
func cmdMachine(ctx context.Context, e *env, args []string) int {
	fs := e.flags("machine")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("machine takes no arguments")
	}
	r, err := e.machineReport(ctx)
	if err != nil {
		return e.fail(err)
	}
	if e.json {
		err = json.NewEncoder(e.stdout).Encode(r)
	} else {
		err = r.print(e.stdout)
	}
	if err != nil {
		return e.fail(err)
	}
	return 0
}

func (e *env) machineReport(ctx context.Context) (*machineReport, error) {
	dirs, err := machine.DefaultDirs()
	if err != nil {
		return nil, err
	}
	reg, err := machine.Read(dirs.Data)
	if errors.Is(err, fs.ErrNotExist) {
		reg, err = &machine.Registry{Schema: machine.SchemaVersion}, nil // nothing registered yet
	}
	if err != nil {
		return nil, err
	}
	host, err := workspace.ReadHost(ctx, e.run)
	if err != nil {
		return nil, err
	}
	// A stopped container still lists its configured networks: only a
	// running Traefik's count.
	pm := podman.Podman{R: e.run}
	running, err := pm.Running(ctx, machine.ProxyContainer)
	if err != nil {
		return nil, err
	}
	var attached []string
	if running {
		if attached, err = pm.Networks(ctx, machine.ProxyContainer); err != nil {
			return nil, err
		}
	}
	r := &machineReport{Projects: []machineProject{}, Routes: []machineRoute{},
		Reach: machineReach{Networks: []reachNetwork{{Network: machine.MachineNetwork, Members: withTraefik(attached, machine.MachineNetwork, "Dozzle")}}}}
	r.Machine.TraefikNetworks = []traefikNetwork{{Network: machine.MachineNetwork, Attached: slices.Contains(attached, machine.MachineNetwork)}}

	ps := slices.Clone(reg.Projects)
	slices.SortFunc(ps, func(a, b machine.ProjectEntry) int { return cmp.Compare(a.Prefix, b.Prefix) })
	for _, pe := range ps {
		proxy := naming.ProxyNetwork(pe.Prefix)
		r.Machine.TraefikNetworks = append(r.Machine.TraefikNetworks, traefikNetwork{Network: proxy, Project: pe.Prefix, Attached: slices.Contains(attached, proxy)})
		m, err := projectModel(ctx, reg, pe, host)
		p := machineProject{Prefix: pe.Prefix, Workspace: pe.Workspace, Subnet: machine.Subnet(pe.Subnet), TraefikIP: machine.TraefikIP(pe.Subnet), Worktrees: []machineWorktree{}}
		if err != nil {
			p.Note = err.Error()
		}
		if err := r.addProject(&p, reg.WorktreesOf(pe.Prefix), pe, m, attached); err != nil {
			return nil, err
		}
		r.Projects = append(r.Projects, p)
	}
	slices.SortFunc(r.Routes, func(a, b machineRoute) int { return cmp.Compare(a.Host, b.Host) })
	nets := reg.Networks("")
	if r.Reach.Isolated, err = pm.Isolation(ctx, nets); err != nil {
		return nil, err
	}
	r.Reach.Notes = []string{traefikReach(r.Machine.TraefikNetworks), reachNote(nets, r.Reach.Isolated)}

	sd := podman.Systemd{R: e.run}
	for _, u := range []struct {
		dst  *unitState
		unit string
	}{{&r.Machine.Traefik, machine.ProxyService}, {&r.Machine.Dozzle, machine.DozzleService}} {
		props, err := sd.Show(ctx, u.unit, "ActiveState", "SubState")
		if err != nil {
			return nil, err
		}
		*u.dst = unitState{Unit: u.unit, State: props["ActiveState"] + "/" + props["SubState"]}
	}
	return r, nil
}

// projectModel loads pe's workspace and builds its model; the error says
// why it can't, for the project's note.
func projectModel(ctx context.Context, reg *machine.Registry, pe machine.ProjectEntry, host render.Host) (*render.Model, error) {
	// Load walks up from its start dir: a removed workspace must not reach
	// a project.toml in a parent.
	if fi, err := os.Stat(filepath.Join(pe.Workspace, "project.toml")); err != nil || !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("workspace not loaded: no project.toml in %s", pe.Workspace)
	}
	w, err := workspace.Load(ctx, pe.Workspace, "")
	if err != nil {
		return nil, fmt.Errorf("workspace not loaded: %w", err)
	}
	m, err := w.Model(reg, host)
	if err != nil {
		return nil, fmt.Errorf("model not built: %w", err)
	}
	return m, nil
}

// addProject fills p's worktrees from the registry and, with the model m
// (nil when it didn't load), r's routes and reach rows. Ports come from
// m, else from the registry's recorded bases. attached are the networks
// Traefik is on.
func (r *machineReport) addProject(p *machineProject, wts []machine.WorktreeEntry, pe machine.ProjectEntry, m *render.Model, attached []string) error {
	bases := pe.Ports
	if m != nil {
		bases = m.PortBases()
		var proxy []string
		for _, s := range m.Services {
			if slices.Contains(s.Networks, "proxy") {
				proxy = append(proxy, s.ID)
			}
		}
		net := naming.ProxyNetwork(pe.Prefix)
		r.Reach.Networks = append(r.Reach.Networks, reachNetwork{Network: net, Members: withTraefik(attached, net, proxy...)})
	}
	for _, wt := range wts {
		w := machineWorktree{Slug: wt.Slug, ID: wt.ID, Ports: map[string]int{}}
		for name, base := range bases {
			w.Ports[name] = base + wt.ID
		}
		if sh := wt.Share; sh != nil {
			port := 0
			if len(sh.Ports) > 0 {
				port = sh.Ports[0]
			} else if m != nil {
				if sp := m.SharePorts(wt.ID); len(sp) > 0 {
					port = sp[0].Port
				}
			}
			if port != 0 {
				w.SharedURL = "https://" + sh.Host + ":" + strconv.Itoa(port) + "/"
			}
		}
		p.Worktrees = append(p.Worktrees, w)
		if m == nil {
			continue
		}
		dev := reachNetwork{Network: naming.DevNetwork(pe.Prefix, wt.Slug)}
		for _, s := range m.Services {
			dev.Members = append(dev.Members, s.ID)
		}
		r.Reach.Networks = append(r.Reach.Networks, dev)
		if err := r.addRoutes(m, wt, p); err != nil {
			return err
		}
	}
	return nil
}

// withTraefik is members, after Traefik when it is attached to net.
func withTraefik(attached []string, net string, members ...string) []string {
	if slices.Contains(attached, net) {
		return append([]string{"Traefik"}, members...)
	}
	return members
}

// addRoutes adds worktree wt's routes, as render labels them; an instance
// or route that doesn't resolve goes into p's note instead.
func (r *machineReport) addRoutes(m *render.Model, wt machine.WorktreeEntry, p *machineProject) error {
	i := slices.IndexFunc(m.Inputs.Worktrees, func(w render.Worktree) bool { return w.Slug == wt.Slug })
	if i < 0 {
		return fmt.Errorf("worktree %s missing from project %s's model", wt.Slug, m.Prefix)
	}
	in, err := m.Instance(m.Inputs.Worktrees[i])
	var routes []render.RenderedRoute
	if err == nil {
		routes, err = render.Routes(in)
	}
	if err != nil {
		p.Note = strings.TrimPrefix(p.Note+"; ", "; ") + "routes of " + wt.Slug + " not resolved: " + err.Error()
		return nil
	}
	for _, rt := range routes {
		r.Routes = append(r.Routes, machineRoute{Host: rt.Host, Project: m.Prefix, Worktree: wt.Slug, Service: rt.Service, Router: rt.Router})
	}
	return nil
}

func (r *machineReport) print(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "PROJECTS")
	for _, p := range r.Projects {
		// No tab: each project's worktree rows align among themselves.
		fmt.Fprintf(tw, "%s  %s  subnet %s  Traefik %s\n", p.Prefix, p.Workspace, p.Subnet, p.TraefikIP)
		if p.Note != "" {
			fmt.Fprintf(tw, "  note: %s\n", p.Note)
		}
		for _, wt := range p.Worktrees {
			var ports []string
			for _, name := range slices.Sorted(maps.Keys(wt.Ports)) {
				ports = append(ports, name+"="+strconv.Itoa(wt.Ports[name]))
			}
			shared := "-"
			if wt.SharedURL != "" {
				shared = "shared " + wt.SharedURL
			}
			fmt.Fprintf(tw, "  %s\tid %d\t%s\t%s\n", wt.Slug, wt.ID, strings.Join(ports, " "), shared)
		}
	}
	fmt.Fprintln(tw, "\nROUTES")
	for _, rt := range r.Routes {
		fmt.Fprintf(tw, "%s\t%s/%s/%s\t%s\n", rt.Host, rt.Project, rt.Worktree, rt.Service, rt.Router)
	}
	fmt.Fprintln(tw, "\nMACHINE")
	for _, u := range []unitState{r.Machine.Traefik, r.Machine.Dozzle} {
		fmt.Fprintf(tw, "%s\t%s\n", u.Unit, u.State)
	}
	for _, n := range r.Machine.TraefikNetworks {
		state := "attached"
		if !n.Attached {
			state = "not attached: railswitch up"
		}
		fmt.Fprintf(tw, "Traefik on %s\t%s\n", n.Network, state)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(w, "\nREACH")
	for _, n := range r.Reach.Networks {
		mark := ""
		if slices.ContainsFunc(r.Machine.TraefikNetworks, func(t traefikNetwork) bool { return t.Network == n.Network && !t.Attached }) {
			mark = " (Traefik not attached: railswitch up)"
		}
		fmt.Fprintf(w, "%s: %s%s\n", n.Network, strings.Join(n.Members, ", "), mark)
	}
	for _, l := range r.Reach.Notes {
		fmt.Fprintln(w, l)
	}
	return nil
}
