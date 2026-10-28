package libraries

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/signing"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/template"
	"github.com/jbigler/railswitch/internal/tomlfile"
)

// fetchTimeout bounds the whole of Fetch's git work.
var fetchTimeout = 5 * time.Minute

const (
	tempPrefix = "railswitch-library-"
	staleAfter = 15 * time.Minute // far past fetchTimeout
	maxFiles   = 2000
	maxTotal   = 64 << 20
	// maxListing bounds ls-tree's output: maxFiles entries of a mode, an
	// oid and a path of up to 1 KiB.
	maxListing = maxFiles * (1 << 10)
)

// manifests is the manifest of each top directory of a library.
var manifests = map[string]string{"packs": "pack.toml", "options": "option.toml", "agents": "agent.toml", templatesDir: template.StackFile}

// templatesDir holds a library's templates (D109): <name>/ each, its files
// those template.CheckPath allows.
const templatesDir = "templates"

// TrustUpdate runs fn on signers.toml under its lock and saves it when fn
// returns save: signing.UpdateTrust on the machine's file.
type TrustUpdate func(fn func(t *signing.Trust) (save bool, err error)) error

// FetchResult is one fetch's verdict. Pending: the checkout waits in
// pending/ for library trust.
type FetchResult struct {
	Status   signing.Status
	Commit   string
	Serial   int64
	Packages int // signed packages
	Pending  bool
	Reason   string
	Verify   *signing.Result
}

// Pending is <data>/libraries/<alias>/pending.toml, beside pending/: what
// library trust confirms and promote records.
type Pending struct {
	Schema         int            `toml:"schema"`
	Library        string         `toml:"library"`
	Commit         string         `toml:"commit"`
	Serial         int64          `toml:"serial"`
	Status         signing.Status `toml:"status"`
	Fingerprint    string         `toml:"fingerprint"`
	NewFingerprint string         `toml:"new_fingerprint"`
	KnownLibrary   bool           `toml:"known_library"`
}

// PendingDir is the alias's checkout that waits for library trust.
func PendingDir(d machine.Dirs, alias string) string {
	return filepath.Join(Dir(d, alias), "pending")
}

func pendingPath(d machine.Dirs, alias string) string {
	return filepath.Join(Dir(d, alias), "pending.toml")
}

// ReadPending is the alias's pending.toml; none is an fs.ErrNotExist
// error.
func ReadPending(d machine.Dirs, alias string) (*Pending, error) {
	path := pendingPath(d, alias)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Pending
	if err := tomlfile.Decode(data, Schema, &p); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &p, nil
}

