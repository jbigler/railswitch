package render

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/tomlfile"
)

// Plan is everything a render writes.
type Plan struct {
	Units map[string][]byte // unit filename → content, written to the Quadlet dir at mode 0644
	Files map[string]File   // workspace-relative slash path → content and mode (env files, package assets)
	// Homes are the per-worktree home dirs a unit bind-mounts at
	// /home/appuser (D30's tool home, an agent's home), workspace-relative
	// slash paths. Apply creates a missing one 0700 and never removes one:
	// they hold user data.
	Homes []string
	// HomeDirs are the mount points under a home, workspace-relative slash
	// paths: Apply creates them 0755 so the runtime doesn't create them (or
	// their parents) as container root, which appuser can't write.
	HomeDirs []string
	// StatusDirs are the per-worktree agent status dirs (D58), when the
	// project has an agent: workspace-relative slash paths, created and kept
	// like Homes.
	StatusDirs []string
}

// File is one workspace file of a Plan.
type File struct {
	Data []byte
	Mode fs.FileMode
}

// Diff is what Apply changed, or in check mode would change. Units are named
// by filename, workspace files by their workspace-relative path.
type Diff struct {
	Added, Changed, Removed []string
	Text                    string   // unified diff; a private file's contents are never shown
	Warnings                []string // non-fatal: what the caller prints as "warning: <w>"
}

const (
	unitMode = fs.FileMode(0o644)
	envDir   = ".railswitch/env"  // D10: <envDir>/<slug>/<svc>.env, 0600 in 0700 dirs
	homeDir  = ".railswitch/home" // D30: <homeDir>/<slug>, a tool's per-worktree home, 0700
	// agentHomeDir holds <agentHomeDir>/<slug>/<agent>, an agent's
	// per-worktree home (D55), 0700: apart from homeDir, which nvim mounts,
	// so no agent's login is visible to nvim.
	agentHomeDir = ".railswitch/agent-home"
	// statusDir holds <statusDir>/<slug>, a worktree's agent status files
	// (D58), 0700, bind-mounted into its agents.
	statusDir = ".railswitch/status"
	// homeSource is the mount source of a per-worktree home.
	homeSource = "{worktree.home}"
	// agentHomeSource is the parent of an agent's home mount source:
	// <agentHomeSource>/<agent>.
	agentHomeSource = "{agent.home}"
	// appHome is the container home every home dir is mounted at.
	appHome = "/home/appuser"
)

// homeRel is the workspace-relative home dir s bind-mounts at appHome in
// worktree slug, if any.
func homeRel(s *Service, slug string) (string, bool) {
	for _, mt := range s.Mounts {
		if mt.Target != appHome {
			continue
		}
		if mt.Source == homeSource {
			return path.Join(homeDir, slug), true
		}
		if name, ok := strings.CutPrefix(mt.Source, agentHomeSource+"/"); ok {
			return path.Join(agentHomeDir, slug, name), true
		}
	}
	return "", false
}

// homeMountPoints are s's mount points under appHome, relative to it: a
// volume's target, and a bind mount's parent dir (its source may be a
// file, which a dir in its place would break).
func homeMountPoints(s *Service) []string {
	var rels []string
	add := func(target string) {
		if rel, ok := strings.CutPrefix(path.Clean(target), appHome+"/"); ok {
			rels = append(rels, rel)
		}
	}
	for _, mt := range s.Mounts {
		add(path.Dir(mt.Target))
	}
	for _, v := range s.Volumes {
		add(v.Mount)
	}
	return rels
}

// Plan renders the project: its units, every worktree's per-service env file
// (D10) and every package asset.
func (m *Model) Plan() (*Plan, error) {
	units, err := Quadlet(m)
	if err != nil {
		return nil, err
	}
	files := map[string]File{}
	var homes, homeDirs, status []string
	hasAgents := len(m.Agents()) > 0
	for _, wt := range m.Inputs.Worktrees {
		in, err := m.Instance(wt)
		if err != nil {
			return nil, fmt.Errorf("worktree %s: %w", wt.Slug, err)
		}
		for _, s := range m.Services {
			files[path.Join(envDir, wt.Slug, s.ID+".env")] = File{Data: EnvFile(in.Env[s.ID]), Mode: 0o600}
			home, ok := homeRel(&s, wt.Slug)
			if !ok {
				continue
			}
			if !slices.Contains(homes, home) {
				homes = append(homes, home)
			}
			for _, rel := range homeMountPoints(&s) {
				if d := path.Join(home, rel); !slices.Contains(homeDirs, d) {
					homeDirs = append(homeDirs, d)
				}
			}
		}
		if hasAgents {
			status = append(status, path.Join(statusDir, wt.Slug))
		}
	}
	for _, a := range m.Assets {
		files[a.Rel] = File{Data: a.Data, Mode: a.Mode}
	}
	return &Plan{Units: units, Files: files, Homes: homes, HomeDirs: homeDirs, StatusDirs: status}, nil
}

