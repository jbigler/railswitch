package workspace

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing/fstest"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/lockstore"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/signing"
	"github.com/jbigler/railswitch/internal/sys"
)

// reviewHint ends every error about package content stack.lock has not
// accepted.
const reviewHint = "an update is waiting for review: railswitch lock --review"

func store(d machine.Dirs) lockstore.Store { return lockstore.Store{Dir: lockstore.Dir(d)} }

// runAccepted is cat with each package of p whose content differs from
// what lock pins at the same ref replaced by the store's copy of the pinned
// content, and the refs replaced, sorted. A package in keep, a ref the
// lock doesn't pin, or a copy that is missing or doesn't load stays as
// resolved (CheckLock then refuses it).
func runAccepted(cat *pack.Catalog, p *config.Project, lock *config.Lock, s lockstore.Store, keep []pack.Ref) (*pack.Catalog, []pack.Ref) {
	var pending []pack.Ref
	for _, x := range projectPackages(p) {
		e, ok := lockedEntry(lock, x.kind, x.key, x.ref)
		if slices.Contains(keep, x.ref) || slices.Contains(pending, x.ref) || !ok {
			continue
		}
		if files, err := catalogFiles(cat, x.kind, x.ref); err == nil && files != nil {
			if sum, err := pack.ContentHash(files); err == nil && sum == e.SHA256 {
				// A lock from before the store has no copies: store what
				// it still runs, best effort, so a later update can be
				// reviewed against it.
				if _, err := os.Lstat(filepath.Join(s.Dir, sum)); errors.Is(err, fs.ErrNotExist) {
					_, _ = s.Put(files)
				}
				continue
			}
		}
		next, err := accepted(cat, s, x.kind, x.ref, e.SHA256)
		if err != nil {
			continue
		}
		cat, pending = next, append(pending, x.ref)
	}
	slices.SortFunc(pending, func(a, b pack.Ref) int { return strings.Compare(a.String(), b.String()) })
	return cat, pending
}

// Accepted is cat with each package of p whose content changed under the
// ref root's stack.lock pins replaced by the store's accepted copy, as Load
// does; cat itself when root has no stack.lock. Whatever copies package
// text out to the user's files (an eject) reads this catalog, so an
// unreviewed update never reaches a build that way.
func Accepted(root string, p *config.Project, cat *pack.Catalog) (*pack.Catalog, error) {
	lock, err := config.ReadLock(filepath.Join(root, lockFile))
	if errors.Is(err, config.ErrNoLock) {
		return cat, nil
	}
	if err != nil {
		return nil, err
	}
	dirs, err := machine.DefaultDirs()
	if err != nil {
		return nil, err
	}
	cat, _ = runAccepted(cat, p, lock, store(dirs), nil)
	return cat, nil
}

// accepted is cat with the store's copy sha as the package of kind at r.
func accepted(cat *pack.Catalog, s lockstore.Store, kind string, r pack.Ref, sha string) (*pack.Catalog, error) {
	files, err := s.Get(sha)
	if err != nil {
		return nil, err
	}
	return cat.WithPackage(kind, r, files)
}

// projectPackages are the packages p's project.toml names, as stack.lock
// keys them, whether or not they load; a ref that doesn't parse is left out.
func projectPackages(p *config.Project) []stackPkg {
	var out []stackPkg
	if r, err := pack.ParseRef(p.Stack.Pack); err == nil {
		out = append(out, stackPkg{kind: pack.KindPack, key: r.Name, ref: r})
	}
	for _, k := range slices.Sorted(maps.Keys(p.Options)) {
		if r, err := pack.ParseRef(p.Options[k]); err == nil {
			out = append(out, stackPkg{kind: pack.KindOption, key: r.Name, ref: r})
		}
	}
	for _, k := range slices.Sorted(maps.Keys(p.Agents)) {
		if r, err := pack.ParseRef(p.Agents[k].Use); err == nil {
			out = append(out, stackPkg{kind: pack.KindAgent, key: r.String(), ref: r})
		}
	}
	if p.Tools.Nvim != nil {
		out = append(out, stackPkg{kind: pack.KindTool, key: "nvim", ref: pack.Ref{Name: "nvim"}})
	}
	if len(p.Agents) > 0 {
		out = append(out, stackPkg{kind: pack.KindBase, key: baseKey, ref: pack.AgentBaseRef})
	}
	return out
}

