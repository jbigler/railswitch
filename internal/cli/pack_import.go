package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing/fstest"

	"github.com/BurntSushi/toml"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/review"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/settings"
	"github.com/jbigler/railswitch/internal/workspace"
)

// The package kinds a folder can hold, by manifest (D114).
var folderKinds = []struct{ kind, top, manifest string }{
	{pack.KindPack, "packs", "pack.toml"},
	{pack.KindOption, "options", "option.toml"},
	{pack.KindAgent, "agents", "agent.toml"},
}

// A package folder's bounds: at most this many files, this much in all.
const (
	maxFolderFiles = 512
	maxFolderBytes = 16 << 20
)

// railswitch pack export <ref> --to <dir> (D114): the package as this
// project runs it (the accepted copy of a locked one), as a folder to hand
// to someone; a local package keeps its name.
func packExport(e *env, args []string) int {
	const usage = "usage: railswitch pack export <ref> --to <dir>"
	fset := e.flags("pack export")
	to := fset.String("to", "", "write the package folder `dir` (must not exist; required)")
	arg, code, ok := parseNamed(e, fset, args, usage)
	if !ok {
		return code
	}
	if *to == "" {
		return e.usageErr("%s", usage)
	}
	ref, err := pack.ParseRef(arg)
	if err != nil {
		return e.usageErr("pack export: %v", err)
	}
	root, err := workspace.Root(e.dir)
	if err != nil {
		return e.fail(err)
	}
	cat, err := workspace.LoadCatalog(root)
	if err != nil {
		return e.fail(err)
	}
	p, perr := config.Load(filepath.Join(root, "project.toml"))
	if ref.Library != "" {
		// Only what a review accepted leaves for the user's files: a
		// library package this project uses and has installed.
		if perr != nil {
			return e.fail(perr)
		}
		if !usesRef(p, ref) {
			return e.fail(fmt.Errorf("pack export: %s is a library package this project doesn't use: only an installed one is exported", ref))
		}
		if err := workspace.CheckInstalled(root, p, cat.Libraries); err != nil {
			return e.fail(fmt.Errorf("pack export: %w", err))
		}
	}
	if perr == nil {
		if cat, err = workspace.Accepted(root, p, cat); err != nil {
			return e.fail(err)
		}
	}
	dest, err := filepath.Abs(*to)
	if err != nil {
		return e.fail(err)
	}
	for _, k := range folderKinds {
		if rel, err := filepath.Rel(filepath.Join(root, k.top), dest); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
			return e.fail(fmt.Errorf("pack export: %s is inside the workspace's %s/: export to a folder outside it (railswitch pack eject makes a local copy)", dest, k.top))
		}
	}
	*to = dest
	var files fs.FS
	var manifest string
	if p, err := cat.Pack(ref); err == nil && p != nil {
		files, manifest = p.Files, "pack.toml"
	} else if o, err := cat.Option(ref); err == nil && o != nil {
		files, manifest = o.Files, "option.toml"
	} else if a, err := cat.Agent(ref); err == nil && a != nil {
		files, manifest = a.Files, "agent.toml"
	} else {
		return e.fail(fmt.Errorf("pack export: no pack, option or agent %s here (railswitch pack ls lists them)", ref))
	}
	if err := copyPackage(*to, files, manifest, ref.Name); err != nil {
		return e.fail(fmt.Errorf("pack export: %w", err))
	}
	fmt.Fprintf(e.stdout, "wrote %s to %s: railswitch pack import %s, in a workspace, reviews and adds it\n", ref, *to, shellQuote(*to))
	return 0
}

// packImportDoc is `pack import --json` (docs/json.md): lock --review's
// document with one package, plus its status.
type packImportDoc struct {
	Ref      string          `json:"ref"`
	Kind     string          `json:"kind"`
	SHA256   string          `json:"sha256"`
	Status   string          `json:"status"` // new, update, same
	Replaces *importReplaces `json:"replaces"`
	// ComparedWith is the built-in a new package is shown against: the
	// one of its kind sharing the most identical files; "" for none.
	ComparedWith string          `json:"compared_with"`
	Used         bool            `json:"used"`
	Hash         string          `json:"hash"`
	Flags        int             `json:"flags"`
	Packages     []importPackage `json:"packages"`
	Restart      []string        `json:"restart"`
	Warnings     []string        `json:"warnings"`
}

