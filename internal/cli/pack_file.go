package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/settings"
	"github.com/jbigler/railswitch/internal/workspace"
)

const packFileUsage = "usage: railswitch pack file show <ref> <path> | write <ref> <path> --if-hash <hash> [--dry-run] [--restart] | rm <ref> <path> --if-hash <hash>"

// railswitch pack file show|write|rm: a package's files (D91, D93).
func cmdPackFile(ctx context.Context, e *env, args []string) int {
	if len(args) == 0 {
		return e.usageErr("%s", packFileUsage)
	}
	switch args[0] {
	case "show":
		return packFileShow(e, args[1:])
	case "write":
		return packFileWrite(ctx, e, args[1:])
	case "rm":
		return packFileRm(ctx, e, args[1:])
	}
	return e.usageErr("pack file: unknown subcommand %q\n%s", args[0], packFileUsage)
}

// packFileNameRe is a file name in entrypoints/ or initdb/, and the <name>
// of Containerfile.<name> and <name>.sh (D93).
var packFileNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// packFilePath checks rel, a slash path in a local package whose manifest
// is manifest, against the package format's files (D93): at the top the
// manifest, Containerfile.<name>, deps.allow, README.md and <name>.sh; in
// entrypoints/ and initdb/ any <file>. Nothing else, so no "..", no
// hidden file and no deeper directory.
func packFilePath(rel, manifest string) error {
	top, file, nested := strings.Cut(rel, "/")
	ok := false
	if manifest == "agent.toml" {
		// An agent's package also holds the files its mounts name
		// (agent.toml mounts), at the top: any plain name.
		if nested {
			ok = top == "entrypoints" && packFileNameRe.MatchString(file)
		} else {
			ok = packFileNameRe.MatchString(top)
		}
	} else if nested {
		ok = (top == "entrypoints" || top == "initdb") && packFileNameRe.MatchString(file)
	} else {
		cf, isCF := strings.CutPrefix(top, "Containerfile.")
		stem, isSh := strings.CutSuffix(top, ".sh")
		switch {
		case top == manifest, top == "deps.allow", top == "README.md":
			ok = true
		case isCF:
			ok = packFileNameRe.MatchString(cf)
		case isSh:
			ok = packFileNameRe.MatchString(stem)
		}
	}
	if !ok {
		if manifest == "agent.toml" {
			return fmt.Errorf("%q is not a package file: an agent's are a <file> at the top or entrypoints/<file>", rel)
		}
		return fmt.Errorf("%q is not a package file: the manifest (%s), Containerfile.<name>, deps.allow, README.md or <name>.sh at the top, or entrypoints/<file> or initdb/<file>", rel, manifest)
	}
	return nil
}

// realDir refuses rel under root unless it is a directory, not a symlink;
// a missing one is fs.ErrNotExist.
func realDir(root, rel string) error {
	fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	switch {
	case err != nil:
		return err
	case fi.Mode()&fs.ModeSymlink != 0:
		return fmt.Errorf("%s is a symlink: a package's directories must be real ones", rel)
	case !fi.IsDir():
		return fmt.Errorf("%s is not a directory", rel)
	}
	return nil
}

// localPackage is the workspace-relative directory and the manifest of
// the local package ref: packs/<name>/ when it exists, else
// options/<name>/, else agents/<name>/. A symlinked directory is refused,
// and so is a ref whose version is not the one its manifest declares (a
// manifest that doesn't parse is not checked: a write may be fixing it).
func localPackage(root string, ref pack.Ref) (dir, manifest string, err error) {
	for _, k := range []struct{ top, manifest string }{{"packs", "pack.toml"}, {"options", "option.toml"}, {"agents", "agent.toml"}} {
		dir := k.top + "/" + ref.Name
		err := realDir(root, k.top)
		if err == nil {
			err = realDir(root, dir)
		}
		if err == nil {
			return dir, k.manifest, checkVersion(root, dir, k.manifest, ref)
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", "", err
		}
	}
	return "", "", fmt.Errorf("%s: no packs/%s/, options/%s/ or agents/%s/ in %s", ref, ref.Name, ref.Name, ref.Name, root)
}

// checkVersion refuses ref when dir's manifest declares another version.
func checkVersion(root, dir, manifest string, ref pack.Ref) error {
	data, exists, err := readPackFile(root, dir, manifest)
	if err != nil || !exists {
		return err
	}
	var m struct{ Version int }
	if _, err := toml.Decode(string(data), &m); err != nil || m.Version < 1 || m.Version == ref.Version {
		return nil
	}
	have := pack.Ref{Name: ref.Name, Version: m.Version, Local: true}
	return fmt.Errorf("%s/%s is %s, not %s", dir, manifest, have, ref)
}

