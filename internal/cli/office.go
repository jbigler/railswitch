package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"text/tabwriter"

	"github.com/jbigler/railswitch/internal/dashconf"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/office"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/web"
)

const officeUsage = "usage: railswitch [--json] office set check <dir> | add <dir> | ls | use <name> | rm <name>"

// officeLs is office set ls --json.
type officeLs struct {
	Active string         `json:"active"`
	Sets   []office.Entry `json:"sets"`
}

// railswitch office set check|add|ls|use|rm: the Virtual Office's sets
// (D153): built in, or installed under <data>/office/; the active one is
// dashboard.toml's [office] set.
func cmdOffice(_ context.Context, e *env, args []string) int {
	if len(args) < 2 || args[0] != "set" {
		return e.usageErr("%s", officeUsage)
	}
	verb := args[1]
	var fset *flag.FlagSet
	want := 1 // positional arguments
	switch verb {
	case "check":
		fset = e.flags("office set check")
	case "add":
		fset = e.flags("office set add")
	case "ls":
		fset, want = e.flags("office set ls"), 0
	case "use":
		fset = e.flags("office set use")
	case "rm":
		fset = e.flags("office set rm")
	default:
		return e.usageErr("office set: unknown subcommand %q\n%s", verb, officeUsage)
	}
	if code, ok := parse(fset, args[2:]); !ok {
		return code
	}
	if fset.NArg() != want {
		return e.usageErr("%s", officeUsage)
	}
	dirs, err := machine.DefaultDirs()
	if err != nil {
		return e.fail(err)
	}
	static := web.StaticFS()
	list, err := office.LoadPieces(static)
	if err != nil {
		return e.fail(err)
	}
	st := &office.Store{Data: dirs.Data, Static: static, List: list}
	switch verb {
	case "check":
		r, err := office.Check(fset.Arg(0), list)
		if err != nil {
			return e.fail(err)
		}
		if e.json {
			if err := json.NewEncoder(e.stdout).Encode(r); err != nil {
				return e.fail(err)
			}
		} else {
			officeReport(e, r)
		}
		if !r.OK() {
			return 1
		}
	case "add":
		r, err := st.Add(fset.Arg(0))
		if r != nil && !r.OK() {
			officeReport(e, r)
			return e.fail(fmt.Errorf("office set add: the set is refused; nothing added"))
		}
		if err != nil {
			return e.fail(err)
		}
		fmt.Fprintf(e.stdout, "added %s (%d of %d pieces; the rest are boxes): railswitch office set use %s\n", r.Name, len(r.Have()), len(list.Pieces), r.Name)
	case "ls":
		f, err := dashconf.Load(dirs)
		if err != nil {
			return e.fail(err)
		}
		active := activeSet(f)
		sets, err := st.Ls()
		if err != nil {
			return e.fail(err)
		}
		if e.json {
			if err := json.NewEncoder(e.stdout).Encode(officeLs{Active: active, Sets: sets}); err != nil {
				return e.fail(err)
			}
			return 0
		}
		tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "SET\tSOURCE\tVERSION\tPIECES\tSTATUS\tDESCRIPTION")
		for _, s := range sets {
			mark := " "
			if s.Name == active {
				mark = "*"
			}
			status := s.Status
			if s.Reason != "" {
				status += " (" + safefile.Printable(s.Reason) + ")"
			}
			fmt.Fprintf(tw, "%s%s\t%s\t%d\t%d/%d\t%s\t%s\n", mark, s.Name, s.Source, s.Version, s.Present, s.Present+s.Missing, status, safefile.Printable(s.Description))
		}
		tw.Flush()
	case "use":
		name := fset.Arg(0)
		s, ok := st.Get(name)
		if !ok {
			return e.fail(fmt.Errorf("office set use: no set %q (railswitch office set ls)", name))
		}
		if s.Status != "ok" {
			return e.fail(fmt.Errorf("office set use: %s is %s: %s", name, s.Status, safefile.Printable(s.Reason)))
		}
		f, err := dashconf.Load(dirs)
		if err != nil {
			return e.fail(err)
		}
		f.OfficeSet = name
		if name == office.DefaultSet {
			f.OfficeSet = ""
		}
		if err := f.Save(dirs); err != nil {
			return e.fail(err)
		}
		fmt.Fprintf(e.stdout, "the Virtual Office uses %s; open pages show it after a reload\n", name)
	case "rm":
		name := fset.Arg(0)
		f, err := dashconf.Load(dirs)
		if err != nil {
			return e.fail(err)
		}
		if err := st.Remove(name); err != nil {
			return e.fail(err)
		}
		fmt.Fprintf(e.stdout, "removed %s\n", name)
		if activeSet(f) == name {
			f.OfficeSet = ""
			if err := f.Save(dirs); err != nil {
				return e.fail(err)
			}
			fmt.Fprintf(e.stdout, "it was the active set: the Virtual Office uses %s again\n", office.DefaultSet)
		}
	}
	return 0
}

func activeSet(f *dashconf.File) string {
	if f.OfficeSet == "" {
		return office.DefaultSet
	}
	return f.OfficeSet
}

// officeReport prints a check: its folder errors, each refused piece, and
// a count; names and reasons come from the folder, so they are made
// printable.
func officeReport(e *env, r *office.Report) {
	for _, m := range r.Errors {
		fmt.Fprintf(e.stdout, "error: %s\n", safefile.Printable(m))
	}
	n := map[string]int{}
	for _, x := range r.Results {
		n[x.Status]++
		if x.Status == "refused" {
			fmt.Fprintf(e.stdout, "refused: %s\n", safefile.Printable(x.Reason))
		}
	}
	name := r.Name
	if name == "" {
		name = "the set"
	}
	verdict := "ok"
	if !r.OK() {
		verdict = "refused"
	}
	fmt.Fprintf(e.stdout, "%s: %s; %d pieces ok, %d missing (drawn as boxes), %d refused\n", name, verdict, n["ok"], n["missing"], n["refused"])
}