// incomingPackage is a package folder as pack import would write it.
type incomingPackage struct {
	kind, top, manifest string
	ref                 pack.Ref
	files               fstest.MapFS // with the manifest's name set
	sha                 string
	dir                 string // workspace-relative: <top>/<name>
	old                 fs.FS  // the local package there now; nil: none
	oldRef              pack.Ref
	oldSHA              string
	status              string
	used                bool
	base                fs.FS // a new package's closest built-in; nil: none
	baseRef             pack.Ref
}

// railswitch pack import <dir> [--as <name>] [--accept <sha256>]
// [--restart] (D114): review a package folder someone handed over against
// the workspace's local package of its name; with --accept, the hash the
// review printed, write it there as local:<name>@N (a package the project
// uses is locked at it and rendered).
func packImport(ctx context.Context, e *env, args []string) int {
	const usage = "usage: railswitch pack import <dir> [--as <name>] [--accept <sha256>] [--restart]"
	fset := e.flags("pack import")
	as := fset.String("as", "", "the local package's `name` (default: the folder's manifest name)")
	accept := fset.String("accept", "", "write the package whose review printed this `sha256`")
	restart := fset.Bool("restart", false, "with --accept: restart the running worktrees the change affects")
	dir, code, ok := parseNamed(e, fset, args, usage)
	if !ok {
		return code
	}
	if *restart && *accept == "" {
		return e.usageErr("%s", usage)
	}
	root, err := workspace.Root(e.dir)
	if err != nil {
		return e.fail(err)
	}
	if dir, err = filepath.Abs(dir); err != nil {
		return e.fail(err)
	}
	in, err := readIncomingPackage(root, dir, *as)
	if err != nil {
		return e.fail(err)
	}
	p, err := config.Load(filepath.Join(root, "project.toml"))
	if err != nil {
		return e.fail(err)
	}
	in.used = usesRef(p, in.ref)
	if in.old != nil && in.oldRef != in.ref && usesRef(p, in.oldRef) {
		return e.fail(fmt.Errorf("the project uses %s and the folder is %s: import it --as another name, or move the project off %s first", in.oldRef, in.ref, in.oldRef))
	}
	cat, err := workspace.LoadCatalog(root)
	if err != nil {
		return e.fail(err)
	}
	next, err := cat.WithPackage(in.kind, in.ref, in.files, nil)
	if err != nil {
		return e.fail(fmt.Errorf("%s does not load: %w", dir, err))
	}
	if in.old == nil {
		in.baseRef, in.base = closestBuiltin(in.kind, in.files)
	}
	var change *settings.Change
	if in.used && in.status != importSame {
		from, err := workspace.Load(ctx, root, "")
		if err != nil {
			return e.fail(err)
		}
		if slices.Contains(from.Pending, in.ref) {
			return e.fail(fmt.Errorf("%s has an update waiting for review: review it first (railswitch lock --review)", in.ref))
		}
		to, err := workspace.LoadEdited(ctx, root, next, in.ref)
		if err != nil {
			return e.fail(err)
		}
		se, err := e.envFor(ctx, to)
		if err != nil {
			return e.fail(err)
		}
		if change, err = se.Compare(from, to); err != nil {
			return e.fail(err)
		}
	}
	if *accept != "" {
		if *accept != in.sha {
			return e.fail(fmt.Errorf("%s changed since its review (sha256 %s, not %s): review it again", dir, in.sha, *accept))
		}
		return e.writeImported(ctx, root, in, change, *restart)
	}
	doc, err := packImportReview(in, change)
	if err != nil {
		return e.fail(err)
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(doc); err != nil {
			return e.fail(err)
		}
		return 0
	}
	ref := safefile.Printable(doc.Ref)
	var head string
	switch doc.Status {
	case importSame:
		fmt.Fprintf(e.stdout, "%s is already %s/: nothing to import\n", ref, in.dir)
		return 0
	case importNew:
		head = fmt.Sprintf("%s %s (sha256 %s): new, as %s/", doc.Kind, ref, doc.SHA256, in.dir)
		if doc.ComparedWith != "" {
			head += "; shown as its changes from the built-in " + doc.ComparedWith
		}
	default:
		head = fmt.Sprintf("%s %s (sha256 %s): replaces %s/ (%s)", doc.Kind, ref, doc.SHA256, in.dir, safefile.Printable(doc.Replaces.Ref))
	}
	if doc.Used {
		head += "; the project uses it"
		if len(doc.Restart) > 0 {
			head += ", restarts " + strings.Join(doc.Restart, ", ")
		}
	}
	nextCmd := fmt.Sprintf("railswitch pack import %s", shellQuote(dir))
	if *as != "" {
		nextCmd += " --as " + *as
	}
	printFolderReview(e, head, doc.Packages[0].Files, doc.Warnings, doc.Flags, nextCmd+" --accept "+doc.SHA256)
	return 0
}

