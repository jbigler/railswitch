package cli

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing/fstest"

	"github.com/jbigler/railswitch/internal/libraries"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/review"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/template"
)

const templateImportUsage = "usage: railswitch template import <dir> [--accept <sha256>]"

// templateImportDoc is `template import --json` (docs/json.md): the
// review document lock --review prints, with one package, the template,
// plus its status and warnings.
type templateImportDoc struct {
	Ref      string          `json:"ref"`
	SHA256   string          `json:"sha256"`
	Status   string          `json:"status"` // new, update, same, older
	Replaces *importReplaces `json:"replaces"`
	Hash     string          `json:"hash"` // what --accept takes: the folder's sha256
	Flags    int             `json:"flags"`
	Packages []importPackage `json:"packages"`
	Restart  []string        `json:"restart"` // always empty: nothing runs from the store
	Warnings []string        `json:"warnings"`
	// BundlesComparedWith names, by directory, the built-in a bundled
	// package new here is shown against (its closest; D116).
	BundlesComparedWith map[string]string `json:"bundles_compared_with"`
}

type importReplaces struct {
	Ref    string `json:"ref"`
	SHA256 string `json:"sha256"`
}

type importPackage struct {
	Ref          string        `json:"ref"`
	Kind         string        `json:"kind"`
	Status       string        `json:"status"` // added, changed, unchanged
	From         string        `json:"from"`
	To           string        `json:"to"`
	AlsoLockedIn []string      `json:"also_locked_in"`
	Files        []review.File `json:"files"`
}

// railswitch template import <dir> [--accept <sha256>] (D113): review a
// template folder someone handed over; with --accept, the hash the review
// printed, save it in this machine's store.
func templateImport(e *env, args []string) int {
	fset := e.flags("template import")
	accept := fset.String("accept", "", "save the template whose review printed this `sha256`")
	dir, code, ok := parseNamed(e, fset, args, templateImportUsage)
	if !ok {
		return code
	}
	d, err := machine.DefaultDirs()
	if err != nil {
		return e.fail(err)
	}
	if dir, err = filepath.Abs(dir); err != nil {
		return e.fail(err)
	}
	in, err := template.ReadIncoming(dir, d)
	if err != nil {
		return e.fail(err)
	}
	if *accept != "" {
		if *accept != in.SHA256 {
			return e.fail(fmt.Errorf("%s changed since its review (sha256 %s, not %s): review it again", dir, in.SHA256, *accept))
		}
		if err := in.Install(d); err != nil {
			return e.fail(err)
		}
		if in.Status == template.ImportSame {
			fmt.Fprintf(e.stdout, "template %s is already on this machine\n", in.T.Ref())
		} else {
			fmt.Fprintf(e.stdout, "imported template %s into %s\n", in.T.Ref(), template.LocalDir(d, in.T.Name))
		}
		return 0
	}
	doc, err := importReview(in, d)
	if err != nil {
		return e.fail(err)
	}
	failed := in.Status == template.ImportOlder
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(doc); err != nil {
			return e.fail(err)
		}
	} else {
		printImport(e, dir, doc)
	}
	if failed {
		fmt.Fprintf(e.stderr, "railswitch: this machine has %s: an import must have a higher version\n", safefile.Printable(doc.Replaces.Ref))
		return 1
	}
	return 0
}

