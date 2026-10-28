package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/review"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/workspace"
)

const lockUsage = "usage: railswitch lock [--review | --accept <hash> [--restart]]"

// railswitch lock [--review | --accept <hash> [--restart]]: write
// stack.lock when nothing waits for review; show the review; accept it
// (D97).
func cmdLock(ctx context.Context, e *env, args []string) int {
	fset := e.flags("lock")
	show := fset.Bool("review", false, "print the review of what a lock would change; write nothing")
	accept := fset.String("accept", "", "accept the review with this hash (lock --review --json): lock, store, render")
	restart := fset.Bool("restart", false, "with --accept: restart the running worktrees the update changes")
	if code, ok := parse(fset, args); !ok {
		return code
	}
	if fset.NArg() != 0 || *show && isSet(fset, "accept") || *restart && *accept == "" || isSet(fset, "accept") && *accept == "" {
		return e.usageErr("%s", lockUsage)
	}
	root, err := workspace.Root(e.dir)
	if err != nil {
		return e.fail(err)
	}
	switch {
	case *show:
		doc, err := e.lockReview(ctx, root, true)
		if err != nil {
			return e.fail(err)
		}
		if e.json {
			if err := json.NewEncoder(e.stdout).Encode(doc); err != nil {
				return e.fail(err)
			}
			return 0
		}
		printReview(e.stdout, doc)
		return 0
	case *accept != "":
		doc, err := e.lockReview(ctx, root, true)
		if err != nil {
			return e.fail(err)
		}
		if doc.Hash != *accept {
			return e.fail(workspace.ErrReviewChanged)
		}
		if err := e.commit(ctx, root, nil, &workspace.Accept{All: true, IfHash: *accept, Run: e.run}); err != nil {
			return e.fail(err)
		}
		if *restart && e.restartAll(ctx, doc.Restart) != 0 {
			return exitWrittenNotRestarted
		}
		return 0
	}
	doc, err := e.lockReview(ctx, root, false)
	if err != nil {
		return e.fail(err)
	}
	// A first lock has nothing to review against, except a library
	// package, which is never locked unseen (D106).
	if len(doc.Packages) > 0 && (!doc.firstLock || doc.installs()) {
		printReview(e.stdout, doc)
		fmt.Fprintf(e.stderr, "railswitch: a package update waits for review; accept with: railswitch lock --accept %s\n", doc.Hash)
		return 1
	}
	if err := workspace.Lock(ctx, root, workspace.Accept{All: true, IfHash: doc.Hash, Run: e.run}); err != nil {
		return e.fail(err)
	}
	fmt.Fprintf(e.stdout, "wrote %s\n", filepath.Join(root, "stack.lock"))
	return 0
}

// reviewDoc is `lock --review --json` (docs/json.md), and the `install`
// of `settings preview --json` and `project new --dry-run --json`.
type reviewDoc struct {
	Hash     string      `json:"hash"`
	Flags    int         `json:"flags"`
	Packages []reviewPkg `json:"packages"`
	Restart  []string    `json:"restart"`

	firstLock bool // there is no stack.lock yet: nothing to review against
}

type reviewPkg struct {
	Ref          string        `json:"ref"`
	Kind         string        `json:"kind"`
	Status       string        `json:"status"`
	From         string        `json:"from"`
	To           string        `json:"to"`
	AlsoLockedIn []string      `json:"also_locked_in"`
	Files        []review.File `json:"files"`

	library bool
}

// installs reports whether doc adds a library package.
func (doc *reviewDoc) installs() bool {
	return slices.ContainsFunc(doc.Packages, func(p reviewPkg) bool { return p.library && p.Status == workspace.StatusAdded })
}

// lockReview is the review of what lock --accept would write for root;
// with restart it also lists the running worktrees the accept changes.
func (e *env) lockReview(ctx context.Context, root string, restart bool) (*reviewDoc, error) {
	pl, err := workspace.Review(ctx, root)
	if err != nil {
		return nil, err
	}
	doc, err := reviewDocOf(pl)
	if err != nil {
		return nil, err
	}
	cur, loadErr := workspace.Load(ctx, root, "")
	doc.firstLock = errors.Is(loadErr, config.ErrNoLock)
	if !restart || len(doc.Packages) == 0 || loadErr != nil {
		return doc, nil
	}
	// An unregistered project has no worktrees to restart.
	se, err := e.envFor(ctx, cur)
	if err != nil {
		return doc, nil
	}
	c, err := se.Compare(cur, pl.Next)
	if err != nil {
		fmt.Fprintf(e.stderr, "warning: the project as accepted does not render: %v\n", err)
		return doc, nil
	}
	doc.Restart = c.Restart
	return doc, nil
}

// reviewOf is pl as a review document, each package compared file by file
// and flagged; Restart is empty.
func reviewDocOf(pl *workspace.Plan) (*reviewDoc, error) {
	doc := &reviewDoc{Hash: workspace.ReviewHash(pl.Lock, pl.Template), Packages: []reviewPkg{}, Restart: []string{}}
	others := otherLocks(pl.Next)
	for _, c := range pl.Packages {
		files, err := review.Compare(c.Kind, c.Old, c.New)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", c.Kind, c.Ref, err)
		}
		p := reviewPkg{Ref: c.Ref.String(), Kind: c.Kind, Status: c.Status, From: c.From, To: c.To, AlsoLockedIn: []string{}, Files: files, library: c.Ref.Library != ""}
		for _, o := range others {
			if c.Kind == pack.KindTemplate {
				break // never locked
			}
			if e, ok := lockTable(o.lock, c.Kind)[lockKey(c.Kind, c.Ref)]; ok && c.To != "" && e.SHA256 == c.To && workspace.Pins(e, c.Ref, pl.Next.Catalog.Libraries) {
				p.AlsoLockedIn = append(p.AlsoLockedIn, o.prefix)
			}
		}
		for _, f := range files {
			doc.Flags += len(f.Flags)
		}
		doc.Packages = append(doc.Packages, p)
	}
	return doc, nil
}

