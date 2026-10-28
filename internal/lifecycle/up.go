package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/podman"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/unithash"
	"github.com/jbigler/railswitch/internal/workspace"
)

// traefikUnit is the shared proxy's unit file and service.
const (
	traefikUnit      = machine.ProxyUnitName
	traefikService   = machine.ProxyService
	traefikContainer = machine.ProxyContainer
	dozzleUnit       = machine.DozzleUnitName
	dozzleService    = machine.DozzleService
	dozzleContainer  = machine.DozzleContainer
)

// L runs lifecycle commands for one workspace.
type L struct {
	R        sys.Runner
	WS       *workspace.Workspace
	Out, Err io.Writer
	In       io.Reader
	TTY      bool

	// LookPath finds a host program (exec.LookPath; nil: none is found):
	// railswitch nvim attaches the host's nvim when there is one (C4).
	LookPath func(string) (string, error)
	// ConfigDir is railswitch's config dir (machine.Dirs.Config), the
	// parent of the user's hooks/ (nvim-attach, nvim-detach).
	ConfigDir string

	// TraefikAPI is the base URL of Traefik's API, asked with Host
	// traefik.localhost ("": the shared proxy's, 127.0.0.1:80); RoutePoll
	// and RouteTimeout pace up's wait for the app's route (zero: 500ms,
	// 30s).
	TraefikAPI              string
	RoutePoll, RouteTimeout time.Duration
	// SpinEvery paces the activity spinner on a terminal (zero: 100ms).
	SpinEvery time.Duration
	// NvimPoll and NvimTimeout pace the wait for the nvim server to answer
	// (zero: 500ms, 2m).
	NvimPoll, NvimTimeout time.Duration
}

func (l *L) podman() podman.Podman   { return podman.Podman{R: l.R} }
func (l *L) systemd() podman.Systemd { return podman.Systemd{R: l.R} }

// model builds the workspace's model from the machine registry.
func (l *L) model(ctx context.Context) (*render.Model, error) {
	host, err := workspace.ReadHost(ctx, l.R)
	if err != nil {
		return nil, err
	}
	return l.modelFor(host)
}

// modelFor builds the workspace's model from the machine registry with
// host as given.
func (l *L) modelFor(host render.Host) (*render.Model, error) {
	reg, err := machine.Open(l.WS.Dirs.Data)
	if err != nil {
		return nil, err
	}
	defer reg.Close()
	return l.WS.Model(reg.Registry, host)
}

// instance is worktree slug's instance of m.
func instance(m *render.Model, slug string) (*render.Instance, error) {
	i := slices.IndexFunc(m.Inputs.Worktrees, func(w render.Worktree) bool { return w.Slug == slug })
	if i < 0 {
		return nil, fmt.Errorf("worktree %s is not registered for project %s", slug, m.Prefix)
	}
	return m.Instance(m.Inputs.Worktrees[i])
}

// load is the model and worktree slug's instance.
func (l *L) load(ctx context.Context, slug string) (*render.Model, *render.Instance, error) {
	m, err := l.model(ctx)
	if err != nil {
		return nil, nil, err
	}
	in, err := instance(m, slug)
	if err != nil {
		return nil, nil, err
	}
	return m, in, nil
}

func (l *L) lockPath() string { return filepath.Join(l.WS.Root, "stack.lock") }

// changes accumulates the unit changes of every render of one Up.
type changes struct{ units bool }

func (c *changes) add(d *render.Diff) {
	for _, n := range slices.Concat(d.Added, d.Changed, d.Removed) {
		if !strings.Contains(n, "/") { // units are bare filenames, workspace files relative paths
			c.units = true
		}
	}
}

// render renders the workspace (units written), printing its warnings.
func (l *L) render(ctx context.Context) (*render.Diff, error) {
	d, err := l.WS.Render(ctx, l.R, false)
	if d != nil {
		for _, w := range d.Warnings {
			fmt.Fprintf(l.Err, "warning: %s\n", w)
		}
	}
	return d, err
}

