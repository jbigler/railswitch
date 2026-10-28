package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/lockstore"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/review"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/template"
	"github.com/jbigler/railswitch/internal/tomlfile"
	"github.com/jbigler/railswitch/internal/workspace"
)

// templateCheckDoc is `template check --json` (docs/json.md).
type templateCheckDoc struct {
	Ref     string        `json:"ref"`
	SHA256  string        `json:"sha256"`  // as [template] records it
	Current string        `json:"current"` // what the ref resolves to now; "" when it doesn't
	Update  bool          `json:"update"`
	Error   string        `json:"error"` // why the ref doesn't resolve; "" when it does
	Files   []review.File `json:"files"` // recorded → current, flagged; [] without an update
}

// railswitch template check [--accept <sha256>]: whether the template the
// project (-C) started from changed, and the diff from what was accepted
// to it (D111). Nothing is applied: --accept only records the new content
// hash in [template] sha256, clearing the notice.
func templateCheck(e *env, args []string) int {
	fset := e.flags("template check")
	accept := fset.String("accept", "", "record this content `sha256` (the check's current) as accepted; nothing else changes")
	if code, ok := parse(fset, args); !ok {
		return code
	}
	if fset.NArg() != 0 || isSet(fset, "accept") && *accept == "" {
		return e.usageErr("usage: railswitch template check [--accept <sha256>]")
	}
	root, err := workspace.Root(e.dir)
	if err != nil {
		return e.fail(err)
	}
	proj := filepath.Join(root, "project.toml")
	data, err := projectBytes(root)
	if err != nil {
		return e.fail(err)
	}
	p, err := config.Parse(data)
	if err != nil {
		return e.fail(err)
	}
	if p.Template == nil {
		return e.fail(fmt.Errorf("%s records no template ([template]): the project was not started from one", proj))
	}
	d, err := machine.DefaultDirs()
	if err != nil {
		return e.fail(err)
	}
	st := template.Check(p.Template, d, workspace.Libraries(d))
	store := lockstore.Store{Dir: lockstore.Dir(d)}
	if *accept != "" {
		switch {
		case st.Err != nil:
			return e.fail(st.Err)
		case *accept != st.Current:
			return e.fail(errors.New("the template changed since it was checked: check it again"))
		case st.Current == st.SHA256:
			fmt.Fprintf(e.stdout, "template %s: nothing to accept\n", safefile.Printable(st.Ref))
			return 0
		}
		if sha, err := store.Put(st.Template.Files); err != nil || sha != st.Current {
			return e.fail(errors.Join(errors.New("store the template's new content"), err))
		}
		next, err := config.SetTemplateSHA(string(data), st.Current)
		if err != nil {
			return e.fail(err)
		}
		if err := tomlfile.WriteAtomic(proj, []byte(next), 0o644); err != nil {
			return e.fail(err)
		}
		fmt.Fprintf(e.stdout, "template %s: accepted %s (nothing else changed: edit Settings or the Containerfiles to take what you want)\n", safefile.Printable(st.Ref), short(st.Current))
		return 0
	}
	doc := templateCheckDoc{Ref: st.Ref, SHA256: st.SHA256, Current: st.Current, Update: st.Changed(), Files: []review.File{}}
	if st.Err != nil {
		doc.Error = st.Err.Error()
	}
	if doc.Update {
		var old fs.FS // a missing copy: every file shown added
		if files, err := store.Get(st.SHA256); err == nil {
			old = files
		}
		if doc.Files, err = review.Compare(pack.KindTemplate, old, st.Template.Files); err != nil {
			return e.fail(err)
		}
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(doc); err != nil {
			return e.fail(err)
		}
		return 0
	}
	switch {
	case doc.Error != "":
		fmt.Fprintf(e.stdout, "template %s does not resolve: %s\n", safefile.Printable(doc.Ref), safefile.Printable(doc.Error))
	case !doc.Update:
		fmt.Fprintf(e.stdout, "template %s: unchanged since it was accepted\n", safefile.Printable(doc.Ref))
	default:
		fmt.Fprintf(e.stdout, "template %s changed: %s -> %s (nothing is applied; record it with railswitch template check --accept %s)\n", safefile.Printable(doc.Ref), short(doc.SHA256), short(doc.Current), doc.Current)
		printReview(e.stdout, &reviewDoc{Packages: []reviewPkg{{Ref: doc.Ref, Kind: pack.KindTemplate, Status: workspace.StatusChanged, From: doc.SHA256, To: doc.Current, Files: doc.Files}}})
	}
	return 0
}