// installReview is the Install review (D106) of project p (nil: root's
// project.toml) on cat (nil: root's catalog), with the library template
// tpl (nil: none): nil when it installs no library package and shows no
// template.
func installReview(ctx context.Context, root string, p *config.Project, cat *pack.Catalog, tpl *workspace.Template) (*reviewDoc, []pack.Ref, error) {
	pl, err := workspace.Install(ctx, root, p, cat, tpl)
	if err != nil || pl == nil {
		return nil, nil, err
	}
	doc, err := reviewDocOf(pl)
	return doc, pl.Install, err
}

// acceptInstall is the Accept of a write that installs what the Install
// review of p (as installReview) lists, when its hash is hash: none
// ("") accepts nothing; a hash that differs, or one given for a write that
// installs nothing, refuses.
func (e *env) acceptInstall(ctx context.Context, root string, p *config.Project, cat *pack.Catalog, hash string) (*workspace.Accept, error) {
	if hash == "" {
		return &workspace.Accept{}, nil
	}
	doc, refs, err := installReview(ctx, root, p, cat, nil)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, errors.New("--accept-install: the change installs no library package")
	}
	if doc.Hash != hash {
		return nil, errInstallChanged
	}
	return &workspace.Accept{Install: refs, IfHash: hash, Run: e.run}, nil
}

// refList is refs as "a, b".
func refList(refs []pack.Ref) string {
	s := make([]string, len(refs))
	for i, r := range refs {
		s[i] = r.String()
	}
	return strings.Join(s, ", ")
}

var errInstallChanged = errors.New("the install changed since it was reviewed: review it again")

type otherLock struct {
	prefix string
	lock   *config.Lock
}

// otherLocks are the stack.locks of every other registered project, by
// registry order; one that can't be read is left out.
func otherLocks(w *workspace.Workspace) []otherLock {
	reg, err := machine.Read(w.Dirs.Data)
	if err != nil {
		return nil
	}
	var out []otherLock
	for _, pe := range reg.Projects {
		if pe.Prefix == w.Project.Project.Prefix {
			continue
		}
		if l, err := config.ReadLock(filepath.Join(pe.Workspace, "stack.lock")); err == nil {
			out = append(out, otherLock{pe.Prefix, l})
		}
	}
	return out
}

// lockTable is l's table of a package kind.
func lockTable(l *config.Lock, kind string) map[string]config.LockEntry {
	switch kind {
	case pack.KindPack:
		return l.Packs
	case pack.KindOption:
		return l.Options
	case pack.KindTool:
		return l.Tools
	case pack.KindBase:
		return l.Base
	}
	return l.Agents
}

// lockKey is how stack.lock keys r: agents by ref, the agent base image
// as "agent", the rest by name.
func lockKey(kind string, r pack.Ref) string {
	switch kind {
	case pack.KindAgent:
		return r.String()
	case pack.KindBase:
		return "agent"
	}
	return r.Name
}

// printReview prints doc as text: each package, each file with its flags
// marked !, then its diff.
func printReview(w io.Writer, doc *reviewDoc) {
	if len(doc.Packages) == 0 {
		fmt.Fprintln(w, "nothing to review: stack.lock is up to date")
		return
	}
	for _, p := range doc.Packages {
		fmt.Fprintf(w, "%s %s %s", p.Status, p.Kind, safefile.Printable(p.Ref))
		if p.From != "" || p.To != "" {
			fmt.Fprintf(w, " %s -> %s", short(p.From), short(p.To))
		}
		fmt.Fprintln(w)
		if len(p.AlsoLockedIn) > 0 {
			fmt.Fprintf(w, "  also locked in %s\n", safefile.Printable(strings.Join(p.AlsoLockedIn, ", ")))
		}
		for _, f := range p.Files {
			fmt.Fprintf(w, "  %s %s\n", f.Status, safefile.Printable(f.Path))
			for _, fl := range f.Flags {
				where := ""
				if fl.Line > 0 {
					where = fmt.Sprintf(" line %d", fl.Line)
				}
				fmt.Fprintf(w, "  ! %s%s: %s\n", fl.Check, where, safefile.Printable(fl.Text))
			}
			if f.Diff == "" {
				fmt.Fprintln(w, "    (empty)")
			}
			for _, line := range strings.SplitAfter(f.Diff, "\n") {
				if line != "" {
					fmt.Fprintf(w, "    %s\n", safefile.Printable(strings.TrimSuffix(line, "\n")))
				}
			}
		}
	}
	fmt.Fprintf(w, "%d flagged\n", doc.Flags)
}

// short is a content hash's first 12 characters, "-" for none.
func short(sha string) string {
	if sha == "" {
		return "-"
	}
	return sha[:min(12, len(sha))]
}
