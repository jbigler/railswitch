package cli

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jbigler/railswitch/internal/libraries"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/signing"
)

// railswitch library add <url> --alias <a> [--branch <b>]: records a
// library repository in libraries.toml and fetches it (D103). A fetch
// that fails before a verdict removes the entry again.
func libraryAdd(ctx context.Context, e *env, args []string) int {
	fset := e.flags("library add")
	alias := fset.String("alias", "", "the `alias` that prefixes the library's refs (<alias>:<name>@N)")
	branch := fset.String("branch", "", "the `branch` to fetch (default: the remote's default)")
	url, code, ok := parseNamed(e, fset, args, libraryUsage)
	if !ok {
		return code
	}
	if *alias == "" {
		return e.usageErr("library add: --alias is required")
	}
	d, err := machine.DefaultDirs()
	if err != nil {
		return e.fail(err)
	}
	lib := libraries.Library{Alias: *alias, URL: url, Branch: *branch}
	if err := libraries.Update(d, func(c *libraries.Config) (bool, error) { return true, c.Add(lib) }); err != nil {
		return e.fail(err)
	}
	res, err := libraries.Fetch(ctx, e.run, d, &lib, updateTrust)
	if err != nil {
		if rmErr := removeLibrary(d, lib.Alias); rmErr != nil {
			err = errors.Join(err, rmErr)
		}
		return e.fail(fmt.Errorf("library add %s: %w; not added", lib.Alias, err))
	}
	return printFetch(e, lib.Alias, res)
}

// railswitch library fetch [<alias>]: fetches one library, or all.
func libraryFetch(ctx context.Context, e *env, args []string) int {
	fset := e.flags("library fetch")
	if code, ok := parse(fset, args); !ok {
		return code
	}
	if fset.NArg() > 1 {
		return e.usageErr("%s", libraryUsage)
	}
	d, err := machine.DefaultDirs()
	if err != nil {
		return e.fail(err)
	}
	c, err := libraries.Load(libraries.Path(d))
	if err != nil {
		return e.fail(err)
	}
	libs := c.Libraries
	if fset.NArg() == 1 {
		lib := c.Library(fset.Arg(0))
		if lib == nil {
			return e.fail(fmt.Errorf("no library %s (library ls lists them)", safefile.Printable(fset.Arg(0))))
		}
		libs = []*libraries.Library{lib}
	}
	code := 0
	for _, lib := range libs {
		res, err := libraries.Fetch(ctx, e.run, d, lib, updateTrust)
		if err != nil {
			code = e.fail(fmt.Errorf("library fetch %s: %w", lib.Alias, err))
			continue
		}
		code = max(code, printFetch(e, lib.Alias, res))
	}
	return code
}

// printFetch prints a fetch's verdict and what to do next. A pending
// fetch exits 0: it waits for the user, not on an error.
func printFetch(e *env, alias string, res *libraries.FetchResult) int {
	p := safefile.Printable
	v := res.Verify
	switch res.Status {
	case signing.StatusTrusted:
		fmt.Fprintf(e.stdout, "%s: trusted, %s serial %d, %d signed packages, commit %s\n", alias, p(v.Library), res.Serial, res.Packages, res.Commit)
		return 0
	case signing.StatusUnknownSigner:
		fmt.Fprintf(e.stdout, "%s: %s serial %d is signed by %s, a key not trusted for it; nothing from it is installed until you trust it.\n", alias, p(v.Library), res.Serial, v.Fingerprint)
		if v.KnownLibrary {
			fmt.Fprintf(e.stdout, "WARNING: the library already has trusted or revoked keys and no trusted key vouched for this one.\n"+
				"It may be an attacker's. Trust it only if the publisher confirms a new signer through another channel:\n"+
				"  railswitch library trust %s --fingerprint %s --new-signer\n", alias, v.Fingerprint)
		} else {
			fmt.Fprintf(e.stdout, "Check the fingerprint with the publisher, then trust it:\n  railswitch library trust %s --fingerprint %s\n", alias, v.Fingerprint)
		}
		return 0
	case signing.StatusRotation:
		fmt.Fprintf(e.stdout, "%s: the trusted key %s vouches for the new key %s. Check it with the publisher, then confirm the rotation:\n  railswitch library trust %s --fingerprint %s --rotation\n",
			alias, v.Fingerprint, v.NewFingerprint, alias, v.NewFingerprint)
		return 0
	}
	reason := ""
	if res.Reason != "" {
		reason = ": " + p(res.Reason)
	}
	fmt.Fprintf(e.stdout, "%s: %s%s; the current copy is kept\n", alias, res.Status, reason)
	return 1
}

// railswitch library rm <alias>: refused while a registered project names
// <alias>: in its project.toml.
func libraryRm(e *env, args []string) int {
	fset := e.flags("library rm")
	alias, code, ok := parseNamed(e, fset, args, libraryUsage)
	if !ok {
		return code
	}
	d, err := machine.DefaultDirs()
	if err != nil {
		return e.fail(err)
	}
	c, err := libraries.Load(libraries.Path(d))
	if err != nil {
		return e.fail(err)
	}
	if c.Library(alias) == nil {
		return e.fail(fmt.Errorf("no library %s (library ls lists them)", safefile.Printable(alias)))
	}
	users, err := libraryUsers(d, alias)
	if err != nil {
		return e.fail(err)
	}
	if len(users) > 0 {
		return e.fail(fmt.Errorf("library rm: %s is used by %s; remove its refs (%s:…) from their project.toml first", alias, strings.Join(users, ", "), alias))
	}
	if err := removeLibrary(d, alias); err != nil {
		return e.fail(err)
	}
	fmt.Fprintf(e.stdout, "removed library %s\n", alias)
	return 0
}