// importReview is in as a review document: every file against the
// store's version (all added for a new template), with D98's flags.
func importReview(in *template.Incoming, d machine.Dirs) (*templateImportDoc, error) {
	ref := in.T.Ref()
	doc := &templateImportDoc{Ref: ref, SHA256: in.SHA256, Status: in.Status, Hash: in.SHA256, Restart: []string{}, Warnings: importWarnings(in.T, d)}
	pkg := importPackage{Ref: ref, Kind: "template", To: in.SHA256, AlsoLockedIn: []string{}, Status: review.Added}
	var old fs.FS
	if in.Stored != nil {
		doc.Replaces = &importReplaces{Ref: in.Stored.Ref(), SHA256: in.StoredSHA}
		old, pkg.From, pkg.Status = in.Stored.Files, in.StoredSHA, review.Changed
	}
	if in.Status == template.ImportSame {
		pkg.Status = "unchanged"
	}
	old, doc.BundlesComparedWith = withBundleBases(old, in.T)
	files, err := review.Compare("template", old, in.T.Files)
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

// withBundleBases is old (the stored template, or nil) with, for each
// package t bundles that old lacks, its closest built-in under the
// bundle's directory: a new bundle is shown as its changes from that
// built-in, not as every file added. It returns which built-in each is.
func withBundleBases(old fs.FS, t *template.Template) (fs.FS, map[string]string) {
	names := map[string]string{}
	mem := fstest.MapFS{}
	if old != nil {
		_ = fs.WalkDir(old, ".", func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				data, _ := fs.ReadFile(old, p)
				mem[p] = &fstest.MapFile{Data: data}
			}
			return nil
		})
	}
	for _, b := range t.Bundled {
		if _, err := fs.Stat(mem, b.Dir); err == nil {
			continue
		}
		files := fstest.MapFS{}
		_ = fs.WalkDir(b.Files, ".", func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				data, _ := fs.ReadFile(b.Files, p)
				files[p] = &fstest.MapFile{Data: data}
			}
			return nil
		})
		ref, base := closestBuiltin(b.Kind, files)
		if base == nil {
			continue
		}
		names[b.Dir] = ref.String()
		_ = fs.WalkDir(base, ".", func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				data, _ := fs.ReadFile(base, p)
				mem[b.Dir+"/"+p] = &fstest.MapFile{Data: data}
			}
			return nil
		})
	}
	if old == nil && len(mem) == 0 {
		return nil, names
	}
	return mem, names
}

// importWarnings name the packages t uses that this machine can't give a
// new project as they are: a library's, when this machine hasn't added
// it (a local: one is bundled, D116).
func importWarnings(t *template.Template, d machine.Dirs) []string {
	out := []string{}
	c, err := libraries.Load(libraries.Path(d))
	if err != nil {
		out = append(out, fmt.Sprintf("the libraries it uses can't be checked: %v", err))
		c = nil
	}
	for _, ref := range t.Packages() {
		r, err := pack.ParseRef(ref)
		switch {
		case err != nil:
			continue
		case r.Library != "" && (c == nil || c.Library(r.Library) == nil):
			out = append(out, fmt.Sprintf("%s comes from the library %s, which this machine hasn't added (railswitch library add <url> --alias %s)", ref, r.Library, r.Library))
		}
	}
	return out
}

func printImport(e *env, dir string, doc *templateImportDoc) {
	p := safefile.Printable
	var head string
	switch doc.Status {
	case template.ImportNew:
		head = fmt.Sprintf("template %s (sha256 %s): new on this machine", p(doc.Ref), doc.SHA256)
	case template.ImportUpdate:
		head = fmt.Sprintf("template %s (sha256 %s): replaces %s", p(doc.Ref), doc.SHA256, p(doc.Replaces.Ref))
	case template.ImportSame:
		fmt.Fprintf(e.stdout, "template %s is already on this machine: nothing to import\n", p(doc.Ref))
		return
	default:
		head = fmt.Sprintf("template %s (sha256 %s)", p(doc.Ref), doc.SHA256)
	}
	for _, d := range slices.Sorted(maps.Keys(doc.BundlesComparedWith)) {
		head += fmt.Sprintf("\n%s/ is shown as its changes from the built-in %s", d, doc.BundlesComparedWith[d])
	}
	next := ""
	if doc.Status != template.ImportOlder {
		next = fmt.Sprintf("railswitch template import %s --accept %s", shellQuote(dir), doc.SHA256)
	}
	printFolderReview(e, head, doc.Packages[0].Files, doc.Warnings, doc.Flags, next)
}

// printFolderReview prints an import's review: head, every file with its
// flags and diff, the warnings, and the command that saves it (next; ""
// for none).
func printFolderReview(e *env, head string, files []review.File, warnings []string, flags int, next string) {
	p := safefile.Printable
	fmt.Fprintln(e.stdout, head)
	for _, f := range files {
		fmt.Fprintf(e.stdout, "\n%s %s\n", f.Status, p(f.Path))
		for _, fl := range f.Flags {
			where := ""
			if fl.Line > 0 {
				where = fmt.Sprintf(" line %d", fl.Line)
			}
			fmt.Fprintf(e.stdout, "  FLAG %s%s: %s\n", fl.Check, where, p(fl.Text))
		}
		for _, line := range strings.Split(strings.TrimSuffix(f.Diff, "\n"), "\n") {
			if line != "" {
				fmt.Fprintf(e.stdout, "  %s\n", p(line))
			}
		}
	}
	for _, w := range warnings {
		fmt.Fprintf(e.stdout, "\nwarning: %s\n", p(w))
	}
	if next != "" {
		fmt.Fprintf(e.stdout, "\n%d flagged; to save it: %s\n", flags, next)
	}
}