const (
	importNew    = "new"
	importUpdate = "update"
	importSame   = "same"
)

// readIncomingPackage reads the package folder dir as hostile input and
// judges it against the workspace's local package of its name: the
// manifest (exactly one of pack.toml, option.toml, agent.toml) sets the
// kind; every file must be a path pack.CheckFilePath allows, a regular
// file read through safefile (≤ 1 MiB), at most maxFolderFiles and
// maxFolderBytes in all; a symlink anywhere, dir included, refuses. as
// ("": the manifest's) names it; the manifest's name line is set to it.
func readIncomingPackage(root, dir, as string) (*incomingPackage, error) {
	fi, err := os.Lstat(dir)
	switch {
	case err != nil:
		return nil, err
	case !fi.IsDir():
		return nil, fmt.Errorf("%s is not a package folder", dir)
	}
	in := &incomingPackage{}
	for _, k := range folderKinds {
		if fi, err := os.Lstat(filepath.Join(dir, k.manifest)); err == nil && fi.Mode().IsRegular() {
			if in.manifest != "" {
				return nil, fmt.Errorf("%s holds both %s and %s: one package per folder", dir, in.manifest, k.manifest)
			}
			in.kind, in.top, in.manifest = k.kind, k.top, k.manifest
		}
	}
	if in.manifest == "" {
		return nil, fmt.Errorf("%s has no pack.toml, option.toml or agent.toml: not a package folder", dir)
	}
	in.files = fstest.MapFS{}
	total := 0
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil || d.IsDir() {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s: not a regular file; refused", rel)
		}
		if err := pack.CheckFilePath(rel, in.manifest); err != nil {
			return fmt.Errorf("%w (a package folder holds only the package's files: no .git, .DS_Store or other extras)", err)
		}
		if len(in.files) == maxFolderFiles {
			return fmt.Errorf("more than %d files", maxFolderFiles)
		}
		data, err := safefile.Read(p, safefile.Limit)
		if err != nil {
			return err
		}
		if total += len(data); total > maxFolderBytes {
			return fmt.Errorf("more than %d MiB in all", maxFolderBytes>>20)
		}
		mode := os.FileMode(0o644)
		if path.Ext(rel) == ".sh" {
			mode = 0o755
		}
		in.files[rel] = &fstest.MapFile{Data: data, Mode: mode}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	var m struct {
		Name    string
		Version int
	}
	mf := in.files[in.manifest]
	if _, err := toml.Decode(string(mf.Data), &m); err != nil {
		return nil, fmt.Errorf("%s/%s: %w", dir, in.manifest, err)
	}
	name := m.Name
	if as != "" {
		name = as
	}
	if !packNameRe.MatchString(name) {
		return nil, fmt.Errorf("package name %q must match %s (pass --as)", name, packNameRe)
	}
	if m.Version < 1 {
		return nil, fmt.Errorf("%s/%s: version must be 1 or more", dir, in.manifest)
	}
	loc := manifestNameRe.FindIndex(mf.Data)
	table := tableHeaderRe.FindIndex(mf.Data)
	if loc == nil || table != nil && table[0] < loc[0] || !manifestNameIs(mf.Data[loc[0]:loc[1]], m.Name) {
		return nil, fmt.Errorf("%s/%s: its top-level name must be one line name = \"<name>\", before any [table]", dir, in.manifest)
	}
	mf.Data = slices.Concat(mf.Data[:loc[0]], []byte(fmt.Sprintf("name = %q", name)), mf.Data[loc[1]:])
	in.ref = pack.Ref{Local: true, Name: name, Version: m.Version}
	in.dir = in.top + "/" + name
	if in.sha, err = pack.ContentHash(in.files); err != nil {
		return nil, err
	}
	for _, k := range folderKinds {
		other := k.top + "/" + name
		err := realDir(root, k.top)
		if err == nil {
			err = realDir(root, other)
		}
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return nil, err
		case k.top != in.top:
			return nil, fmt.Errorf("%s/ exists: local:%s would name two packages; import it --as another name", other, name)
		}
		old, err := pack.ReadTree(filepath.Join(root, filepath.FromSlash(other)), "")
		if err != nil {
			return nil, fmt.Errorf("%s: %w", other, err)
		}
		in.old = old
		if in.oldSHA, err = pack.ContentHash(old); err != nil {
			return nil, err
		}
		var om struct{ Version int }
		if data, err := fs.ReadFile(old, in.manifest); err == nil {
			_, _ = toml.Decode(string(data), &om)
		}
		in.oldRef = pack.Ref{Local: true, Name: name, Version: om.Version}
	}
	switch {
	case in.old == nil:
		in.status = importNew
	case in.oldSHA == in.sha:
		in.status = importSame
	default:
		in.status = importUpdate
	}
	return in, nil
}