// catalogFiles is the files of the package of kind at r; nil when the
// catalog has none.
func catalogFiles(cat *pack.Catalog, kind string, r pack.Ref) (fs.FS, error) {
	switch kind {
	case pack.KindPack:
		p, err := cat.Pack(r)
		if p == nil {
			return nil, err
		}
		return p.Files, err
	case pack.KindOption:
		o, err := cat.Option(r)
		if o == nil {
			return nil, err
		}
		return o.Files, err
	case pack.KindTool:
		if t := cat.Tools[r.Name]; t != nil {
			return t.Files, nil
		}
		return nil, nil
	case pack.KindBase:
		return cat.AgentBase, nil
	}
	a, err := cat.Agent(r)
	if a == nil {
		return nil, err
	}
	return a.Files, err
}

// Accept is what a lock write may accept of the packages whose content
// changed under the ref stack.lock pins (D97), and of the library packages
// new to it (D106): every one (lock --accept), the library packages an
// Install review listed (settings apply and project new with
// --accept-install), one file of one package (pack file write: the file it
// writes, reviewed in the editor), or none (every other write).
type Accept struct {
	All bool
	// Install accepts these library refs when they are new to the lock;
	// any other library ref new to it refuses the write.
	Install []pack.Ref
	// Edit, when set, accepts its package only when the file it names is
	// the package's one difference from the accepted copy; any other
	// difference refuses the write.
	Edit *FileEdit
	// IfHash, when set, refuses a write whose stack.lock bytes don't hash
	// (Hash) to it: the review that was accepted.
	IfHash string
	// Run runs ssh-keygen when the write accepts a library package: its
	// library's checkout is verified again against today's trust first.
	Run sys.Runner
}

// accepts reports whether a accepts sp's content: new to the lock (had
// false) or changed under the ref it pins.
func (a Accept) accepts(sp stackPkg, had bool) bool {
	return a.All || !had && slices.Contains(a.Install, sp.ref)
}

// FileEdit is one file of the package of Kind at Ref, by its
// package-relative slash path.
type FileEdit struct {
	Kind string
	Ref  pack.Ref
	Path string
}

// onlyEdited refuses files, the resolved content (hashing to next) of a
// package stack.lock pins at sha, unless putting edit's file back to the
// store's copy of sha gives exactly sha: nothing else of the package
// changed unreviewed. The bytes judged must hash to next, so a file changed
// after the lock hashed it is refused too.
func onlyEdited(s lockstore.Store, sha, next string, files fs.FS, edit FileEdit) error {
	refuse := fmt.Errorf("other files of %s changed since it was accepted: review them first (railswitch lock --review)", edit.Ref)
	stored, err := s.Get(sha)
	if err != nil {
		return refuse
	}
	mem, err := lockstore.Read(files)
	if err != nil {
		return err
	}
	if sum, err := pack.ContentHash(mem); err != nil || sum != next {
		return refuse
	}
	if data, err := fs.ReadFile(stored, edit.Path); err == nil {
		mem[edit.Path] = &fstest.MapFile{Data: data}
	} else {
		delete(mem, edit.Path)
	}
	if sum, err := pack.ContentHash(mem); err != nil || sum != sha {
		return refuse
	}
	return nil
}

// ErrReviewChanged is Lock's error when Accept.IfHash no longer matches.
var ErrReviewChanged = errors.New("the update changed since it was reviewed: review it again")