// Up renders, checks, pulls, builds and seeds what worktree slug needs, then
// starts its app unit and waits for Traefik to route the app. It stops at
// the first error; nothing is started after a failure, and running it
// again resumes.
func (l *L) Up(ctx context.Context, slug string) error {
	if err := l.checkProxySubnet(ctx); err != nil {
		return err
	}
	l.warnNotIsolated(ctx)
	l.warnGitGuard(ctx)
	var ch changes
	d, err := l.render(ctx)
	if err != nil {
		return err
	}
	ch.add(d)
	m, in, err := l.load(ctx, slug)
	if err != nil {
		return err
	}
	if err := l.Preflight(ctx, m, slug); err != nil {
		return err
	}

	pulled, err := l.pullServiceImages(ctx, m, in)
	if err != nil {
		return err
	}
	if pulled {
		if d, err = l.render(ctx); err != nil {
			return err
		}
		ch.add(d)
		if m, in, err = l.load(ctx, slug); err != nil {
			return err
		}
	}

	_, wrote, err := l.pinBases(ctx, in, false)
	if err != nil {
		return err
	}
	if wrote { // a new FROM digest gives new build tags (D36)
		if d, err = l.render(ctx); err != nil {
			return err
		}
		ch.add(d)
		if m, in, err = l.load(ctx, slug); err != nil {
			return err
		}
	}
	if err := l.build(ctx, in.Builds, func(int) bool { return false }); err != nil {
		return err
	}
	if err := l.seedVolumes(ctx, m, in); err != nil {
		return err
	}

	sd := l.systemd()
	names, err := m.NamesFor(slug)
	if err != nil {
		return err
	}
	if sd.Timeout, err = l.startTimeout(names.Containers); err != nil {
		return err
	}
	// What systemd has loaded, read before any reload: a unit file changed
	// by an earlier render (railswitch render, wt add, a failed up) is
	// not loaded yet or loaded stale (NeedDaemonReload). Whether a running
	// container is stale is told by its unit-hash label (unithash), not by
	// NeedDaemonReload, which any other daemon-reload clears. Traefik's
	// label ignores its Network= lines: a project added or removed is
	// joined or left live (AttachTraefik), never by a restart. The
	// dashboard is no container: it is stale when its unit file is newer
	// than its start (staleDashboard).
	reload := ch.units
	var restartTraefik, restartDozzle, restartDashboard bool
	var restart []string // active units of this worktree running an older unit
	key := unithash.Key(m.Prefix)
	app := unitOf(m, slug, m.Services[0].ID)
	var appActive bool
	for _, u := range append(names.Units, traefikService, dozzleService, machine.DashboardService) {
		st, err := sd.UnitState(ctx, u)
		if err != nil && u == machine.DashboardService { // never blocks a worktree's up
			l.dashboardWarning(err)
			continue
		}
		if err != nil {
			return err
		}
		if st.Load != "loaded" || st.NeedReload {
			reload = true
		}
		if st.Active != "active" {
			continue
		}
		appActive = appActive || u == app
		ctr := strings.TrimSuffix(u, ".service")
		switch {
		case u == traefikService:
			stale, err := l.staleContainer(ctx, traefikContainer, traefikUnit, unithash.Key("railswitch"))
			if err != nil {
				return err
			}
			restartTraefik = restartTraefik || stale
		case u == dozzleService:
			stale, err := l.staleContainer(ctx, dozzleContainer, dozzleUnit, unithash.Key("railswitch"))
			if err != nil {
				return err
			}
			restartDozzle = stale
		case u == machine.DashboardService:
			restartDashboard = l.staleDashboard(ctx, sd)
		case slices.Contains(names.Containers, ctr):
			stale, err := l.staleContainer(ctx, ctr, ctr+".container", key)
			if err != nil {
				return err
			}
			if stale {
				restart = append(restart, u)
			}
		}
	}
	if reload {
		if err := sd.DaemonReload(ctx); err != nil {
			return err
		}
	}
	if restartTraefik {
		if err := sd.Restart(ctx, traefikService); err != nil {
			return err
		}
	}
	if restartDozzle {
		if err := sd.Restart(ctx, dozzleService); err != nil {
			return err
		}
	}
	if restartDashboard {
		if err := sd.Restart(ctx, machine.DashboardService); err != nil {
			l.dashboardWarning(err)
		}
	}
	if len(restart) > 0 {
		done := l.busy("restarting "+strings.Join(restart, ", "), "restarted "+strings.Join(restart, ", "))
		err := sd.Restart(ctx, restart...)
		done(err)
		if err != nil {
			return err
		}
	}
	// The app also needs the project's proxy network and Traefik. The
	// network is started first so a running Traefik can join it live.
	proxyNet := strings.TrimSuffix(naming.ProxyNetworkUnit(m.Prefix), ".network") + "-network.service"
	failed := append(slices.Clone(names.Units), proxyNet, traefikService)
	if err := sd.Start(ctx, proxyNet); err != nil {
		return l.startFailure(ctx, sd, slug, failed, err)
	}
	if err := l.AttachTraefik(ctx); err != nil {
		return err
	}
	// The app first, then every other long-running service by name: a
	// service added to a running stack (a new tool, an option) would not
	// start through the app's Wants=, because starting an active unit
	// starts none of its new dependencies. Oneshots (deps) stay behind the
	// app's Requires=: started by name, one would rerun on every up. The
	// others start in a second call, so one that fails (a tool's port taken)
	// leaves the app up with a warning, as its Wants= would (D23).
	//
	// A start job still pulls in its unit's Requires= when the unit is
	// already active, and deps (a oneshot) is inactive once it has run: on
	// a running stack, starting the app or the worker would run deps again
	// under the live app (a dev server that watches its build then refuses
	// to reload). So with the app active, every service starts by name
	// without its requirements, and deps runs only on an app (re)start.
	start := sd.Start
	if appActive {
		start = sd.StartOnly
	}
	var others []string
	for _, s := range m.Services[1:] {
		if s.Role != render.RoleAgent && !s.Oneshot {
			others = append(others, unitOf(m, slug, s.ID))
		}
	}
	done := l.busy("starting "+slug+": "+strings.Join(startedServices(m, appActive), ", "), "started "+slug)
	err = start(ctx, app)
	var othersErr error
	if err == nil && len(others) > 0 {
		othersErr = start(ctx, others...)
	}
	done(err)
	if err != nil {
		return l.startFailure(ctx, sd, slug, failed, err)
	}
	if othersErr != nil {
		l.warnFailed(ctx, sd, others, othersErr)
	}
	return l.waitRoute(ctx, m, in, slug)
}

