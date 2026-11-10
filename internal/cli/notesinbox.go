package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"text/tabwriter"
	"unicode/utf8"

	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/tomlfile"
	"github.com/jbigler/railswitch/internal/workspace"
)

const notesInboxUsage = "usage: railswitch [--json] notes inbox ls | [--json] notes inbox show <worktree> <name> | notes inbox accept <worktree> <name> --hash <hash> [--as <name>] | notes inbox reject <worktree> <name>"

// inboxRow is one element of `notes inbox ls --json` suggestions.
type inboxRow struct {
	Worktree string `json:"worktree"`
	noteRow
	Replaces bool `json:"replaces"` // a note of that name exists
}

// inboxDoc is `notes inbox show --json`.
type inboxDoc struct {
	Worktree string `json:"worktree"`
	noteDoc
	Note *noteDoc `json:"note"` // the note it would replace; null: none
}

// railswitch notes inbox ls|show|accept|reject (D197): the notes agents
// suggest, <ws>/.railswitch/notes-inbox/<slug>/. Agents write those
// folders, so they are read as notes are: regular files named as noteName,
// never through a symlink; accept writes the bytes it hashed.
func notesInbox(e *env, args []string) int {
	if len(args) == 0 {
		return e.usageErr("%s", notesInboxUsage)
	}
	switch args[0] {
	case "ls":
		return notesInboxLs(e, args[1:])
	case "show":
		return notesInboxShow(e, args[1:])
	case "accept":
		return notesInboxAccept(e, args[1:])
	case "reject":
		return notesInboxReject(e, args[1:])
	}
	return e.usageErr("notes inbox: unknown subcommand %q\n%s", args[0], notesInboxUsage)
}

// inboxRoot is the workspace's folder of inboxes.
func (e *env) inboxRoot() (string, error) {
	root, err := workspace.Root(e.dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, filepath.FromSlash(render.NotesInboxDir)), nil
}

// validSlug reports whether s is a worktree slug, as Sanitize makes them.
func validSlug(s string) bool {
	t, err := naming.Sanitize(s)
	return err == nil && t == s
}

// inboxArgs checks <worktree> <name> and returns the inbox folder.
func (e *env) inboxArgs(cmd, slug, name string) (string, int, bool) {
	if !validSlug(slug) {
		return "", e.usageErr("notes inbox %s: %q is not a worktree name", cmd, slug), false
	}
	if !noteName.MatchString(name) {
		return "", e.usageErr("notes inbox %s: %q is not a note name (letters, digits, . _ -, ending .md)", cmd, name), false
	}
	root, err := e.inboxRoot()
	if err != nil {
		return "", e.fail(err), false
	}
	return filepath.Join(root, slug), 0, true
}

func notesInboxLs(e *env, args []string) int {
	fset := e.flags("notes inbox ls")
	if code, ok := parse(fset, args); !ok {
		return code
	}
	if fset.NArg() != 0 {
		return e.usageErr("%s", notesInboxUsage)
	}
	root, err := e.inboxRoot()
	if err != nil {
		return e.fail(err)
	}
	notes, err := e.notesDir()
	if err != nil {
		return e.fail(err)
	}
	ents, err := os.ReadDir(root)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return e.fail(err)
	}
	rows := []inboxRow{}
	for _, d := range ents {
		if !d.IsDir() || !validSlug(d.Name()) { // a symlink's type is not a dir
			continue
		}
		ns, err := notesList(filepath.Join(root, d.Name()))
		if err != nil {
			return e.fail(err)
		}
		for _, n := range ns {
			fi, err := os.Lstat(filepath.Join(notes, n.Name))
			rows = append(rows, inboxRow{Worktree: d.Name(), noteRow: n, Replaces: err == nil && fi.Mode().IsRegular()})
		}
	}
	slices.SortStableFunc(rows, func(a, b inboxRow) int { return b.mod.Compare(a.mod) })
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(struct {
			Suggestions []inboxRow `json:"suggestions"`
		}{rows}); err != nil {
			return e.fail(err)
		}
		return 0
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "WORKTREE\tNAME\tSIZE\tMODIFIED\tREPLACES")
	for _, r := range rows {
		rep := ""
		if r.Replaces {
			rep = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", r.Worktree, r.Name, r.Size, r.Modified, rep)
	}
	if err := tw.Flush(); err != nil {
		return e.fail(err)
	}
	return 0
}