// readPackFile reads rel of the package dir (localPackage) as a hostile
// file; exists is false for a missing one. A symlinked entrypoints/ or
// initdb/, or a file that is not a regular one, is refused.
func readPackFile(root, dir, rel string) (data []byte, exists bool, err error) {
	if sub, _, nested := strings.Cut(rel, "/"); nested {
		if err := realDir(root, dir+"/"+sub); errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		} else if err != nil {
			return nil, false, err
		}
	}
	data, err = safefile.Read(filepath.Join(root, filepath.FromSlash(dir+"/"+rel)), safefile.Limit)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	return data, err == nil, err
}

// packFileDoc is `pack file show --json` (docs/json.md).
type packFileDoc struct {
	Path string `json:"path"`
	Text string `json:"text"`
	Hash string `json:"hash"`
}

// railswitch pack file show <ref> <path>: a file of a local package, or of
// a built-in pack or option (read-only).
func packFileShow(e *env, args []string) int {
	fset := e.flags("pack file show")
	a, code, ok := parseArgs(e, fset, args, 2, "usage: railswitch pack file show <ref> <path>")
	if !ok {
		return code
	}
	ref, err := pack.ParseRef(a[0])
	if err != nil {
		return e.usageErr("pack file show: %v", err)
	}
	rel := a[1]
	if !fs.ValidPath(rel) || rel == "." {
		return e.usageErr("pack file show: %q is not a path inside the package", rel)
	}
	var data []byte
	if ref.Local {
		root, err := workspace.Root(e.dir)
		if err != nil {
			return e.fail(err)
		}
		dir, _, err := localPackage(root, ref)
		if err != nil {
			return e.fail(err)
		}
		var exists bool
		if data, exists, err = readPackFile(root, dir, rel); err != nil {
			return e.fail(err)
		} else if !exists {
			return e.fail(fmt.Errorf("%s/%s does not exist", dir, rel))
		}
	} else {
		cat, err := pack.LoadBuiltins()
		if err != nil {
			return e.fail(err)
		}
		var files fs.FS
		if p := cat.Packs[ref]; p != nil {
			files = p.Files
		} else if o := cat.Options[ref]; o != nil {
			files = o.Files
		} else if a := cat.Agents[ref]; a != nil {
			files = a.Files
		} else {
			return e.fail(fmt.Errorf("no built-in pack, option or agent %s (packs: %s; options: %s; agents: %s)", ref, refNames(cat.Packs), refNames(cat.Options), refNames(cat.Agents)))
		}
		if data, err = fs.ReadFile(files, rel); err != nil {
			return e.fail(fmt.Errorf("%s: %w", ref, err))
		}
	}
	if !e.json {
		e.stdout.Write(data)
		return 0
	}
	if err := json.NewEncoder(e.stdout).Encode(packFileDoc{Path: rel, Text: string(data), Hash: workspace.Hash(data)}); err != nil {
		return e.fail(err)
	}
	return 0
}

// railswitch pack file write <ref> <path> --if-hash <h> [--dry-run]
// [--restart]: stdin replaces a local package's file (--if-hash "": adds it).
func packFileWrite(ctx context.Context, e *env, args []string) int {
	const usage = "usage: railswitch pack file write <ref> <path> --if-hash <hash> [--dry-run] [--restart]  (the text on stdin; --if-hash \"\" adds a new file)"
	fset := e.flags("pack file write")
	hash := fset.String("if-hash", "", "the hash of the file the edit started from (pack file show --json); \"\" for a new file (required)")
	dry := fset.Bool("dry-run", false, "check the new text and print what it changes; write nothing")
	restart := fset.Bool("restart", false, "restart the running worktrees the change affects")
	a, code, ok := parseArgs(e, fset, args, 2, usage)
	if !ok {
		return code
	}
	if !isSet(fset, "if-hash") {
		return e.usageErr("%s", usage)
	}
	data, err := e.readStdin()
	if err != nil {
		return e.fail(err)
	}
	return packFileChange(ctx, e, a[0], a[1], *hash, data, *dry, *restart)
}

// railswitch pack file rm <ref> <path> --if-hash <h>: remove a local
// package's file; never its manifest.
func packFileRm(ctx context.Context, e *env, args []string) int {
	const usage = "usage: railswitch pack file rm <ref> <path> --if-hash <hash>"
	fset := e.flags("pack file rm")
	hash := fset.String("if-hash", "", "the hash of the file (pack file show --json; required)")
	a, code, ok := parseArgs(e, fset, args, 2, usage)
	if !ok {
		return code
	}
	if *hash == "" {
		return e.usageErr("%s", usage)
	}
	return packFileChange(ctx, e, a[0], a[1], *hash, nil, false, false)
}

