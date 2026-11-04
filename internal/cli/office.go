package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/jbigler/railswitch/internal/dashconf"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/office"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/tomlfile"
	"github.com/jbigler/railswitch/internal/web"
)

const officeUsage = "usage: railswitch [--json] office set check <dir> | add <dir> | ls | use <name> | rm <name>\n       railswitch [--json] office tv [jellyfin set [--url <url>] [--playlist <name>] [--user <name>] | jellyfin key | jellyfin rm]"

// officeLs is office set ls --json.
type officeLs struct {
	Active string         `json:"active"`
	Sets   []office.Entry `json:"sets"`
}

// railswitch office set check|add|ls|use|rm: the Virtual Office's sets
// (D153): built in, or installed under <data>/office/; the active one is
// dashboard.toml's [office] set.
func cmdOffice(ctx context.Context, e *env, args []string) int {
	if len(args) >= 1 && args[0] == "tv" {
		return cmdOfficeTV(ctx, e, args[1:])
	}
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

// officeTV is office tv --json: the lounge screen's sources (D168), each
// null when not set up.
type officeTV struct {
	Jellyfin *officeJellyfin `json:"jellyfin"`
}

type officeJellyfin struct {
	dashconf.Jellyfin
	KeySet bool `json:"key_set"`
}

// railswitch office tv [jellyfin set|key|rm]: the sources the lounge's big
// screen offers (D168). Jellyfin's API key travels on stdin (D33) into a
// 0600 file the dashboard hands the office page.
func cmdOfficeTV(ctx context.Context, e *env, args []string) int {
	verb := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		if args[0] != "jellyfin" || len(args) < 2 {
			return e.usageErr("%s", officeUsage)
		}
		verb, args = args[1], args[2:]
	}
	var fset *flag.FlagSet
	var u, pl, user *string
	switch verb {
	case "":
		fset = e.flags("office tv")
	case "key":
		fset = e.flags("office tv jellyfin key")
	case "rm":
		fset = e.flags("office tv jellyfin rm")
	case "set":
		fset = e.flags("office tv jellyfin set")
		u = fset.String("url", "", "the Jellyfin server: http(s)://host[:port][/path]")
		pl = fset.String("playlist", "", "the playlist's name in Jellyfin")
		user = fset.String("user", "", "the Jellyfin user who owns it (\"\": none)")
	default:
		return e.usageErr("office tv jellyfin: unknown subcommand %q\n%s", verb, officeUsage)
	}
	if code, ok := parse(fset, args); !ok {
		return code
	}
	if fset.NArg() != 0 {
		return e.usageErr("%s", officeUsage)
	}
	dirs, err := machine.DefaultDirs()
	if err != nil {
		return e.fail(err)
	}
	f, err := dashconf.Load(dirs)
	if err != nil {
		return e.fail(err)
	}
	keyPath := dashconf.JellyfinKeyPath(dirs)
	switch verb {
	case "":
		var r officeTV
		if f.Jellyfin.URL != "" {
			_, err := os.Stat(keyPath)
			r.Jellyfin = &officeJellyfin{Jellyfin: f.Jellyfin, KeySet: err == nil}
		}
		if e.json {
			if err := json.NewEncoder(e.stdout).Encode(r); err != nil {
				return e.fail(err)
			}
			return 0
		}
		j := r.Jellyfin
		if j == nil {
			fmt.Fprintln(e.stdout, "no sources: railswitch office tv jellyfin set --url <server> --playlist <name>")
			return 0
		}
		key := "key set"
		if !j.KeySet {
			key = "no key (railswitch office tv jellyfin key)"
		}
		who := ""
		if j.User != "" {
			who = " of " + safefile.Printable(j.User)
		}
		fmt.Fprintf(e.stdout, "jellyfin: %s, playlist %q%s; %s\n", j.URL, safefile.Printable(j.Playlist), who, key)
	case "set":
		j := f.Jellyfin
		fset.Visit(func(fl *flag.Flag) {
			switch fl.Name {
			case "url":
				j.URL = strings.TrimSuffix(*u, "/")
			case "playlist":
				j.Playlist = *pl
			case "user":
				j.User = *user
			}
		})
		if err := j.Check(); err != nil {
			return e.fail(err)
		}
		f.Jellyfin = j
		if err := f.Save(dirs); err != nil {
			return e.fail(err)
		}
		fmt.Fprintf(e.stdout, "the lounge's screen offers %q from %s; open office pages show it after a reload\n", safefile.Printable(j.Playlist), j.URL)
		if _, err := os.Stat(keyPath); err != nil {
			fmt.Fprintln(e.stdout, "next: railswitch office tv jellyfin key")
		}
	case "key":
		v, err := e.readSecretValue(ctx, "the Jellyfin API key", false)
		if err != nil {
			return e.fail(err)
		}
		defer clear(v)
		if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
			return e.fail(err)
		}
		if err := tomlfile.WriteAtomic(keyPath, v, 0o600); err != nil {
			return e.fail(err)
		}
		fmt.Fprintln(e.stdout, "the Jellyfin API key is set")
	case "rm":
		f.Jellyfin = dashconf.Jellyfin{}
		if err := f.Save(dirs); err != nil {
			return e.fail(err)
		}
		if err := os.Remove(keyPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return e.fail(err)
		}
		fmt.Fprintln(e.stdout, "removed the Jellyfin source and its key")
	}
	return 0
}
