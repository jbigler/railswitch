package lifecycle

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/podman"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/tailscale"
	"github.com/jbigler/railswitch/internal/workspace"
)

// ProjectRemove unregisters project prefix and tears down what railswitch
// made for it, by exact name, after a confirmation that defaults to No (yes
// skips it; without a terminal it refuses). In order: each shared
// worktree's own tailnet ports are turned off; every worktree is stopped
// (Down when the registered workspace loads, else a stop of each container
// unit its marked units name); the project leaves the registry and the
// machine units; Traefik leaves <prefix>_proxy; every network the marked
// units name is removed (no -f, <prefix>_proxy last); the marked unit files
// are deleted and systemd reloaded. purge also removes the "<prefix>_…"
// volumes the units name and <workspace>/.railswitch. The workspace
// directory and its git repo are never deleted. It works with the
// workspace directory gone: the names come from the registry and the unit
// markers.
func ProjectRemove(ctx context.Context, r sys.Runner, dirs machine.Dirs, out io.Writer, in io.Reader, tty bool, prefix string, yes, purge bool) error {
	reg, err := machine.Read(dirs.Data)
	if err != nil {
		return err
	}
	pe, ok := reg.Project(prefix)
	if !ok {
		return fmt.Errorf("project %s is not registered", prefix)
	}
	wts := reg.WorktreesOf(prefix)
	units, err := render.MarkedUnits(dirs.Quadlet, prefix)
	if err != nil {
		return err
	}
	if !yes {
		what := fmt.Sprintf("stops %d worktrees of %s and deletes their containers, per-worktree volumes, units and networks", len(wts), prefix)
		if purge {
			what += ", the shared " + prefix + "_… volumes and, if the workspace still loads as " + prefix + ", " + filepath.Join(pe.Workspace, ".railswitch")
		}
		if !tty {
			return fmt.Errorf("project rm %s: pass --yes (no terminal to confirm on)", what)
		}
		fmt.Fprintf(out, "project rm %s. Continue? [y/N] ", what)
		line, _ := bufio.NewReader(in).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintln(out, "aborted")
			return nil
		}
	}

	l := &L{R: r, WS: &workspace.Workspace{Dirs: dirs}, Out: out, Err: io.Discard, In: in, TTY: tty}
	loaded := false
	if w, err := workspace.Load(ctx, pe.Workspace, ""); err == nil && filepath.Clean(w.Root) == filepath.Clean(pe.Workspace) && w.Project.Project.Prefix == prefix {
		l.WS, loaded = w, true
	} else {
		fmt.Fprintf(out, "workspace %s does not load; using the registry and the unit files\n", pe.Workspace)
	}

	// 1. Each shared worktree's own tailnet ports.
	for _, wt := range wts {
		if wt.Share == nil {
			continue
		}
		if loaded {
			err = l.DropShare(ctx, wt.Slug)
		} else {
			err = unserveBases(ctx, r, pe, wt.ID)
		}
		if err != nil {
			return fmt.Errorf("unsharing %s: %w", wt.Slug, err)
		}
	}

	// 2. Stop every worktree.
	sd := podman.Systemd{R: r}
	if loaded {
		for _, wt := range wts {
			if err := l.Down(ctx, wt.Slug, true); err != nil {
				return fmt.Errorf("down %s: %w", wt.Slug, err)
			}
		}
	} else {
		for _, u := range units {
			if base, ok := strings.CutSuffix(filepath.Base(u.Path), ".container"); ok {
				if err := sd.StopLoaded(ctx, base+".service"); err != nil {
					return err
				}
			}
		}
		// Stopping a Quadlet unit removes its container; then its
		// per-worktree volumes, exact names from the units.
		p := podman.Podman{R: r}
		for _, u := range units {
			for _, v := range u.VolumeNames {
				if strings.HasPrefix(v, prefix+"-") {
					if err := p.RemoveVolume(ctx, v); err != nil {
						return err
					}
				}
			}
		}
	}

	// 3. Unregister; the machine units drop the project's network.
	lr, err := machine.Open(dirs.Data)
	if err != nil {
		return err
	}
	if err := lr.RemoveProject(prefix); err != nil {
		lr.Close()
		return err
	}
	if err := lr.Save(); err != nil {
		lr.Close()
		return err
	}
	lr.Close()
	if err := machine.RefreshProxyUnits(lr.Registry, dirs.Quadlet); err != nil {
		return err
	}

	// 4. Traefik leaves <prefix>_proxy, no longer registered.
	if err := l.AttachTraefik(ctx); err != nil {
		return err
	}

	// 5. Networks, exact names, the proxy network last.
	p := podman.Podman{R: r}
	proxy := naming.ProxyNetwork(prefix)
	var nets, vols []string
	for _, u := range units {
		for _, n := range u.NetworkNames {
			if n != proxy && !slices.Contains(nets, n) {
				nets = append(nets, n)
			}
		}
		for _, v := range u.VolumeNames {
			if strings.HasPrefix(v, prefix+"_") && !slices.Contains(vols, v) {
				vols = append(vols, v)
			}
		}
	}
	if slices.ContainsFunc(units, func(u render.MarkedUnit) bool { return slices.Contains(u.NetworkNames, proxy) }) {
		nets = append(nets, proxy)
	}
	for _, n := range nets {
		if err := p.NetworkRemove(ctx, n); err != nil {
			return err
		}
	}

	// 6–7. The unit files, then systemd forgets them.
	for _, u := range units {
		if err := os.Remove(u.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if err := sd.DaemonReload(ctx); err != nil {
		return err
	}
	// A Quadlet network service stays active (RemainAfterExit) with its file
	// gone, so a re-registered prefix would never recreate the network. Only
	// after the reload: until then Traefik's unit Requires= the proxy one.
	for _, u := range units {
		if base, ok := strings.CutSuffix(filepath.Base(u.Path), ".network"); ok {
			if err := sd.StopLoaded(ctx, base+"-network.service"); err != nil {
				return err
			}
		}
	}

	// 8. --purge: the shared volumes and the workspace's generated files.
	if purge {
		for _, v := range vols {
			ok, err := p.VolumeExists(ctx, v)
			if err != nil {
				return err
			}
			if ok {
				if err := p.VolumeRemove(ctx, v); err != nil {
					return err
				}
			}
		}
		gen := filepath.Join(pe.Workspace, ".railswitch")
		if !loaded {
			fmt.Fprintf(out, "kept %s: the workspace does not load as project %s\n", gen, prefix)
		} else if fi, err := os.Lstat(gen); err == nil && fi.IsDir() {
			if err := os.RemoveAll(gen); err != nil {
				return err
			}
		}
	}
	fmt.Fprintf(out, "removed project %s; its workspace %s and git repo are kept\n", prefix, pe.Workspace)
	return nil
}

// shareBases are the pinned bases of the ports share serves: app and
// storage.
var shareBases = []int{13000, 19000}

// unserveBases turns off, for a shared worktree with ID id of project pe
// whose workspace doesn't load, each share port base+id (a pinned share
// base pe records) that is served to its own loopback port, as Share set it
// up. IDs are unique machine-wide, so such a port is this worktree's.
func unserveBases(ctx context.Context, r sys.Runner, pe machine.ProjectEntry, id int) error {
	ts := tailscale.T{R: r}
	served, err := ts.Served(ctx)
	if err != nil {
		return err
	}
	var ports []int
	for _, b := range pe.Ports {
		if slices.Contains(shareBases, b) && !slices.Contains(ports, b+id) {
			ports = append(ports, b+id)
		}
	}
	slices.Sort(ports)
	for _, port := range ports {
		if t, ok := served[port]; ok && t == tailscale.Target(port) {
			if err := ts.ServeOff(ctx, port); err != nil {
				return err
			}
		}
	}
	return nil
}
