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
	// Homes are the per-worktree home dirs (D30) a unit bind-mounts,
	// workspace-relative slash paths. Apply creates a missing one 0700 and
	// never removes one: they hold user data.
	Homes []string
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
	Text                    string // unified diff; a private file's contents are never shown
}

const (
	unitMode = fs.FileMode(0o644)
	envDir   = ".railswitch/env"  // D10: <envDir>/<slug>/<svc>.env, 0600 in 0700 dirs
	homeDir  = ".railswitch/home" // D30: <homeDir>/<slug>, a tool's per-worktree home, 0700
	// homeSource is the mount source of a per-worktree home.
	homeSource = "{worktree.home}"
)

// Plan renders the project: its units, every worktree's per-service env file
// (D10) and every package asset.
func (m *Model) Plan() (*Plan, error) {
	units, err := Quadlet(m)
	if err != nil {
		return nil, err
	}
	files := map[string]File{}
	var homes []string
	mountsHome := slices.ContainsFunc(m.Services, func(s Service) bool {
		return slices.ContainsFunc(s.Mounts, func(mt Mount) bool { return mt.Source == homeSource })
	})
	for _, wt := range m.Inputs.Worktrees {
		in, err := m.Instance(wt)
		if err != nil {
			return nil, fmt.Errorf("worktree %s: %w", wt.Slug, err)
		}
		for _, s := range m.Services {
			files[path.Join(envDir, wt.Slug, s.ID+".env")] = File{Data: EnvFile(in.Env[s.ID]), Mode: 0o600}
		}
		if mountsHome {
			homes = append(homes, path.Join(homeDir, wt.Slug))
		}
	}
	for _, a := range m.Assets {
		files[a.Rel] = File{Data: a.Data, Mode: a.Mode}
	}
	return &Plan{Units: units, Files: files, Homes: homes}, nil
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

// Apply makes the disk match p: p.Files under ws, each missing p.Homes dir
// (0700; an existing one is left as it is and none is ever removed), p.Units
// in quadletDir, then it removes every unit in quadletDir whose marker names
// prefix but which p does not render (D28), and every env dir or env file p
// does not render.
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
	var homes []string // the ones to create
	for _, rel := range p.Homes {
		fi, err := os.Stat(filepath.Join(ws, filepath.FromSlash(rel)))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			homes = append(homes, rel)
			d.Added = append(d.Added, rel)
			text.WriteString("--- /dev/null\n+++ b/" + rel + "/\n(new directory, mode 0700)\n")
		case err != nil:
			return nil, err
		case !fi.IsDir():
			return nil, fmt.Errorf("%s: not a directory (the per-worktree home a unit mounts)", filepath.Join(ws, filepath.FromSlash(rel)))
		}
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
	for _, rel := range homes {
		dir := filepath.Join(ws, filepath.FromSlash(rel))
		if err := ensureDir(filepath.Dir(dir), false); err != nil {
			return nil, err
		}
		if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
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
