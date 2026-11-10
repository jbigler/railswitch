package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/tomlfile"
	"github.com/jbigler/railswitch/internal/workspace"
)

const notesUsage = "usage: railswitch [--json] notes ls | [--json] notes show <name> | notes write <name> --if-hash <hash> | notes rm <name> --if-hash <hash> | notes inbox ..."

// noteName is a note's file name: what railswitch shows, writes or removes.
var noteName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}\.md$`)

// noteRow is one element of `notes ls --json` notes (docs/json.md).
type noteRow struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"` // RFC 3339, UTC
	mod      time.Time
}

// noteDoc is `notes show --json`.
type noteDoc struct {
	Name string `json:"name"`
	Text string `json:"text"`
	Hash string `json:"hash"`
}

// railswitch notes ls|show|write|rm: the project's shared notes (D141,
// D144), <ws>/notes/. Agents write that folder, so it is hostile: only
// regular files named as noteName are read, never through a symlink, and a
// write or removal must name the content it replaces (--if-hash).
func cmdNotes(_ context.Context, e *env, args []string) int {
	if len(args) == 0 {
		return e.usageErr("%s", notesUsage)
	}
	switch args[0] {
	case "ls":
		return notesLs(e, args[1:])
	case "show":
		return notesShow(e, args[1:])
	case "write":
		return notesWrite(e, args[1:])
	case "rm":
		return notesRm(e, args[1:])
	case "inbox":
		return notesInbox(e, args[1:])
	}
	return e.usageErr("notes: unknown subcommand %q\n%s", args[0], notesUsage)
}

// notesDir is the workspace's notes folder.
func (e *env) notesDir() (string, error) {
	root, err := workspace.Root(e.dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, render.NotesDir), nil
}

// notesList is the regular files named as notes in dir, newest first; a
// missing dir has none.
func notesList(dir string) ([]noteRow, error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return []noteRow{}, nil
	}
	if err != nil {
		return nil, err
	}
	rows := []noteRow{}
	for _, d := range ents {
		if !d.Type().IsRegular() || !noteName.MatchString(d.Name()) {
			continue
		}
		fi, err := d.Info() // lstat: a file swapped for a symlink since is gone or skipped
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		rows = append(rows, noteRow{Name: d.Name(), Size: fi.Size(), Modified: fi.ModTime().UTC().Format(time.RFC3339), mod: fi.ModTime()})
	}
	slices.SortStableFunc(rows, func(a, b noteRow) int { return b.mod.Compare(a.mod) })
	return rows, nil
}

func notesLs(e *env, args []string) int {
	fset := e.flags("notes ls")
	if code, ok := parse(fset, args); !ok {
		return code
	}
	if fset.NArg() != 0 {
		return e.usageErr("%s", notesUsage)
	}
	dir, err := e.notesDir()
	if err != nil {
		return e.fail(err)
	}
	rows, err := notesList(dir)
	if err != nil {
		return e.fail(err)
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(struct {
			Notes []noteRow `json:"notes"`
		}{rows}); err != nil {
			return e.fail(err)
		}
		return 0
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSIZE\tMODIFIED")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%d\t%s\n", r.Name, r.Size, r.Modified)
	}
	if err := tw.Flush(); err != nil {
		return e.fail(err)
	}
	return 0
}

// readNote reads note name of dir as a hostile file; exists is false for a
// missing one, and a symlink or other non-regular file is an error.
func readNote(dir, name string) (data []byte, exists bool, err error) {
	data, err = safefile.Read(filepath.Join(dir, name), safefile.Limit)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	return data, err == nil, err
}

func notesShow(e *env, args []string) int {
	fset := e.flags("notes show")
	a, code, ok := parseArgs(e, fset, args, 1, "usage: railswitch [--json] notes show <name>")
	if !ok {
		return code
	}
	name := a[0]
	if !noteName.MatchString(name) {
		return e.usageErr("notes show: %q is not a note name (letters, digits, . _ -, ending .md)", name)
	}
	dir, err := e.notesDir()
	if err != nil {
		return e.fail(err)
	}
	data, exists, err := readNote(dir, name)
	if err != nil {
		return e.fail(err)
	}
	if !exists {
		return e.fail(fmt.Errorf("no note %s in %s", name, dir))
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(noteDoc{Name: name, Text: string(data), Hash: workspace.Hash(data)}); err != nil {
			return e.fail(err)
		}
		return 0
	}
	fmt.Fprint(e.stdout, printableText(data))
	return 0
}