// Lock loads and validates root's project.toml, resolves its stack and
// writes <root>/stack.lock: every pack, option and agent with its content
// hash, keeping the image digests of an existing lock. A package whose
// content changed under its locked ref, or a library package new to the
// lock, is locked as resolved only when accept accepts it; otherwise a
// changed package's entry stays (a stored copy that can't run it fails the
// write) and a new library package refuses the write. Each library whose
// packages it accepts is verified again first (Accept.Run). Every package
// the new lock pins is stored first (lockstore). It needs no existing lock.
func Lock(ctx context.Context, root string, accept Accept) error {
	pl, err := plan(root, nil, nil, accept)
	if err != nil {
		return err
	}
	return pl.write(ctx, accept)
}

// write stores pl's packages and writes its stack.lock, refusing bytes
// that don't hash to accept.IfHash (when set), a library whose checkout no
// longer verifies (reverify), and a package whose content changed since pl
// hashed it (an edit meanwhile: what was stored is not what the lock would
// pin).
func (pl *lockPlan) write(ctx context.Context, accept Accept) error {
	data, err := pl.lock.Encode()
	if err != nil {
		return err
	}
	if accept.IfHash != "" && Hash(data) != accept.IfHash {
		return ErrReviewChanged
	}
	if err := pl.reverify(ctx, accept.Run); err != nil {
		return err
	}
	for _, sp := range pl.put {
		sha, err := pl.store.Put(sp.files)
		if err != nil {
			return fmt.Errorf("store %s %s: %w", sp.kind, sp.ref, err)
		}
		if sha != table(pl.lock, sp.kind)[sp.key].SHA256 {
			return fmt.Errorf("%s %s changed while it was being locked: lock again", sp.kind, sp.ref)
		}
	}
	return pl.lock.Write(filepath.Join(pl.root, lockFile))
}

// lockPlan is a lock write worked out: the lock it writes, the packages
// it stores, and what it was worked out from.
type lockPlan struct {
	root  string
	p     *config.Project
	cat   *pack.Catalog // as resolved: no accepted copies swapped in
	st    *pack.Stack
	old   *config.Lock // the current stack.lock; nil when there is none
	lock  *config.Lock
	put   []stackPkg
	dirs  machine.Dirs
	store lockstore.Store
	// verify are the library packages the write accepts: new to the lock
	// or changed under it.
	verify []stackPkg
}

// plan works out the lock write of project p (nil: root's project.toml)
// on catalog cat (nil: root's, LoadCatalog) against root's stack.lock.
func plan(root string, p *config.Project, cat *pack.Catalog, accept Accept) (*lockPlan, error) {
	var err error
	if p == nil {
		if p, err = loadProject(root); err != nil {
			return nil, err
		}
	}
	if cat == nil {
		if cat, err = LoadCatalog(root); err != nil {
			return nil, err
		}
	}
	st, err := cat.Resolve(p)
	if err != nil {
		return nil, err
	}
	dirs, err := machine.DefaultDirs()
	if err != nil {
		return nil, err
	}
	pl := &lockPlan{root: root, p: p, cat: cat, st: st, dirs: dirs, store: store(dirs)}
	old, err := config.ReadLock(filepath.Join(root, lockFile))
	switch {
	case errors.Is(err, config.ErrNoLock):
		old = &config.Lock{}
	case err != nil:
		return nil, err
	default:
		pl.old = old
	}
	l := *old
	l.Packs, l.Options, l.Agents, l.Tools, l.Base = nil, nil, nil, nil, nil
	for _, sp := range stackPackages(st) {
		e, err := sp.entry()
		if err != nil {
			return nil, err
		}
		o, had := lockedAt(old, sp)
		switch {
		case !had && sp.ref.Library != "" && !accept.accepts(sp, had):
			// A library package is never locked unseen, even the first
			// time (D106).
			return nil, fmt.Errorf("installing %s needs review: %s", sp.ref, installHint)
		case had && o.SHA256 != e.SHA256 && !accept.accepts(sp, had):
			if accept.Edit != nil && accept.Edit.Kind == sp.kind && accept.Edit.Ref == sp.ref {
				if err := onlyEdited(pl.store, o.SHA256, e.SHA256, sp.files, *accept.Edit); err != nil {
					return nil, err
				}
				break
			}
			// The accepted copy keeps the project running; without one,
			// locking the update would accept it unseen.
			if _, err := accepted(cat, pl.store, sp.kind, sp.ref, o.SHA256); err != nil {
				return nil, fmt.Errorf("%s %s: %s", sp.kind, sp.ref, reviewHint)
			}
			table(&l, sp.kind)[sp.key] = o
			continue
		case sp.ref.Library != "" && (!had || o.SHA256 != e.SHA256):
			pl.verify = append(pl.verify, sp)
		}
		table(&l, sp.kind)[sp.key] = e
		pl.put = append(pl.put, sp)
	}
	l.Removed = dropped(old, &l)
	pl.lock = &l
	return pl, nil
}