// EnvFile is a podman env file: NAME=value per line, the value verbatim
// (expandEnv refuses a value with a line break).
func EnvFile(env []EnvVar) []byte {
	b := []byte{} // an env file with no var is still written: its unit names it
	for _, e := range env {
		b = append(b, e.Name...)
		b = append(b, '=')
		b = append(b, e.Value...)
		b = append(b, '\n')
	}
	return b
}

// target is one file a Plan puts on disk.
type target struct {
	name, path string // name as reported in the Diff
	data       []byte
	mode       fs.FileMode
}

// Apply makes the disk match p: p.Files under ws, each missing p.Homes and
// p.StatusDirs dir (0700) and p.HomeDirs dir (0755; an existing dir is left
// as it is and none is ever removed), p.Units in quadletDir, then it removes
// every unit in quadletDir whose marker names prefix but which p does not
// render (D28), and every env dir or env file p does not render.
// Unchanged files are left alone. Each file is replaced atomically, so a
// failure leaves every file old or new, never half-written, and running again
// finishes the job. With check, nothing is written: the Diff says what would
// change.
func Apply(p *Plan, ws, quadletDir, prefix string, check bool) (*Diff, error) {
	targets := make([]target, 0, len(p.Files)+len(p.Units))
	// Workspace files first: a unit never names a file that isn't there.
	for _, rel := range sortedKeys(p.Files) {
		f := p.Files[rel]
		targets = append(targets, target{rel, filepath.Join(ws, filepath.FromSlash(rel)), f.Data, f.Mode})
	}
	for _, name := range sortedKeys(p.Units) {
		targets = append(targets, target{name, filepath.Join(quadletDir, name), p.Units[name], unitMode})
	}

	d := &Diff{}
	var text strings.Builder
	var writes []target
	for _, t := range targets {
		old, oldMode, exists, err := readTarget(t.path)
		if err != nil {
			return nil, err
		}
		switch {
		case !exists:
			d.Added = append(d.Added, t.name)
			text.WriteString("--- /dev/null\n+++ b/" + t.name + "\n")
		case oldMode != t.mode || string(old) != string(t.data):
			d.Changed = append(d.Changed, t.name)
			text.WriteString("--- a/" + t.name + "\n+++ b/" + t.name + "\n")
			if oldMode != t.mode {
				fmt.Fprintf(&text, "old mode %04o\nnew mode %04o\n", oldMode, t.mode)
			}
		default:
			continue
		}
		text.WriteString(contentDiff(old, t.data, (exists && private(oldMode)) || private(t.mode)))
		writes = append(writes, t)
	}

	units, err := orphanUnits(p, quadletDir, prefix)
	if err != nil {
		return nil, err
	}
	envs, err := orphanEnv(p, ws)
	if err != nil {
		return nil, err
	}
	var dirs []string // the private dirs to create
	for _, rel := range slices.Concat(p.Homes, p.StatusDirs) {
		fi, err := os.Stat(filepath.Join(ws, filepath.FromSlash(rel)))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			dirs = append(dirs, rel)
			d.Added = append(d.Added, rel)
			text.WriteString("--- /dev/null\n+++ b/" + rel + "/\n(new directory, mode 0700)\n")
		case err != nil:
			return nil, err
		case !fi.IsDir():
			return nil, fmt.Errorf("%s: not a directory (a per-worktree dir a unit mounts)", filepath.Join(ws, filepath.FromSlash(rel)))
		}
	}
	var subdirs []homeSubdir // the mount points under a home to create
	for _, full := range p.HomeDirs {
		sd, err := splitHomeDir(p.Homes, full)
		if err != nil {
			return nil, err
		}
		switch _, err := os.Stat(filepath.Join(ws, filepath.FromSlash(sd.home))); {
		case errors.Is(err, fs.ErrNotExist): // the home is new: so is the dir
		case err != nil:
			return nil, err
		default:
			if err := sd.stat(ws); err == nil {
				continue
			} else if !errors.Is(err, fs.ErrNotExist) {
				return nil, err
			}
		}
		subdirs = append(subdirs, sd)
		d.Added = append(d.Added, full)
		text.WriteString("--- /dev/null\n+++ b/" + full + "/\n(new directory, mode 0755)\n")
	}
	for _, o := range slices.Concat(units, envs) {
		d.Removed = append(d.Removed, o.name)
		text.WriteString("--- a/" + o.name + "\n+++ /dev/null\n")
		text.WriteString(contentDiff(o.data, nil, private(o.mode)))
	}
	slices.Sort(d.Added)
	slices.Sort(d.Changed)
	slices.Sort(d.Removed)
	d.Text = text.String()
	if check {
		return d, nil
	}

	// Env dirs first, so no env file ever lands in a dir others can read;
	// one that already existed too open is tightened even when its files are
	// unchanged.
	privDirs := map[string]bool{}
	for _, t := range targets {
		if dir := filepath.Dir(t.path); private(t.mode) && !privDirs[dir] {
			privDirs[dir] = true
			if err := ensureDir(dir, true); err != nil {
				return nil, err
			}
		}
	}
	for _, rel := range dirs {
		dir := filepath.Join(ws, filepath.FromSlash(rel))
		if err := ensureDir(filepath.Dir(dir), false); err != nil {
			return nil, err
		}
		if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
	}
	for _, sd := range subdirs { // after the homes they lie in
		if err := sd.create(ws); err != nil {
			return nil, err
		}
	}
	for _, t := range writes {
		if err := ensureDir(filepath.Dir(t.path), false); err != nil {
			return nil, err
		}
		if err := tomlfile.WriteAtomic(t.path, t.data, t.mode); err != nil {
			return nil, err
		}
	}
	for _, o := range units {
		if err := os.Remove(o.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	for _, o := range envs {
		if err := os.RemoveAll(o.path); err != nil {
			return nil, err
		}
	}
	return d, nil
}

// private reports whether mode gives group and others no access.
func private(mode fs.FileMode) bool { return mode&0o077 == 0 }

// homeSubdir is one p.HomeDirs entry: rel under the home dir home, both
// slash paths, home workspace-relative.
type homeSubdir struct{ home, rel string }

// splitHomeDir finds the home of p.HomeDirs entry full among homes.
func splitHomeDir(homes []string, full string) (homeSubdir, error) {
	for _, h := range homes {
		if rel, ok := strings.CutPrefix(full, h+"/"); ok && filepath.IsLocal(rel) {
			return homeSubdir{h, rel}, nil
		}
	}
	return homeSubdir{}, fmt.Errorf("%s: under no home dir of the plan", full)
}

// stat and create go through an os.Root on the home: a container writes
// the home, so a symlink there must not lead them outside it.
func (sd homeSubdir) stat(ws string) error {
	root, err := os.OpenRoot(filepath.Join(ws, filepath.FromSlash(sd.home)))
	if err != nil {
		return err
	}
	defer root.Close()
	fi, err := root.Stat(sd.rel)
	if err == nil && !fi.IsDir() {
		return fmt.Errorf("%s: not a directory (a mount point under a home)", filepath.Join(ws, filepath.FromSlash(sd.home), filepath.FromSlash(sd.rel)))
	}
	return err
}

func (sd homeSubdir) create(ws string) error {
	root, err := os.OpenRoot(filepath.Join(ws, filepath.FromSlash(sd.home)))
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.MkdirAll(sd.rel, 0o755); err != nil {
		return fmt.Errorf("create the mount point %s: %w", filepath.Join(ws, filepath.FromSlash(sd.home), filepath.FromSlash(sd.rel)), err)
	}
	return nil
}

func contentDiff(old, data []byte, private bool) string {
	if !private {
		return lineDiff(old, data)
	}
	if string(old) == string(data) {
		return ""
	}
	return "(contents not shown: private file)\n"
}

// readTarget reads the file at path. A path that exists but is not a regular
// file reads as empty with mode 0, so it differs and is replaced.
func readTarget(path string) (data []byte, mode fs.FileMode, exists bool, err error) {
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, 0, false, nil
	case err != nil:
		return nil, 0, false, err
	case !fi.Mode().IsRegular():
		return nil, 0, true, nil
	}
	data, err = os.ReadFile(path)
	return data, fi.Mode().Perm(), true, err
}