// packImportReview is in as a review document.
func packImportReview(in *incomingPackage, change *settings.Change) (*packImportDoc, error) {
	ref := in.ref.String()
	doc := &packImportDoc{Ref: ref, Kind: in.kind, SHA256: in.sha, Status: in.status, Used: in.used, Hash: in.sha, Restart: []string{}, Warnings: []string{}}
	pkg := importPackage{Ref: ref, Kind: in.kind, To: in.sha, AlsoLockedIn: []string{}, Status: review.Added}
	against := in.old
	if in.old != nil {
		doc.Replaces = &importReplaces{Ref: in.oldRef.String(), SHA256: in.oldSHA}
		pkg.From, pkg.Status = in.oldSHA, review.Changed
	} else if in.base != nil {
		doc.ComparedWith, against = in.baseRef.String(), in.base
	}
	if in.status == importSame {
		pkg.Status = "unchanged"
	}
	if change != nil {
		doc.Restart = append(doc.Restart, change.Restart...)
	}
	files, err := review.Compare(in.kind, against, in.files)
	if err != nil {
		return nil, err
	}
	pkg.Files = files
	for _, f := range files {
		doc.Flags += len(f.Flags)
	}
	doc.Packages = []importPackage{pkg}
	return doc, nil
}

// closestBuiltin is the built-in package of kind sharing the most
// identical files with files (a manifest's name line aside), at least
// one; a new package's review is its diff from it.
func closestBuiltin(kind string, files fstest.MapFS) (pack.Ref, fs.FS) {
	cat, err := pack.LoadBuiltins()
	if err != nil {
		return pack.Ref{}, nil
	}
	cands := map[pack.Ref]fs.FS{}
	switch kind {
	case pack.KindPack:
		for r, p := range cat.Packs {
			cands[r] = p.Files
		}
	case pack.KindOption:
		for r, o := range cat.Options {
			cands[r] = o.Files
		}
	case pack.KindAgent:
		for r, a := range cat.Agents {
			cands[r] = a.Files
		}
	}
	var best pack.Ref
	var bestFS fs.FS
	most := 0
	for _, r := range slices.SortedFunc(maps.Keys(cands), func(a, b pack.Ref) int { return strings.Compare(a.String(), b.String()) }) {
		n := 0
		for p, f := range files {
			data, err := fs.ReadFile(cands[r], p)
			if err == nil && unnamed(data) == unnamed(f.Data) {
				n++
			}
		}
		if n > most {
			best, bestFS, most = r, cands[r], n
		}
	}
	return best, bestFS
}

