package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
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
		if err := e.commit(ctx, root, nil, &workspace.Accept{All: true, IfHash: *accept}); err != nil {
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
	if len(doc.Packages) > 0 && !doc.firstLock {
		printReview(e.stdout, doc)
		fmt.Fprintf(e.stderr, "railswitch: a package update waits for review; accept with: railswitch lock --accept %s\n", doc.Hash)
		return 1
	}
	if err := workspace.Lock(ctx, root, workspace.Accept{All: true, IfHash: doc.Hash}); err != nil {
		return e.fail(err)
	}
	fmt.Fprintf(e.stdout, "wrote %s\n", filepath.Join(root, "stack.lock"))
	return 0
}

// reviewDoc is `lock --review --json` (docs/json.md).
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
}

// lockReview is the review of what lock --accept would write for root;
// with restart it also lists the running worktrees the accept changes.
func (e *env) lockReview(ctx context.Context, root string, restart bool) (*reviewDoc, error) {
	pl, err := workspace.Review(ctx, root)
	if err != nil {
		return nil, err
	}
	doc := &reviewDoc{Hash: workspace.Hash(pl.Lock), Packages: []reviewPkg{}, Restart: []string{}}
	cur, loadErr := workspace.Load(ctx, root, "")
	doc.firstLock = errors.Is(loadErr, config.ErrNoLock)
	others := otherLocks(pl.Next)
	for _, c := range pl.Packages {
		files, err := review.Compare(c.Kind, c.Old, c.New)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", c.Kind, c.Ref, err)
		}
		p := reviewPkg{Ref: c.Ref.String(), Kind: c.Kind, Status: c.Status, From: c.From, To: c.To, AlsoLockedIn: []string{}, Files: files}
		for _, o := range others {
			if e, ok := lockTable(o.lock, c.Kind)[lockKey(c.Kind, c.Ref)]; ok && c.To != "" && e.SHA256 == c.To && e.Version == strconv.Itoa(c.Ref.Version) && (e.Source == "local") == c.Ref.Local {
				p.AlsoLockedIn = append(p.AlsoLockedIn, o.prefix)
			}
		}
		for _, f := range files {
			doc.Flags += len(f.Flags)
		}
		doc.Packages = append(doc.Packages, p)
	}
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
	}
	return l.Agents
}

// lockKey is how stack.lock keys r: agents by ref, the rest by name.
func lockKey(kind string, r pack.Ref) string {
	if kind == pack.KindAgent {
		return r.String()
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
		fmt.Fprintf(w, "%s %s %s", p.Status, p.Kind, p.Ref)
		if p.From != "" || p.To != "" {
			fmt.Fprintf(w, " %s -> %s", short(p.From), short(p.To))
		}
		fmt.Fprintln(w)
		if len(p.AlsoLockedIn) > 0 {
			fmt.Fprintf(w, "  also locked in %s\n", strings.Join(p.AlsoLockedIn, ", "))
		}
		for _, f := range p.Files {
			fmt.Fprintf(w, "  %s %s\n", f.Status, f.Path)
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