// printableText is a note's text for a terminal: an agent wrote it, so no
// escapes get through; line breaks and tabs stay.
func printableText(data []byte) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return ' '
		}
		return r
	}, string(data))
}

// railswitch notes write <name> --if-hash <h>: stdin replaces note name
// (--if-hash "": creates it, refused when it exists).
func notesWrite(e *env, args []string) int {
	const usage = "usage: railswitch notes write <name> --if-hash <hash>  (the text on stdin; --if-hash \"\" adds a new note)"
	fset := e.flags("notes write")
	hash := fset.String("if-hash", "", "the hash of the note the edit started from (notes show --json); \"\" for a new note (required)")
	a, code, ok := parseArgs(e, fset, args, 1, usage)
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
	if !utf8.Valid(data) {
		return e.fail(errors.New("notes write: the text is not UTF-8"))
	}
	return notesChange(e, a[0], *hash, data)
}

// railswitch notes rm <name> --if-hash <h>: remove note name.
func notesRm(e *env, args []string) int {
	const usage = "usage: railswitch notes rm <name> --if-hash <hash>"
	fset := e.flags("notes rm")
	hash := fset.String("if-hash", "", "the hash of the note (notes show --json; required)")
	a, code, ok := parseArgs(e, fset, args, 1, usage)
	if !ok {
		return code
	}
	if *hash == "" {
		return e.usageErr("%s", usage)
	}
	return notesChange(e, a[0], *hash, nil)
}

// lockFile takes an exclusive flock on path, creating it.
func lockFile(path string) (unlock func(), err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return func() { f.Close() }, nil
}

// notesChange sets note name to data (nil: removes it) when it still holds
// what the caller read (hash; "": it must not exist). A symlink or other
// non-regular file in its place is refused. A write goes to a temp file
// renamed over the name, which replaces a symlink planted after the check
// rather than following it.
func notesChange(e *env, name, hash string, data []byte) int {
	if !noteName.MatchString(name) {
		return e.usageErr("notes: %q is not a note name (letters, digits, . _ -, ending .md)", name)
	}
	dir, err := e.notesDir()
	if err != nil {
		return e.fail(err)
	}
	// Two saves at once (two dashboard tabs) must not both pass the hash
	// check: the check and the write hold one lock, outside the folder
	// agents write.
	unlock, err := lockFile(filepath.Join(filepath.Dir(dir), ".railswitch", "notes.lock"))
	if err != nil {
		return e.fail(err)
	}
	defer unlock()
	cur, exists, err := readNote(dir, name)
	switch {
	case err != nil:
		return e.fail(err)
	case hash == "" && exists:
		return e.fail(fmt.Errorf("note %s exists: pass its hash (notes show --json) to replace it", name))
	case hash != "" && !exists:
		return e.fail(fmt.Errorf("no note %s in %s", name, dir))
	case hash != "" && workspace.Hash(cur) != hash:
		return e.fail(fmt.Errorf("note %s changed since it was read: show it again", name))
	}
	path := filepath.Join(dir, name)
	if data == nil {
		err = os.Remove(path)
	} else if err = os.MkdirAll(dir, 0o700); err == nil {
		err = tomlfile.WriteAtomic(path, data, 0o644)
	}
	if err != nil {
		return e.fail(err)
	}
	if e.json { // the note's new hash ("": removed), for the next edit's --if-hash
		h := ""
		if data != nil {
			h = workspace.Hash(data)
		}
		if err := json.NewEncoder(e.stdout).Encode(struct {
			Hash string `json:"hash"`
		}{h}); err != nil {
			return e.fail(err)
		}
	}
	return 0
}