// unnamed is data with its manifest name line blanked.
func unnamed(data []byte) string {
	return manifestNameRe.ReplaceAllString(string(data), "")
}

// writeImported writes in's files as root/<top>/<name>/. A package the
// project uses goes through commit: every file, the old ones it lacks
// removed, locked at the reviewed content (Accept.Import) and rendered,
// all put back on failure. An unused one is written beside the old
// directory and renamed over it.
func (e *env) writeImported(ctx context.Context, root string, in *incomingPackage, change *settings.Change, restart bool) int {
	if in.status == importSame {
		fmt.Fprintf(e.stdout, "%s is already %s/\n", in.ref, in.dir)
		return 0
	}
	if !in.used {
		if err := writeFolder(filepath.Join(root, filepath.FromSlash(in.dir)), in.files); err != nil {
			return e.fail(err)
		}
		fmt.Fprintf(e.stdout, "wrote %s/ (%s): %s\n", in.dir, in.ref, useHint(in.kind, in.ref))
		return 0
	}
	var changes []fileChange
	for _, rel := range sortedPaths(in.files) {
		f := in.files[rel]
		changes = append(changes, fileChange{Rel: in.dir + "/" + rel, Data: f.Data, Mode: f.Mode})
	}
	if in.old != nil {
		_ = fs.WalkDir(in.old, ".", func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && in.files[p] == nil {
				changes = append(changes, fileChange{Rel: in.dir + "/" + p})
			}
			return nil
		})
	}
	w := judgedWrite{hash: in.sha, change: change}
	code := e.commitJudged(ctx, root, w, changes, &workspace.Accept{Import: &workspace.PackageImport{Kind: in.kind, Ref: in.ref, SHA256: in.sha}}, restart)
	if code != 1 {
		fmt.Fprintf(e.stdout, "wrote %s/ (%s), locked and rendered\n", in.dir, in.ref)
	}
	return code
}

// tableHeaderRe is a TOML table header line.
var tableHeaderRe = regexp.MustCompile(`(?m)^\s*\[`)

// manifestNameIs reports whether line, a name line manifestNameRe matched,
// sets name.
func manifestNameIs(line []byte, name string) bool {
	var m struct{ Name string }
	_, err := toml.Decode(string(line), &m)
	return err == nil && m.Name == name
}

// useHint says where a project starts using the local package ref of kind.
func useHint(kind string, ref pack.Ref) string {
	switch kind {
	case pack.KindPack:
		return fmt.Sprintf("pick it for the pack in Settings ([stack] pack = %q)", ref)
	case pack.KindOption:
		return "pick it for its slot in Settings"
	}
	return fmt.Sprintf("use it with railswitch agent add <name> --use %s", ref)
}

func sortedPaths(m fstest.MapFS) []string {
	out := make([]string, 0, len(m))
	for p := range m {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

// writeFolder writes files as the directory dir: a temp directory beside
// it, renamed into place; an existing dir is moved aside first and removed
// after.
func writeFolder(dir string, files fstest.MapFS) error {
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".import-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for _, rel := range sortedPaths(files) {
		if err := writePackageFile(filepath.Join(tmp, filepath.FromSlash(rel)), files[rel].Data, files[rel].Mode); err != nil {
			return err
		}
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	old := ""
	switch _, err := os.Lstat(dir); {
	case err == nil:
		old = tmp + ".old"
		if err := os.Rename(dir, old); err != nil {
			return err
		}
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		if old != "" {
			err = errors.Join(err, os.Rename(old, dir))
		}
		return err
	}
	if old != "" {
		// The new directory is in place; a leftover is never a package
		// (localManifests skips names packNameRe refuses).
		_ = os.RemoveAll(old)
	}
	return nil
}