// installHint ends the error of a write that would install a library
// package unreviewed.
const installHint = "railswitch settings preview shows the install review; accept it with settings apply --accept-install <hash> (or railswitch lock --review, then lock --accept <hash>)"

// reverify verifies again, against today's trust, the checkout of every
// library pl accepts a package of (one ssh-keygen call each): a library
// whose signer was distrusted or revoked since its fetch can't be
// installed or updated. Each accepted package must still be signed, by the
// key its fetch recorded.
func (pl *lockPlan) reverify(ctx context.Context, run sys.Runner) error {
	if len(pl.verify) == 0 {
		return nil
	}
	if run == nil {
		return fmt.Errorf("accepting %s needs its library verified again: no runner", pl.verify[0].ref)
	}
	trust, err := signing.LoadTrust(filepath.Join(pl.dirs.Config, signing.TrustFile))
	if err != nil {
		return err
	}
	results := map[string]*signing.Result{}
	for _, sp := range pl.verify {
		alias := sp.ref.Library
		res, ok := results[alias]
		if !ok {
			co, err := pl.cat.Libraries.Checkout(alias)
			if err != nil {
				return err
			}
			if res, err = signing.Verify(ctx, run, co.Dir, trust); err != nil {
				return fmt.Errorf("verify library %s: %w", alias, err)
			}
			results[alias] = res
		}
		if res.Status != signing.StatusTrusted {
			return fmt.Errorf("the signer of %s is no longer trusted (%s): %s can't be installed or updated", alias, res.Status, sp.ref)
		}
		e := table(pl.lock, sp.kind)[sp.key]
		if res.Fingerprint != e.Signer {
			return fmt.Errorf("library %s is signed by %s now, not by %s as fetched: fetch it again", alias, res.Fingerprint, e.Signer)
		}
		if !slices.ContainsFunc(res.Packages, func(p signing.PackageResult) bool {
			return p.Signed && p.Kind == sp.kind && p.Ref == sp.ref.Bare().String()
		}) {
			return fmt.Errorf("%s is no longer signed by library %s: fetch it again", sp.ref, alias)
		}
	}
	return nil
}

// lockedAt is what old accepted for sp's ref: its table's entry, or the
// entry a write dropped (Removed) when the ref left the lock since.
func lockedAt(old *config.Lock, sp stackPkg) (config.LockEntry, bool) {
	return lockedEntry(old, sp.kind, sp.key, sp.ref)
}

// lockedEntry is what lock accepted for ref r of kind under key: its
// table's entry, or the entry a write dropped (Removed). Load, ejects and
// Lock all ask it, so a ref back from Removed runs and ejects its accepted
// copy, as Lock keeps it.
func lockedEntry(lock *config.Lock, kind, key string, r pack.Ref) (config.LockEntry, bool) {
	if o, ok := table(lock, kind)[key]; ok && Pins(o, r) {
		return o, true
	}
	for _, rm := range lock.Removed {
		if rm.Kind == kind && rm.Key == key && Pins(rm.Entry(), r) {
			return rm.Entry(), true
		}
	}
	return config.LockEntry{}, false
}