// startedServices are the service IDs up starts, the app last: every
// service but agents (D62), and deps only when the app is not already
// active (deps runs through the app's Requires=).
func startedServices(m *render.Model, appActive bool) []string {
	var ids []string
	for _, s := range m.Services[1:] {
		if s.Role != render.RoleAgent && !(appActive && s.Oneshot) {
			ids = append(ids, s.ID)
		}
	}
	return append(ids, m.Services[0].ID)
}

// warnFailed reports services that did not start while the app did: up
// goes on, the app is usable without them. A unit counts as failed as in
// startFailure (a Restart=on-failure one is activating, Result exit-code).
func (l *L) warnFailed(ctx context.Context, sd podman.Systemd, units []string, startErr error) {
	var failed, hints []string
	for _, u := range units {
		st, err := sd.Show(ctx, u, "ActiveState", "Result")
		if err != nil || (st["ActiveState"] != "failed" && (st["Result"] == "" || st["Result"] == "success")) {
			continue
		}
		failed = append(failed, u)
		hints = append(hints, "journalctl --user -u "+u)
	}
	if len(failed) == 0 {
		failed = units
	}
	fmt.Fprintf(l.Err, "warning: %s did not start: %v", strings.Join(failed, ", "), startErr)
	if len(hints) > 0 {
		fmt.Fprintf(l.Err, "; %s", strings.Join(hints, "; "))
	}
	fmt.Fprintln(l.Err)
}