func isSet(fset *flag.FlagSet, name string) bool {
	set := false
	fset.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

// packFileChange sets rel of the local package refArg to data (nil:
// removes it) when the file's hash is still hash ("": it must not exist)
// and the package still loads with the change. When the project's stack
// uses the package, every worktree is rendered with it in memory first,
// and the write locks and renders through commit; otherwise only the file
// is written.
func packFileChange(ctx context.Context, e *env, refArg, rel, hash string, data []byte, dry, restart bool) int {
	ref, err := pack.ParseRef(refArg)
	if err != nil {
		return e.usageErr("pack file: %v", err)
	}
	if !ref.Local {
		return e.usageErr("pack file: %s is built in; only a local: package is edited (railswitch pack eject %s makes one)", ref, ref)
	}
	root, err := workspace.Root(e.dir)
	if err != nil {
		return e.fail(err)
	}
	dir, manifest, err := localPackage(root, ref)
	if err != nil {
		return e.fail(err)
	}
	if err := packFilePath(rel, manifest); err != nil {
		return e.fail(err)
	}
	if data == nil && rel == manifest {
		return e.fail(fmt.Errorf("%s is the package's manifest; it can't be removed", rel))
	}
	cur, exists, err := readPackFile(root, dir, rel)
	if err != nil {
		return e.fail(err)
	}
	wrel := dir + "/" + rel
	switch {
	case hash == "" && exists:
		return e.fail(fmt.Errorf("%s exists: pass its hash (pack file show --json) to replace it", wrel))
	case hash != "" && (!exists || workspace.Hash(cur) != hash):
		return e.fail(fmt.Errorf("%s changed on disk since you opened it", wrel))
	}

	p, err := config.Load(filepath.Join(root, "project.toml"))
	if err != nil {
		return e.fail(err)
	}
	used := usesRef(p, ref)
	cat, err := pack.LoadCatalog(root)
	if err != nil {
		return e.fail(err)
	}
	w := judgedWrite{hash: workspace.Hash(data), used: &used}
	next, err := cat.WithFile(wrel, data)
	if err != nil {
		return e.fail(err)
	}
	switch manifest {
	case "pack.toml":
		_, w.err = next.Pack(ref)
	case "option.toml":
		_, w.err = next.Option(ref)
	default:
		_, w.err = next.Agent(ref)
	}
	if used && w.err == nil {
		// The current project may not load (the write may be its fix): the
		// change is judged from the project as it would be.
		var to *workspace.Workspace
		if to, w.err = workspace.LoadEdited(ctx, root, next, ref); w.err == nil {
			var se settings.Env
			if se, err = e.envFor(ctx, to); err != nil {
				return e.fail(err)
			}
			from, _ := workspace.Load(ctx, root, "")
			w.change, w.err = se.Compare(from, to)
		}
	}
	if dry {
		return e.printJudged(w)
	}
	mode := os.FileMode(0o644)
	if path.Ext(rel) == ".sh" {
		mode = 0o755
	}
	change := fileChange{Rel: wrel, Data: data, Mode: mode}
	if used {
		return e.commitJudged(ctx, root, w, []fileChange{change}, &workspace.Accept{Edit: &workspace.FileEdit{Kind: strings.TrimSuffix(manifest, ".toml"), Ref: ref, Path: rel}}, restart)
	}
	if w.err != nil {
		return e.fail(w.err)
	}
	line, err := applyChange(root, change)
	if err != nil {
		return e.fail(err)
	}
	fmt.Fprintln(e.stdout, line)
	return 0
}

// usesRef reports whether p's stack names ref: its pack, an option or an
// agent's package. It reads project.toml only, so a package that doesn't
// load is still known to be used.
func usesRef(p *config.Project, ref pack.Ref) bool {
	r := ref.String()
	if p.Stack.Pack == r || slices.Contains(slices.Collect(maps.Values(p.Options)), r) {
		return true
	}
	for _, a := range p.Agents {
		if a.Use == r {
			return true
		}
	}
	return false
}

// packFileEntry is one of `pack show --json` files.
type packFileEntry struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	Executable bool   `json:"executable"`
}

// packFiles are the regular files of a package, by slash path, sorted;
// symlinks are not followed or listed. A built-in's embedded files have no
// mode bits: its scripts are the .sh files, executable once ejected.
func packFiles(files fs.FS, builtin bool) ([]packFileEntry, error) {
	out := []packFileEntry{}
	err := fs.WalkDir(files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		exe := fi.Mode()&0o111 != 0 || builtin && strings.HasSuffix(p, ".sh")
		out = append(out, packFileEntry{Path: p, Size: fi.Size(), Executable: exe})
		return nil
	})
	slices.SortFunc(out, func(a, b packFileEntry) int { return strings.Compare(a.Path, b.Path) })
	return out, err
}