// dropped is the Removed list of l, written over old: every entry of old's
// tables l no longer pins, then old's Removed, newest first, one per ref,
// none that l pins again, at most config.MaxRemoved.
func dropped(old, l *config.Lock) []config.LockRemoved {
	same := func(a, b config.LockRemoved) bool {
		return a.Kind == b.Kind && a.Key == b.Key && a.Version == b.Version && sourceKind(a.Source) == sourceKind(b.Source)
	}
	var out []config.LockRemoved
	add := func(r config.LockRemoved) {
		if e, ok := table(l, r.Kind)[r.Key]; ok && same(r, config.LockRemoved{Kind: r.Kind, Key: r.Key, Version: e.Version, Source: e.Source}) {
			return
		}
		if len(out) < config.MaxRemoved && !slices.ContainsFunc(out, func(x config.LockRemoved) bool { return same(x, r) }) {
			out = append(out, r)
		}
	}
	for _, kind := range lockKinds {
		t := table(old, kind)
		for _, key := range slices.Sorted(maps.Keys(t)) {
			e := t[key]
			add(config.Removed(kind, key, e))
		}
	}
	for _, r := range old.Removed {
		add(r)
	}
	return out
}

// Change statuses of a review.
const (
	StatusChanged = "changed"
	StatusAdded   = "added"
	StatusRemoved = "removed"
	StatusFirst   = "first" // changed, with no stored copy of what was accepted
)

// Change is one package a lock --accept would lock differently.
type Change struct {
	Ref      pack.Ref
	Kind     string // pack.KindPack etc.
	Status   string
	From, To string // content hashes: the locked one ("" when added), the new one ("" when removed)
	Old, New fs.FS  // Old nil when added or first (or a removed package's copy is missing); New nil when removed
}

// Plan is what lock --accept would write: the exact stack.lock bytes, the
// packages it changes, and the project as it leaves it.
type Plan struct {
	Lock     []byte
	Packages []Change
	Next     *Workspace // the accepted project: as resolved, under the new lock
	// Install are the library refs an Install review accepts.
	Install []pack.Ref
}

// Review is the review of an accept against root's current stack.lock
// (none: every package added), resolved without any accepted copy.
func Review(_ context.Context, root string) (*Plan, error) {
	pl, err := plan(root, nil, nil, Accept{All: true})
	if err != nil {
		return nil, err
	}
	data, err := pl.lock.Encode()
	if err != nil {
		return nil, err
	}
	old := pl.old
	if old == nil {
		old = &config.Lock{}
	}
	stored := func(sha string) fs.FS {
		files, err := pl.store.Get(sha)
		if err != nil {
			return nil
		}
		return files
	}
	var changes []Change
	removed := func(kind, key string, o config.LockEntry) {
		r, err := lockedRef(kind, key, o, pl.cat.Libraries)
		if err != nil {
			return
		}
		changes = append(changes, Change{Ref: r, Kind: kind, Status: StatusRemoved, From: o.SHA256, Old: stored(o.SHA256)})
	}
	want := map[string]bool{}
	for _, sp := range stackPackages(pl.st) {
		want[sp.kind+" "+sp.key] = true
		e, err := sp.entry()
		if err != nil {
			return nil, err
		}
		o, ok := table(old, sp.kind)[sp.key]
		inTable := ok && Pins(o, sp.ref)
		if ok && !inTable {
			removed(sp.kind, sp.key, o)
		}
		// A ref back in the lock is reviewed against what it last accepted.
		prev, had := lockedAt(old, sp)
		switch {
		case inTable && prev.SHA256 == e.SHA256:
		case !had && (sp.kind == pack.KindTool || sp.kind == pack.KindBase):
			// Built in and run unlocked before D107: adopted as resolved
			// by the next lock write, not reviewed (CheckLock agrees).
		case had && prev.SHA256 != e.SHA256:
			c := Change{Ref: sp.ref, Kind: sp.kind, Status: StatusChanged, From: prev.SHA256, To: e.SHA256, Old: stored(prev.SHA256), New: sp.files}
			if c.Old == nil {
				c.Status = StatusFirst
			}
			changes = append(changes, c)
		default:
			changes = append(changes, Change{Ref: sp.ref, Kind: sp.kind, Status: StatusAdded, To: e.SHA256, New: sp.files})
		}
	}
	for _, kind := range lockKinds {
		t := table(old, kind)
		for _, key := range slices.Sorted(maps.Keys(t)) {
			if !want[kind+" "+key] {
				removed(kind, key, t[key])
			}
		}
	}
	next := &Workspace{Root: root, Project: pl.p, Catalog: pl.cat, Stack: pl.st, Lock: pl.lock, Dirs: pl.dirs}
	return &Plan{Lock: data, Packages: changes, Next: next}, nil
}