// startFailure turns a failed `systemctl start` into an error naming the
// units (the worktree's, the proxy network's, Traefik's) that failed, after
// printing each one's journal to Err.
// systemctl itself only says a dependency failed and points at
// `journalctl -xe`, the system journal, which holds no user units. The
// unit journal is the only record left: a failed container ran with --rm.
// Its output is there because units log to journald; podman's own event
// lines (image pull, container create with every label) are left out.
func (l *L) startFailure(ctx context.Context, sd podman.Systemd, slug string, units []string, startErr error) error {
	var failed, hints []string
	for _, u := range units {
		st, err := sd.Show(ctx, u, "ActiveState", "Result")
		if err != nil || (st["ActiveState"] != "failed" && (st["Result"] == "" || st["Result"] == "success")) {
			continue
		}
		failed = append(failed, fmt.Sprintf("%s (%s)", u, st["Result"]))
		hints = append(hints, "journalctl --user -u "+u)
		fmt.Fprintf(l.Err, "--- %s: %s/%s, last log lines:\n", u, st["ActiveState"], st["Result"])
		// Best effort: the report must not hide the start error.
		res, err := l.R.Run(ctx, sys.Cmd{Name: "journalctl", Args: []string{"--user", "-u", u, "-b", "-n", "400", "--no-pager", "-o", "json", "--all"}})
		if err == nil {
			for _, line := range unitLogLines(res.Stdout, 40) {
				fmt.Fprintln(l.Err, line)
			}
		}
	}
	if len(failed) == 0 {
		return fmt.Errorf("%w\nno unit of worktree %s reports a failure; see journalctl --user -b -n 100", startErr, slug)
	}
	return fmt.Errorf("%w\nfailed: %s\nmore: %s", startErr, strings.Join(failed, ", "), strings.Join(hints, "; "))
}

// unitLogLines are the last n messages of journalctl -o json output,
// without podman's own entries (SYSLOG_IDENTIFIER podman: its events). A
// MESSAGE journald stored as bytes (not valid UTF-8) is decoded as such.
func unitLogLines(out []byte, n int) []string {
	var lines []string
	for raw := range bytes.Lines(out) {
		var e struct {
			ID  string          `json:"SYSLOG_IDENTIFIER"`
			Msg json.RawMessage `json:"MESSAGE"`
		}
		if json.Unmarshal(raw, &e) != nil || e.ID == "podman" {
			continue
		}
		if len(e.Msg) == 0 || string(e.Msg) == "null" {
			continue
		}
		var s string
		if json.Unmarshal(e.Msg, &s) != nil {
			var b []byte
			var ints []int
			if json.Unmarshal(e.Msg, &ints) != nil {
				continue
			}
			for _, c := range ints {
				b = append(b, byte(c))
			}
			s = string(b)
		}
		lines = append(lines, strings.TrimRight(s, "\n"))
	}
	return lines[max(0, len(lines)-n):]
}

// staleDashboard reports whether the running dashboard's unit file (render
// rewrites it for a new railswitch path) is newer than the dashboard's
// start (podman.Systemd.ActiveSince, exact to the microsecond). A missing
// file is not stale. The dashboard never blocks a worktree's up: an error
// is a warning (dashboardWarning) and the unit counts as not stale.
func (l *L) staleDashboard(ctx context.Context, sd podman.Systemd) bool {
	fi, err := os.Stat(filepath.Join(l.WS.Dirs.SystemdUser, machine.DashboardService))
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	var started time.Time
	if err == nil {
		started, err = sd.ActiveSince(ctx, machine.DashboardService)
	}
	if err != nil {
		l.dashboardWarning(err)
		return false
	}
	return fi.ModTime().After(started)
}

// dashboardWarning reports a dashboard failure that up goes on past.
func (l *L) dashboardWarning(err error) {
	fmt.Fprintf(l.Err, "warning: %s: %v\n", machine.DashboardService, err)
}

// staleContainer reports whether container ctr was created from another
// unit than the rendered unitFile: its key label differs from the file's.
func (l *L) staleContainer(ctx context.Context, ctr, unitFile, key string) (bool, error) {
	want, err := l.renderedHash(unitFile, key)
	if err != nil {
		return false, err
	}
	have, err := l.podman().Label(ctx, ctr, key)
	if err != nil {
		return false, err
	}
	return have != want, nil
}