// ensureDir creates dir (and its parents, 0755). A private dir is 0700.
func ensureDir(dir string, private bool) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if !private {
		return nil
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if fi.Mode().Perm() != 0o700 {
		return os.Chmod(dir, 0o700)
	}
	return nil
}

// orphanUnits are the regular files in quadletDir whose marker names prefix
// (a project-wide or worktree unit, never a machine-wide one) and which p does
// not render: units of a worktree no longer registered or a service no longer
// in the stack (D28). Files without a marker are the user's and stay.
func orphanUnits(p *Plan, quadletDir, prefix string) ([]target, error) {
	entries, err := os.ReadDir(quadletDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []target
	for _, e := range entries {
		if _, ok := p.Units[e.Name()]; ok || !e.Type().IsRegular() {
			continue
		}
		path := filepath.Join(quadletDir, e.Name())
		line, err := firstLine(path)
		if err != nil {
			return nil, err
		}
		if pfx, _, machine, ok := parseMarker(line); !ok || machine || pfx != prefix {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		out = append(out, target{name: e.Name(), path: path, data: data, mode: unitMode})
	}
	return out, nil
}

// firstLine is the first line of the file at path, read no further than a
// marker line can be long.
func firstLine(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	line, err := bufio.NewReader(f).ReadSlice('\n')
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return "", err
	}
	return strings.TrimSuffix(string(line), "\n"), nil
}

// MarkedUnit is a unit file in the Quadlet dir whose marker names a project,
// with the object names it declares.
type MarkedUnit struct {
	Path           string
	ContainerNames []string // ContainerName= values
	VolumeNames    []string // the named volumes of Volume=<name>:… (host paths excluded)
	NetworkNames   []string // NetworkName= values
}

// MarkedUnits are the regular files in dir, sorted by name, whose marker
// names exactly prefix (project-wide or worktree units; never machine-wide
// ones, never the user's unmarked files). A missing dir has none.
func MarkedUnits(dir, prefix string) ([]MarkedUnit, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []MarkedUnit
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		first, _, _ := strings.Cut(string(data), "\n")
		if pfx, _, machine, ok := parseMarker(first); !ok || machine || pfx != prefix {
			continue
		}
		u := MarkedUnit{Path: path}
		for line := range strings.Lines(string(data)) {
			k, v, ok := strings.Cut(strings.TrimRight(line, "\n"), "=")
			if !ok {
				continue
			}
			switch k {
			case "ContainerName":
				u.ContainerNames = append(u.ContainerNames, v)
			case "NetworkName":
				u.NetworkNames = append(u.NetworkNames, v)
			case "Volume":
				if name, _, ok := strings.Cut(v, ":"); ok && name != "" && !strings.HasPrefix(name, "/") {
					u.VolumeNames = append(u.VolumeNames, name)
				}
			}
		}
		out = append(out, u)
	}
	return out, nil
}

// orphanEnv are the entries under <ws>/.railswitch/env that p does not
// render: the env dir of a slug no longer registered, or a file in a live env
// dir that no service writes any more. Neither's contents are shown.
func orphanEnv(p *Plan, ws string) ([]target, error) {
	root := filepath.Join(ws, filepath.FromSlash(envDir))
	slugs, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	live := map[string]bool{} // every env dir and file p renders, slash-relative
	for rel := range p.Files {
		if rest, ok := strings.CutPrefix(rel, envDir+"/"); ok {
			live[rel] = true
			if slug, _, ok := strings.Cut(rest, "/"); ok {
				live[envDir+"/"+slug] = true
			}
		}
	}
	var out []target
	for _, s := range slugs {
		rel := envDir + "/" + s.Name()
		if !live[rel] {
			out = append(out, target{name: rel, path: filepath.Join(root, s.Name())})
			continue
		}
		if !s.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, s.Name()))
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if frel := rel + "/" + f.Name(); !live[frel] {
				out = append(out, target{name: frel, path: filepath.Join(root, s.Name(), f.Name())})
			}
		}
	}
	return out, nil
}