// Install is the install review of project p (nil: root's project.toml)
// on catalog cat (nil: root's) against root's stack.lock (D106): the
// library packages new to the lock, each added and compared against
// nothing, and the exact stack.lock the write that accepts just them
// (Accept.Install) leaves, whose Hash is the review's. nil when p installs
// no library package.
func Install(_ context.Context, root string, p *config.Project, cat *pack.Catalog) (*Plan, error) {
	pl, err := plan(root, p, cat, Accept{All: true})
	if err != nil {
		return nil, err
	}
	old := pl.old
	if old == nil {
		old = &config.Lock{}
	}
	var refs []pack.Ref
	var changes []Change
	for _, sp := range stackPackages(pl.st) {
		if _, had := lockedAt(old, sp); had || sp.ref.Library == "" {
			continue
		}
		e, err := sp.entry()
		if err != nil {
			return nil, err
		}
		refs = append(refs, sp.ref)
		changes = append(changes, Change{Ref: sp.ref, Kind: sp.kind, Status: StatusAdded, To: e.SHA256, New: sp.files})
	}
	if len(refs) == 0 {
		return nil, nil
	}
	ipl, err := plan(root, pl.p, pl.cat, Accept{Install: refs})
	if err != nil {
		return nil, err
	}
	data, err := ipl.lock.Encode()
	if err != nil {
		return nil, err
	}
	next := &Workspace{Root: root, Project: ipl.p, Catalog: ipl.cat, Stack: ipl.st, Lock: ipl.lock, Dirs: ipl.dirs}
	return &Plan{Lock: data, Packages: changes, Next: next, Install: refs}, nil
}

// lockedRef is the ref a lock entry pins: packs and options are keyed by
// name, agents by ref. A library package's alias is the one libs knows its
// library by; none is an error.
func lockedRef(kind, key string, e config.LockEntry, libs pack.Libraries) (pack.Ref, error) {
	switch kind {
	case pack.KindAgent:
		return pack.ParseRef(key)
	case pack.KindTool:
		return pack.Ref{Name: key}, nil
	case pack.KindBase:
		return pack.AgentBaseRef, nil
	}
	v, err := strconv.Atoi(e.Version)
	if err != nil {
		return pack.Ref{}, err
	}
	r := pack.Ref{Name: key, Version: v, Local: e.Source == localSource}
	if id, ok := LibraryID(e); ok {
		if libs != nil {
			r.Library = libs.Alias(id)
		}
		if r.Library == "" {
			return pack.Ref{}, fmt.Errorf("%s %s: no library on this machine has id %s", kind, key, id)
		}
	}
	return r, nil
}

// ReviewPending reports whether stack.lock leaves an update to review:
// a package running its accepted copy, or a lock CheckLock refuses.
func (w *Workspace) ReviewPending() bool {
	return len(w.Pending) > 0 || errors.Is(w.CheckLock(), ErrStaleLock)
}