func notesInboxShow(e *env, args []string) int {
	fset := e.flags("notes inbox show")
	a, code, ok := parseArgs(e, fset, args, 2, "usage: railswitch [--json] notes inbox show <worktree> <name>")
	if !ok {
		return code
	}
	dir, code, ok := e.inboxArgs("show", a[0], a[1])
	if !ok {
		return code
	}
	data, exists, err := readNote(dir, a[1])
	if err != nil {
		return e.fail(err)
	}
	if !exists {
		return e.fail(fmt.Errorf("no suggestion %s from %s", a[1], a[0]))
	}
	notes, err := e.notesDir()
	if err != nil {
		return e.fail(err)
	}
	cur, curExists, err := readNote(notes, a[1])
	if err != nil {
		return e.fail(err)
	}
	if e.json {
		doc := inboxDoc{Worktree: a[0], noteDoc: noteDoc{Name: a[1], Text: string(data), Hash: workspace.Hash(data)}}
		if curExists {
			doc.Note = &noteDoc{Name: a[1], Text: string(cur), Hash: workspace.Hash(cur)}
		}
		if err := json.NewEncoder(e.stdout).Encode(doc); err != nil {
			return e.fail(err)
		}
		return 0
	}
	fmt.Fprint(e.stdout, printableText(data))
	return 0
}

// railswitch notes inbox accept <worktree> <name> --hash <h> [--as <name>]:
// the suggestion becomes note <as> (default <name>), replacing one of that
// name, and leaves the inbox. --hash is what the user reviewed (inbox show
// --json): an agent's edit since is refused, not accepted unseen.
func notesInboxAccept(e *env, args []string) int {
	const usage = "usage: railswitch notes inbox accept <worktree> <name> --hash <hash> [--as <name>]"
	fset := e.flags("notes inbox accept")
	hash := fset.String("hash", "", "the hash of the suggestion as reviewed (notes inbox show --json; required)")
	as := fset.String("as", "", "the note's name (default: the suggestion's)")
	a, code, ok := parseArgs(e, fset, args, 2, usage)
	if !ok {
		return code
	}
	if *hash == "" {
		return e.usageErr("%s", usage)
	}
	dir, code, ok := e.inboxArgs("accept", a[0], a[1])
	if !ok {
		return code
	}
	target := a[1]
	if *as != "" {
		target = *as
	}
	if !noteName.MatchString(target) {
		return e.usageErr("notes inbox accept: %q is not a note name (letters, digits, . _ -, ending .md)", target)
	}
	notes, err := e.notesDir()
	if err != nil {
		return e.fail(err)
	}
	unlock, err := lockFile(filepath.Join(filepath.Dir(notes), ".railswitch", "notes.lock"))
	if err != nil {
		return e.fail(err)
	}
	defer unlock()
	data, exists, err := readNote(dir, a[1])
	switch {
	case err != nil:
		return e.fail(err)
	case !exists:
		return e.fail(fmt.Errorf("no suggestion %s from %s", a[1], a[0]))
	case workspace.Hash(data) != *hash:
		return e.fail(fmt.Errorf("suggestion %s changed since it was read: show it again", a[1]))
	case !utf8.Valid(data):
		return e.fail(fmt.Errorf("suggestion %s is not UTF-8", a[1]))
	}
	if _, _, err := readNote(notes, target); err != nil { // a symlink or other non-regular file in its place
		return e.fail(err)
	}
	if err := os.MkdirAll(notes, 0o700); err != nil {
		return e.fail(err)
	}
	if err := tomlfile.WriteAtomic(filepath.Join(notes, target), data, 0o644); err != nil {
		return e.fail(err)
	}
	if err := os.Remove(filepath.Join(dir, a[1])); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return e.fail(fmt.Errorf("note %s saved, but the suggestion stays: %w", target, err))
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(struct {
			Name string `json:"name"`
			Hash string `json:"hash"`
		}{target, *hash}); err != nil {
			return e.fail(err)
		}
	}
	return 0
}

// railswitch notes inbox reject <worktree> <name>: remove the suggestion.
func notesInboxReject(e *env, args []string) int {
	fset := e.flags("notes inbox reject")
	a, code, ok := parseArgs(e, fset, args, 2, "usage: railswitch notes inbox reject <worktree> <name>")
	if !ok {
		return code
	}
	dir, code, ok := e.inboxArgs("reject", a[0], a[1])
	if !ok {
		return code
	}
	// Remove takes the name itself: a symlink goes, never what it points at.
	if err := os.Remove(filepath.Join(dir, a[1])); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return e.fail(fmt.Errorf("no suggestion %s from %s", a[1], a[0]))
		}
		return e.fail(err)
	}
	return 0
}