// renderedHash is the key label stamped in the rendered unitFile.
func (l *L) renderedHash(unitFile, key string) (string, error) {
	data, err := os.ReadFile(filepath.Join(l.WS.Dirs.Quadlet, unitFile))
	if err != nil {
		return "", err
	}
	return unithash.Read(data, key), nil
}

// defaultStartSec is systemd's DefaultTimeoutStartSec, for a unit that
// sets no TimeoutStartSec.
const defaultStartSec = 90

// startTimeout bounds starting containers' units: the sum of their
// TimeoutStartSec (starting the app waits for deps and the data services,
// a chain at worst) plus two minutes, never below podman.StartTimeout.
func (l *L) startTimeout(containers []string) (time.Duration, error) {
	total := 0
	for _, c := range containers {
		data, err := os.ReadFile(filepath.Join(l.WS.Dirs.Quadlet, c+".container"))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return 0, err
		}
		n := defaultStartSec
		for line := range strings.Lines(string(data)) {
			if v, ok := strings.CutPrefix(strings.TrimRight(line, "\n"), "TimeoutStartSec="); ok {
				if n, err = strconv.Atoi(v); err != nil {
					return 0, fmt.Errorf("%s.container: TimeoutStartSec=%q: %w", c, v, err)
				}
			}
		}
		total += n
	}
	return max(time.Duration(total)*time.Second+2*time.Minute, podman.StartTimeout), nil
}

func unitOf(m *render.Model, slug, svc string) string {
	return containerOf(m, slug, svc) + ".service"
}

// pullServiceImages pulls every registry image of the worktree that
// stack.lock has no digest for and records the digest (D15).
func (l *L) pullServiceImages(ctx context.Context, m *render.Model, in *render.Instance) (bool, error) {
	var refs []string
	for _, s := range m.Services {
		if s.Image.Kind != render.ImagePull {
			continue
		}
		ref, err := pack.Expand(s.Image.Ref, in.Scope(s.ID))
		if err != nil {
			return false, fmt.Errorf("%s image: %w", s.ID, err)
		}
		if l.WS.Lock.Digest(ref) == "" && !slices.Contains(refs, ref) {
			refs = append(refs, ref)
		}
	}
	for _, ref := range refs {
		fmt.Fprintf(l.Out, "pulling %s\n", ref)
		dg, err := l.podman().Pull(ctx, ref)
		if err != nil {
			return false, err
		}
		if dg == "" {
			return false, fmt.Errorf("podman reported no digest for %s", ref)
		}
		l.WS.Lock.SetDigest(ref, dg)
		if err := l.WS.Lock.Write(l.lockPath()); err != nil {
			return false, err
		}
	}
	return len(refs) > 0, nil
}

// fromRefs are the distinct FROM refs of steps, in build order.
func fromRefs(steps []render.BuildStep) []string {
	var refs []string
	for _, s := range steps {
		for _, r := range s.From {
			if !slices.Contains(refs, r) {
				refs = append(refs, r)
			}
		}
	}
	return refs
}

// repo is ref without its tag: docker.io/library/ruby:3.3.6-slim →
// docker.io/library/ruby.
func repo(ref string) string {
	if i := strings.LastIndexByte(ref, ':'); i > strings.LastIndexByte(ref, '/') {
		return ref[:i]
	}
	return ref
}

