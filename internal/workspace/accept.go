package workspace

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing/fstest"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/lockstore"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/pack"
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
		e, ok := table(lock, x.kind)[x.key]
		if slices.Contains(keep, x.ref) || slices.Contains(pending, x.ref) || !ok || !pins(e, x.ref) {
			continue
		}
		if files, err := catalogFiles(cat, x.kind, x.ref); err == nil && files != nil {
			if sum, err := pack.ContentHash(files); err == nil && sum == e.SHA256 {
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
	}
	a, err := cat.Agent(r)
	if a == nil {
		return nil, err
	}
	return a.Files, err
}

// Accept is what a lock write may accept of the packages whose content
// changed under the ref stack.lock pins (D97): every one (lock --accept),
// one file of one package (pack file write: the file it writes, reviewed
// in the editor), or none (every other write).
type Accept struct {
	All bool
	// Edit, when set, accepts its package only when the file it names is
	// the package's one difference from the accepted copy; any other
	// difference refuses the write.
	Edit *FileEdit
	// IfHash, when set, refuses a write whose stack.lock bytes don't hash
	// (Hash) to it: the review that was accepted.
	IfHash string
}

// FileEdit is one file of package Ref, by its package-relative slash path.
type FileEdit struct {
	Ref  pack.Ref
	Path string
}

// onlyEdited refuses files, the resolved content of a package stack.lock
// pins at sha, unless putting edit's file back to the store's copy of sha
// gives exactly sha: nothing else of the package changed unreviewed.
func onlyEdited(s lockstore.Store, sha string, files fs.FS, edit FileEdit) error {
	refuse := fmt.Errorf("other files of %s changed since it was accepted: review them first (railswitch lock --review)", edit.Ref)
	stored, err := s.Get(sha)
	if err != nil {
		return refuse
	}
	mem, err := lockstore.Read(files)
	if err != nil {
		return err
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
// content changed under its locked ref is locked as resolved only when
// accept accepts it; otherwise its entry stays, and a stored copy that
// can't run it fails the write. Every package the new lock pins is stored
// first (lockstore). It needs no existing lock.
func Lock(_ context.Context, root string, accept Accept) error {
	pl, err := plan(root, accept)
	if err != nil {
		return err
	}
	data, err := pl.lock.Encode()
	if err != nil {
		return err
	}
	if accept.IfHash != "" && Hash(data) != accept.IfHash {
		return ErrReviewChanged
	}
	for _, sp := range pl.put {
		if _, err := pl.store.Put(sp.files); err != nil {
			return fmt.Errorf("store %s %s: %w", sp.kind, sp.ref, err)
		}
	}
	return pl.lock.Write(filepath.Join(root, lockFile))
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
}

func plan(root string, accept Accept) (*lockPlan, error) {
	p, err := loadProject(root)
	if err != nil {
		return nil, err
	}
	cat, err := pack.LoadCatalog(root)
	if err != nil {
		return nil, err
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
	l.Packs, l.Options, l.Agents = nil, nil, nil
	for _, sp := range stackPackages(st) {
		e, err := sp.entry()
		if err != nil {
			return nil, err
		}
		if o, ok := lockedAt(old, sp); ok && o.SHA256 != e.SHA256 && !accept.All {
			if accept.Edit != nil && accept.Edit.Ref == sp.ref {
				if err := onlyEdited(pl.store, o.SHA256, sp.files, *accept.Edit); err != nil {
					return nil, err
				}
			} else {
				// The accepted copy keeps the project running; without
				// one, locking the update would accept it unseen.
				if _, err := accepted(cat, pl.store, sp.kind, sp.ref, o.SHA256); err != nil {
					return nil, fmt.Errorf("%s %s: %s", sp.kind, sp.ref, reviewHint)
				}
				table(&l, sp.kind)[sp.key] = o
				continue
			}
		}
		table(&l, sp.kind)[sp.key] = e
		pl.put = append(pl.put, sp)
	}
	l.Removed = dropped(old, &l)
	pl.lock = &l
	return pl, nil
}

// lockedAt is what old accepted for sp's ref: its table's entry, or the
// entry a write dropped (Removed) when the ref left the lock since.
func lockedAt(old *config.Lock, sp stackPkg) (config.LockEntry, bool) {
	if o, ok := table(old, sp.kind)[sp.key]; ok && pins(o, sp.ref) {
		return o, true
	}
	for _, r := range old.Removed {
		if r.Kind == sp.kind && r.Key == sp.key && pins(r.Entry(), sp.ref) {
			return r.Entry(), true
		}
	}
	return config.LockEntry{}, false
}

// dropped is the Removed list of l, written over old: every entry of old's
// tables l no longer pins, then old's Removed, newest first, one per ref,
// none that l pins again, at most config.MaxRemoved.
func dropped(old, l *config.Lock) []config.LockRemoved {
	same := func(a, b config.LockRemoved) bool {
		return a.Kind == b.Kind && a.Key == b.Key && a.Version == b.Version && (a.Source == localSource) == (b.Source == localSource)
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
	for _, kind := range []string{pack.KindPack, pack.KindOption, pack.KindAgent} {
		t := table(old, kind)
		for _, key := range slices.Sorted(maps.Keys(t)) {
			e := t[key]
			add(config.LockRemoved{Kind: kind, Key: key, Version: e.Version, Source: e.Source, SHA256: e.SHA256})
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
}

// Review is the review of an accept against root's current stack.lock
// (none: every package added), resolved without any accepted copy.
func Review(_ context.Context, root string) (*Plan, error) {
	pl, err := plan(root, Accept{All: true})
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
		r, err := lockedRef(kind, key, o)
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
		inTable := ok && pins(o, sp.ref)
		if ok && !inTable {
			removed(sp.kind, sp.key, o)
		}
		// A ref back in the lock is reviewed against what it last accepted.
		prev, had := lockedAt(old, sp)
		switch {
		case inTable && prev.SHA256 == e.SHA256:
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
	for _, kind := range []string{pack.KindPack, pack.KindOption, pack.KindAgent} {
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

// lockedRef is the ref a lock entry pins: packs and options are keyed by
// name, agents by ref.
func lockedRef(kind, key string, e config.LockEntry) (pack.Ref, error) {
	if kind == pack.KindAgent {
		return pack.ParseRef(key)
	}
	v, err := strconv.Atoi(e.Version)
	if err != nil {
		return pack.Ref{}, err
	}
	return pack.Ref{Name: key, Version: v, Local: e.Source == localSource}, nil
}

// ReviewPending reports whether stack.lock leaves an update to review:
// a package running its accepted copy, or a lock CheckLock refuses.
func (w *Workspace) ReviewPending() bool {
	return len(w.Pending) > 0 || errors.Is(w.CheckLock(), ErrStaleLock)
}