// Fetch clones lib's branch, a hostile repository (D104), into a staging
// dir and verifies it under update. trusted: it replaces current/.
// unknown-signer or rotation: it waits as pending/. Any other verdict:
// it is dropped. Each verdict is recorded in fetch.toml, and a verdict
// that is not pending drops an older pending/. An error is no verdict:
// nothing in the alias's directory changes. The temp clone is always
// removed.
func Fetch(ctx context.Context, run sys.Runner, d machine.Dirs, lib *Library, update TrustUpdate) (*FetchResult, error) {
	if err := lib.check(); err != nil {
		return nil, err
	}
	unlock, err := Lock(d, lib.Alias)
	if err != nil {
		return nil, err
	}
	defer unlock()
	dir := Dir(d, lib.Alias)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	removeLeftovers(dir)
	staging, err := os.MkdirTemp(dir, "staging-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging) // a no-op once it became current/ or pending/
	commit, err := clone(ctx, run, lib, staging)
	if err != nil {
		return nil, err
	}

	var out *FetchResult
	err = update(func(t *signing.Trust) (bool, error) {
		res, err := signing.Verify(ctx, run, staging, t)
		if err != nil {
			return false, err
		}
		if lib.ID != "" && res.Library != "" && res.Library != lib.ID {
			return false, fmt.Errorf("%s fetched the index of %s, not %s, the library it was added as; refused",
				lib.Alias, safefile.Printable(res.Library), lib.ID)
		}
		out = &FetchResult{Status: res.Status, Commit: commit, Serial: res.Serial, Reason: res.Reason, Verify: res}
		for _, p := range res.Packages {
			if p.Signed {
				out.Packages++
			}
		}
		now := time.Now().UTC().Truncate(time.Second)
		switch res.Status {
		case signing.StatusTrusted:
			err := install(d, lib.Alias, staging, &Record{
				ID: res.Library, Commit: commit, Serial: res.Serial, Status: res.Status, Fetched: now, Signer: res.Fingerprint,
			})
			return err == nil, err
		case signing.StatusUnknownSigner, signing.StatusRotation:
			out.Pending = true
			return false, wait(d, lib.Alias, staging, now, &Pending{
				Library: res.Library, Commit: commit, Serial: res.Serial, Status: res.Status,
				Fingerprint: res.Fingerprint, NewFingerprint: res.NewFingerprint, KnownLibrary: res.KnownLibrary,
			})
		}
		return false, verdict(d, lib.Alias, res.Status, res.Reason, now)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Promote makes the alias's pending/ its current/, once library trust has
// verified it as trusted. The caller holds Lock(d, alias).
func Promote(d machine.Dirs, alias string) error {
	p, err := ReadPending(d, alias)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("library %s has no fetch waiting for trust", alias)
	}
	if err != nil {
		return err
	}
	signer := p.Fingerprint
	if p.Status == signing.StatusRotation {
		signer = p.NewFingerprint
	}
	rec := &Record{ID: p.Library, Commit: p.Commit, Serial: p.Serial, Status: signing.StatusTrusted, Signer: signer}
	if old, err := ReadRecord(d, alias); err == nil {
		rec.Fetched = old.Fetched
	}
	return install(d, alias, PendingDir(d, alias), rec)
}

// install makes src the alias's current/ and records rec, the id first:
// libraries.toml takes it from the first verified index and refuses
// another id later or one another alias has.
func install(d machine.Dirs, alias, src string, rec *Record) error {
	err := Update(d, func(c *Config) (bool, error) {
		lib := c.Library(alias)
		if lib == nil {
			return false, fmt.Errorf("no library %s", alias)
		}
		for _, o := range c.Libraries {
			if o.Alias != alias && o.ID == rec.ID {
				return false, fmt.Errorf("library %s is already added as %s", rec.ID, o.Alias)
			}
		}
		switch lib.ID {
		case rec.ID:
			return false, nil
		case "":
			lib.ID = rec.ID
			return true, nil
		}
		return false, fmt.Errorf("library %s is %s, not %s", alias, lib.ID, rec.ID)
	})
	if err != nil {
		return err
	}
	if err := replaceDir(src, CurrentDir(d, alias)); err != nil {
		return err
	}
	if err := WriteRecord(d, alias, rec); err != nil {
		return err
	}
	return dropPending(d, alias)
}

// wait makes staging the alias's pending/ and records the verdict.
func wait(d machine.Dirs, alias, staging string, now time.Time, p *Pending) error {
	if err := replaceDir(staging, PendingDir(d, alias)); err != nil {
		return err
	}
	p.Schema = Schema
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).Encode(p); err != nil {
		return err
	}
	if err := tomlfile.WriteAtomic(pendingPath(d, alias), b.Bytes(), 0o644); err != nil {
		return err
	}
	return record(d, alias, p.Status, "", now)
}

// verdict records a fetch that replaces nothing and drops a pending/.
func verdict(d machine.Dirs, alias string, s signing.Status, reason string, now time.Time) error {
	if err := dropPending(d, alias); err != nil {
		return err
	}
	return record(d, alias, s, reason, now)
}

// record updates fetch.toml's verdict, keeping what it says of current/.
func record(d machine.Dirs, alias string, s signing.Status, reason string, now time.Time) error {
	rec, err := ReadRecord(d, alias)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		rec = &Record{}
	case err != nil:
		return err
	}
	rec.Status, rec.Reason, rec.Fetched = s, reason, now
	return WriteRecord(d, alias, rec)
}

func dropPending(d machine.Dirs, alias string) error {
	if err := os.Remove(pendingPath(d, alias)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.RemoveAll(PendingDir(d, alias))
}

// replaceDir puts src at dst: dst, if any, is moved aside over an empty
// dir (rename(2) replaces one; os.Rename refuses) and removed after.
func replaceDir(src, dst string) error {
	old, err := os.MkdirTemp(filepath.Dir(dst), "old-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(old)
	if err := syscall.Rename(dst, old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return &os.LinkError{Op: "rename", Old: dst, New: old, Err: err}
	}
	if err := syscall.Rename(src, dst); err != nil {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
	}
	return nil
}

// removeLeftovers removes the staging-* and old-* dirs a killed fetch or
// promote left in dir; the caller holds the alias's lock.
func removeLeftovers(dir string) {
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if n := e.Name(); strings.HasPrefix(n, "staging-") || strings.HasPrefix(n, "old-") {
			os.RemoveAll(filepath.Join(dir, n))
		}
	}
}

// Remove removes the alias's directory and lock file; the caller holds
// Lock(d, alias).
func Remove(d machine.Dirs, alias string) error {
	if err := os.RemoveAll(Dir(d, alias)); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(d.Data, "libraries", alias+".lock")); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

var commitRe = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// blob is one file of the library's tree.
type blob struct{ path, oid string }

// clone writes the library files of lib's branch into staging and returns
// the commit: a shallow, blobless clone with no working tree in a temp dir
// always removed; only the package paths' blobs are fetched, and only
// once the tree passed treeFiles. No hook, LFS filter or prompt runs.
func clone(ctx context.Context, run sys.Runner, lib *Library, staging string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	tmp, err := sys.RuntimeTemp(tempPrefix, staleAfter)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	env, err := sys.GitNetEnv(ctx, run, os.Environ())
	if err != nil {
		return "", err
	}
	env = append(env, "GIT_LFS_SKIP_SMUDGE=1")
	repo := filepath.Join(tmp, "repo")
	git := func(stdin io.Reader, stdout io.Writer, args ...string) (sys.Result, error) {
		argv := append([]string{"--literal-pathspecs", "-c", "core.hooksPath=/dev/null", "-c", "protocol.ext.allow=never"}, sys.GitProtocols...)
		argv = append(argv, args...)
		return run.Run(ctx, sys.Cmd{Name: "git", Args: argv, Env: env, Timeout: fetchTimeout, Stdin: stdin, Stdout: stdout})
	}
	argv := []string{"clone", "--quiet", "--depth", "1", "--filter=blob:none", "--no-checkout"}
	if lib.Branch != "" {
		argv = append(argv, "--branch", lib.Branch)
	}
	if _, err := git(nil, nil, append(argv, "--", lib.URL, repo)...); err != nil {
		return "", fmt.Errorf("git clone %s: %w", lib.URL, err)
	}
	res, err := git(nil, nil, "-C", repo, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	commit := strings.TrimSpace(string(res.Stdout))
	if !commitRe.MatchString(commit) {
		return "", fmt.Errorf("git rev-parse HEAD: %q", safefile.Printable(commit))
	}
	var tree listing
	_, err = git(nil, &tree, "-C", repo, "ls-tree", "-r", "-z", "HEAD", "--", signing.IndexFile, signing.SigFile, "packs", "options", "agents", templatesDir)
	if tree.over {
		return "", fmt.Errorf("the library has more than %d files; refused", maxFiles)
	}
	if err != nil {
		return "", fmt.Errorf("git ls-tree: %w", err)
	}
	files, err := treeFiles(tree.b)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return commit, nil
	}
	var oids strings.Builder
	for _, f := range files {
		oids.WriteString(f.oid + "\n")
	}
	// One fetch for every blob: each one read lazily would be a round
	// trip to the remote.
	if _, err := git(strings.NewReader(oids.String()), nil, "-C", repo, "-c", "fetch.negotiationAlgorithm=noop",
		"fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--recurse-submodules=no", "--filter=blob:none", "--stdin", "origin"); err != nil {
		return "", fmt.Errorf("git fetch (the library's files): %w", err)
	}
	res, err = git(strings.NewReader(oids.String()), nil, "-C", repo, "cat-file", "--batch-check=%(objectname) %(objecttype) %(objectsize)")
	if err != nil {
		return "", fmt.Errorf("git cat-file --batch-check: %w", err)
	}
	if err := checkSizes(files, res.Stdout); err != nil {
		return "", err
	}
	for _, f := range files {
		var buf capped
		if _, err := git(nil, &buf, "-C", repo, "cat-file", "blob", f.oid); err != nil {
			return "", fmt.Errorf("git cat-file blob %s: %w", safefile.Printable(f.path), err)
		}
		if err := writeFile(staging, f.path, buf.b); err != nil {
			return "", err
		}
	}
	return commit, nil
}

// treeFiles is the files of `git ls-tree -r -z` output, refusing the tree
// unless each is a regular file (no symlink, no submodule) at a library
// path: library.toml, library.toml.sig, <kind>s/<name>/<file> with
// <file> on the package format's allowlist (D93), or templates/<name>/
// with template.CheckPath's files.
func treeFiles(out []byte) ([]blob, error) {
	var files []blob
	for ent := range bytes.SplitSeq(out, []byte{0}) {
		if len(ent) == 0 {
			continue
		}
		meta, p, ok := bytes.Cut(ent, []byte{'\t'})
		f := strings.Fields(string(meta)) // <mode> <type> <oid>
		path := safefile.Printable(string(p))
		switch {
		case !ok || len(f) != 3:
			return nil, fmt.Errorf("git ls-tree: unreadable entry %q", safefile.Printable(string(ent)))
		case f[0] == "120000":
			return nil, fmt.Errorf("%s is a symlink: a library holds only regular files; refused", path)
		case f[1] == "commit":
			return nil, fmt.Errorf("%s is a submodule: a library holds only regular files; refused", path)
		case f[1] != "blob" || f[0] != "100644" && f[0] != "100755":
			return nil, fmt.Errorf("%s has mode %s: a library holds only regular files; refused", path, f[0])
		}
		if err := checkPath(string(p)); err != nil {
			return nil, fmt.Errorf("%w; refused", err)
		}
		files = append(files, blob{string(p), f[2]})
	}
	return files, nil
}

func checkPath(p string) error {
	if p == signing.IndexFile || p == signing.SigFile {
		return nil
	}
	top, rest, _ := strings.Cut(p, "/")
	name, rel, ok := strings.Cut(rest, "/")
	manifest := manifests[top]
	if manifest == "" || !ok || !pack.ValidName(name) {
		return fmt.Errorf("%q is not a library path: library.toml, library.toml.sig or <packs|options|agents|templates>/<name>/<file>", safefile.Printable(p))
	}
	if top == templatesDir {
		if err := template.CheckPath(rel); err != nil {
			return fmt.Errorf("%s/%s/: %w", top, name, err)
		}
		return nil
	}
	if err := pack.CheckFilePath(rel, manifest); err != nil {
		return fmt.Errorf("%s/%s/: %w", top, name, err)
	}
	return nil
}

// checkSizes refuses a file over safefile.Limit, or more than maxTotal in
// all, from `git cat-file --batch-check` output for files.
func checkSizes(files []blob, out []byte) error {
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != len(files) {
		return fmt.Errorf("git cat-file --batch-check: %d lines for %d files", len(lines), len(files))
	}
	var total int64
	for i, l := range lines {
		f := strings.Fields(l)
		if len(f) != 3 || f[0] != files[i].oid || f[1] != "blob" {
			return fmt.Errorf("git cat-file --batch-check: %q for %s", safefile.Printable(l), safefile.Printable(files[i].path))
		}
		n, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil {
			return fmt.Errorf("git cat-file --batch-check: %q", safefile.Printable(l))
		}
		if n > safefile.Limit {
			return fmt.Errorf("%s is %d bytes, over %d; refused", safefile.Printable(files[i].path), n, safefile.Limit)
		}
		if total += n; total > maxTotal {
			return fmt.Errorf("the library's files are over %d bytes in all; refused", maxTotal)
		}
	}
	return nil
}

// writeFile writes rel (checked by treeFiles) under dir: 0644, a .sh 0755.
func writeFile(dir, rel string, data []byte) error {
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if strings.HasSuffix(rel, ".sh") {
		mode = 0o755
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chmod(p, mode) // past the umask
}

var (
	errOverLimit    = errors.New("blob over safefile.Limit")
	errTooManyFiles = errors.New("more files than a library may hold")
)

// listing keeps `git ls-tree -z` output and fails the write, which stops
// git, once it holds more than maxFiles entries or maxListing bytes: a
// hostile tree is refused without listing all of it.
type listing struct {
	b    []byte
	n    int
	over bool
}

func (l *listing) Write(p []byte) (int, error) {
	l.n += bytes.Count(p, []byte{0})
	if l.n > maxFiles || len(l.b)+len(p) > maxListing {
		l.over = true
		return 0, errTooManyFiles
	}
	l.b = append(l.b, p...)
	return len(p), nil
}

// capped keeps at most safefile.Limit bytes and fails the write past that.
type capped struct{ b []byte }

func (c *capped) Write(p []byte) (int, error) {
	if len(c.b)+len(p) > safefile.Limit {
		return 0, errOverLimit
	}
	c.b = append(c.b, p...)
	return len(p), nil
}