// pinBases makes every FROM image of in's builds the one stack.lock records
// (D36): a ref with no digest is pulled and recorded; one whose local image
// is missing or differs is pulled by digest and tagged. With repull, every
// ref is pulled by tag and the new digest recorded. A ref already pinned by
// digest (repo@sha256:…) is its own pin: it is pulled as is when missing,
// never re-tagged, and nothing is recorded for it. It returns the refs
// whose recorded digest changed, and whether it wrote stack.lock (the
// build tags then change: re-render and reload).
func (l *L) pinBases(ctx context.Context, in *render.Instance, repull bool) (changed []string, wrote bool, err error) {
	p, lock := l.podman(), l.WS.Lock
	for _, ref := range fromRefs(in.Builds) {
		if strings.Contains(ref, "@") {
			ok, err := p.ImageExists(ctx, ref)
			if err != nil {
				return nil, wrote, err
			}
			if !ok {
				fmt.Fprintf(l.Out, "pulling %s\n", ref)
				if err := p.PullOnly(ctx, ref); err != nil {
					return nil, wrote, err
				}
			}
			continue
		}
		old := lock.BaseDigest(ref)
		if old == "" || repull {
			fmt.Fprintf(l.Out, "pulling %s\n", ref)
			dg, err := p.Pull(ctx, ref)
			if err != nil {
				return nil, wrote, err
			}
			if dg == "" {
				return nil, wrote, fmt.Errorf("podman reported no digest for %s", ref)
			}
			if dg == old {
				continue
			}
			lock.SetBaseDigest(ref, dg)
			if err := lock.Write(l.lockPath()); err != nil {
				return nil, wrote, err
			}
			wrote = true
			switch {
			case old != "":
				fmt.Fprintf(l.Out, "%s: %s → %s\n", ref, old, dg)
				changed = append(changed, ref)
			case repull:
				fmt.Fprintf(l.Out, "%s: (none) → %s\n", ref, dg)
			}
			continue
		}
		local, err := p.ImageDigest(ctx, ref)
		if err != nil {
			return nil, wrote, err
		}
		if local == old {
			continue
		}
		pinned := repo(ref) + "@" + old
		fmt.Fprintf(l.Out, "pulling %s\n", pinned)
		if err := p.PullOnly(ctx, pinned); err != nil {
			return nil, wrote, err
		}
		if err := p.Tag(ctx, pinned, ref); err != nil {
			return nil, wrote, err
		}
	}
	return changed, wrote, nil
}

// build runs each step, in order, whose tag is missing or for which force
// reports true. Build uses --pull=never: the FROM images are the pinned ones.
func (l *L) build(ctx context.Context, steps []render.BuildStep, force func(i int) bool) error {
	p := l.podman()
	for i, s := range steps {
		if !force(i) {
			ok, err := p.ImageExists(ctx, s.Tag)
			if err != nil {
				return err
			}
			if ok {
				continue
			}
		}
		fmt.Fprintf(l.Out, "building %s (%s)\n", s.Name, s.Tag)
		if err := p.Build(ctx, podman.BuildArgs{File: s.File, Tag: s.Tag, Context: s.Context, Args: s.Args, Progress: l.Out}); err != nil {
			return fmt.Errorf("build %s: %w", s.Name, err)
		}
	}
	return nil
}

// seedVolumes creates every missing volume with a seed and imports the
// seed from <ws>/db-dumps/ when it is there (D18).
func (l *L) seedVolumes(ctx context.Context, m *render.Model, in *render.Instance) error {
	p := l.podman()
	for _, s := range m.Services {
		for _, v := range s.Volumes {
			if v.Seed == "" {
				continue
			}
			name, err := in.VolumeName(s.ID, v)
			if err != nil {
				return err
			}
			ok, err := p.VolumeExists(ctx, name)
			if err != nil {
				return err
			}
			if ok {
				continue
			}
			seed := filepath.Join(m.Inputs.Workspace, "db-dumps", v.Seed)
			fi, err := os.Stat(seed)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				seed = ""
			case err != nil:
				return err
			case !fi.Mode().IsRegular():
				return fmt.Errorf("seed %s: not a regular file", seed)
			}
			if err := p.VolumeCreate(ctx, name); err != nil {
				return err
			}
			if seed == "" {
				continue
			}
			if err := p.VolumeImport(ctx, name, seed); err != nil {
				// An unseeded volume would never be seeded: the next up re-creates it.
				// Ctrl-C cancelled ctx; the cleanup still runs, bounded by its own timeout.
				if rmErr := p.RemoveVolume(context.WithoutCancel(ctx), name); rmErr != nil {
					return errors.Join(err, fmt.Errorf("removing unseeded volume %s: %w", name, rmErr))
				}
				return err
			}
		}
	}
	return nil
}