// libraryUsers are the registered projects whose project.toml names a ref
// of alias: the text "<alias>: or '<alias>:.
func libraryUsers(d machine.Dirs, alias string) ([]string, error) {
	reg, err := machine.Read(d.Data)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil // no project registered yet
	}
	if err != nil {
		return nil, err
	}
	var users []string
	for _, p := range reg.Projects {
		data, err := safefile.Read(filepath.Join(p.Workspace, "project.toml"), safefile.Limit)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("project %s: %w", p.Prefix, err)
		}
		if bytes.Contains(data, []byte(`"`+alias+":")) || bytes.Contains(data, []byte(`'`+alias+":")) {
			users = append(users, p.Prefix)
		}
	}
	return users, nil
}

// removeLibrary removes alias's entry and its directory, under its lock.
func removeLibrary(d machine.Dirs, alias string) error {
	unlock, err := libraries.Lock(d, alias)
	if err != nil {
		return err
	}
	defer unlock()
	if err := libraries.Update(d, func(c *libraries.Config) (bool, error) { return true, c.Remove(alias) }); err != nil {
		return err
	}
	return libraries.Remove(d, alias)
}

// libraryLsDoc is `library ls --json` (docs/json.md).
type libraryLsDoc struct {
	Libraries []libraryRow `json:"libraries"`
}

type libraryRow struct {
	Alias    string          `json:"alias"`
	URL      string          `json:"url"`
	Branch   string          `json:"branch"`
	ID       string          `json:"id"`
	Status   string          `json:"status"` // the last fetch's; "" never fetched
	Serial   int64           `json:"serial"` // current/'s, as Commit
	Commit   string          `json:"commit"`
	Fetched  string          `json:"fetched"` // RFC 3339; "" never fetched
	Packages int             `json:"packages"`
	Pending  *libraryPending `json:"pending"`
}

type libraryPending struct {
	Fingerprint    string `json:"fingerprint"`
	NewFingerprint string `json:"new_fingerprint"`
	Status         string `json:"status"`
	KnownLibrary   bool   `json:"known_library"`
}

// railswitch library ls: each library with its last fetch.
func libraryLs(e *env, args []string) int {
	fset := e.flags("library ls")
	if code, ok := parse(fset, args); !ok {
		return code
	}
	if fset.NArg() != 0 {
		return e.usageErr("%s", libraryUsage)
	}
	d, err := machine.DefaultDirs()
	if err != nil {
		return e.fail(err)
	}
	c, err := libraries.Load(libraries.Path(d))
	if err != nil {
		return e.fail(err)
	}
	doc := libraryLsDoc{Libraries: []libraryRow{}}
	for _, lib := range c.Libraries {
		row, err := libraryState(d, lib)
		if err != nil {
			return e.fail(err)
		}
		doc.Libraries = append(doc.Libraries, row)
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(doc); err != nil {
			return e.fail(err)
		}
		return 0
	}
	p := safefile.Printable
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ALIAS\tSTATUS\tSERIAL\tPACKAGES\tFETCHED\tURL")
	for _, r := range doc.Libraries {
		status, serial := r.Status, "-"
		if status == "" {
			status = "not fetched"
		}
		if r.Commit != "" {
			serial = strconv.FormatInt(r.Serial, 10)
		}
		url := p(r.URL)
		if r.Branch != "" {
			url += " (" + p(r.Branch) + ")"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", r.Alias, status, serial, r.Packages, cmp.Or(r.Fetched, "-"), url)
	}
	tw.Flush()
	for _, r := range doc.Libraries {
		if r.Pending != nil {
			fmt.Fprintf(e.stdout, "%s: a fetch signed by %s waits for library trust %s --fingerprint …\n", r.Alias,
				cmp.Or(r.Pending.NewFingerprint, r.Pending.Fingerprint), r.Alias)
		}
	}
	return 0
}

// libraryState is lib's row: its fetch record, current/'s package count
// and its pending fetch.
func libraryState(d machine.Dirs, lib *libraries.Library) (libraryRow, error) {
	row := libraryRow{Alias: lib.Alias, URL: lib.URL, Branch: lib.Branch, ID: lib.ID}
	rec, err := libraries.ReadRecord(d, lib.Alias)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return row, err
	default:
		row.Status, row.Serial, row.Commit = string(rec.Status), rec.Serial, rec.Commit
		if !rec.Fetched.IsZero() {
			row.Fetched = rec.Fetched.UTC().Format(time.RFC3339)
		}
	}
	idx, _, err := readIndex(libraries.CurrentDir(d, lib.Alias))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return row, fmt.Errorf("library %s: %w", lib.Alias, err)
	default:
		row.Packages = len(idx.Packages)
	}
	pend, err := libraries.ReadPending(d, lib.Alias)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return row, err
	default:
		row.Pending = &libraryPending{Fingerprint: pend.Fingerprint, NewFingerprint: pend.NewFingerprint, Status: string(pend.Status), KnownLibrary: pend.KnownLibrary}
	}
	return row, nil
}
