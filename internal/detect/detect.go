// Package detect reads a repository for the pack and options that fit it
// (D88). The rules are the packages' own (D87); this package only fetches
// the files they name and runs them.
package detect

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/sys"
)

// fetchTimeout bounds the whole of Fetch, every git call together.
var fetchTimeout = 2 * time.Minute

// staleAfter is the age of a leftover temp dir that the next Fetch
// removes: far past fetchTimeout, so never a running Fetch's.
const staleAfter = 10 * time.Minute

const tempPrefix = "railswitch-detect-"

// Paths is every repository path a rule in cat names, sorted and unique.
func Paths(cat *pack.Catalog) []string {
	var out []string
	for _, p := range cat.Packs {
		out = append(out, p.Detect.Files...)
	}
	for _, o := range cat.Options {
		for _, d := range o.Detect {
			out = append(out, d.File)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Fetch is the contents of each of paths in branch of repo, read from the
// object store of a shallow, blobless clone with no working tree, in a
// temp dir it always removes. Only a regular file (mode 100644 or 100755)
// of at most safefile.Limit bytes counts: a path that is missing, a
// symlink, under one, a directory or a submodule is left out, and no blob
// is inflated onto disk. No hook, LFS filter or prompt runs.
func Fetch(ctx context.Context, r sys.Runner, repo, branch string, paths []string) (map[string][]byte, error) {
	for _, a := range [][2]string{{"repository", repo}, {"branch", branch}} {
		if err := sys.GitArg(a[0], a[1]); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	tmp, err := sys.RuntimeTemp(tempPrefix, staleAfter)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	env, err := sys.GitNetEnv(ctx, r, os.Environ())
	if err != nil {
		return nil, err
	}
	env = append(env, "GIT_LFS_SKIP_SMUDGE=1")
	dir := filepath.Join(tmp, "repo")
	git := func(stdout io.Writer, args ...string) (sys.Result, error) {
		argv := append([]string{"--literal-pathspecs", "-c", "core.hooksPath=/dev/null"}, sys.GitProtocols...)
		argv = append(argv, args...)
		return r.Run(ctx, sys.Cmd{Name: "git", Args: argv, Env: env, Timeout: fetchTimeout, Stdout: stdout})
	}
	if _, err := git(nil, "clone", "--quiet", "--depth", "1", "--filter=blob:none", "--no-checkout", "--branch", branch, "--", repo, dir); err != nil {
		return nil, fmt.Errorf("git clone --branch %s %s: %w", branch, repo, err)
	}
	files := map[string][]byte{}
	if len(paths) == 0 {
		return files, nil
	}
	res, err := git(nil, append([]string{"-C", dir, "ls-tree", "-z", "HEAD", "--"}, paths...)...)
	if err != nil {
		return nil, fmt.Errorf("git ls-tree: %w", err)
	}
	for p, oid := range regularBlobs(res.Stdout, paths) {
		// The blob is fetched (compressed) from the promisor here.
		res, err := git(nil, "-C", dir, "cat-file", "-s", oid)
		if err != nil {
			return nil, fmt.Errorf("git cat-file -s %s: %w", p, err)
		}
		size, err := strconv.ParseInt(strings.TrimSpace(string(res.Stdout)), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("git cat-file -s %s: %q", p, res.Stdout)
		}
		if size > safefile.Limit {
			continue
		}
		var buf capped
		if _, err := git(&buf, "-C", dir, "cat-file", "blob", oid); err != nil {
			if errors.Is(err, errOverLimit) || errors.Is(buf.err, errOverLimit) {
				continue
			}
			return nil, fmt.Errorf("git cat-file blob %s: %w", p, err)
		}
		files[p] = buf.b
	}
	return files, nil
}

// regularBlobs is path → blob id for each entry of `git ls-tree -z` output
// that is one of paths and a regular file. A path under a symlinked or
// missing directory has no entry.
func regularBlobs(out []byte, paths []string) map[string]string {
	blobs := map[string]string{}
	for _, ent := range bytes.Split(out, []byte{0}) {
		meta, p, ok := bytes.Cut(ent, []byte{'\t'})
		if !ok || !slices.Contains(paths, string(p)) {
			continue
		}
		f := strings.Fields(string(meta)) // <mode> <type> <oid>
		if len(f) == 3 && (f[0] == "100644" || f[0] == "100755") && f[1] == "blob" {
			blobs[string(p)] = f[2]
		}
	}
	return blobs
}

var errOverLimit = errors.New("blob over safefile.Limit")

// capped keeps at most safefile.Limit bytes and fails the write past that.
type capped struct {
	b   []byte
	err error
}

func (c *capped) Write(p []byte) (int, error) {
	if len(c.b)+len(p) > safefile.Limit {
		c.err = errOverLimit
		return 0, errOverLimit
	}
	c.b = append(c.b, p...)
	return len(p), nil
}

// Result is what project detect prints (docs/json.md). Pack nil means no
// pack fits; then Options is empty.
type Result struct {
	Branch  string        `json:"branch"`
	Pack    *PackMatch    `json:"pack"`
	Options []OptionMatch `json:"options"`
}

// PackMatch is the chosen pack: Evidence its detect files the repository
// has, Others the other packs whose files it all has.
type PackMatch struct {
	Ref      string   `json:"ref"`
	Evidence []string `json:"evidence"`
	Others   []string `json:"others"`
}

// OptionMatch is one slot's choice. Source "repo": a rule of Ref matched,
// Evidence says which; "default": nothing matched and Ref is the pack's
// default (or "none"). Others are the other matching options.
type OptionMatch struct {
	Key      string   `json:"key"`
	Ref      string   `json:"ref"`
	Source   string   `json:"source"`
	Evidence string   `json:"evidence"`
	Others   []string `json:"others"`
}

// Choose picks the pack (packRef when not "") and each slot's option from
// files, the repository paths Fetch found.
func Choose(cat *pack.Catalog, files map[string][]byte, packRef string) (Result, error) {
	res := Result{Options: []OptionMatch{}}
	var matched []pack.Ref
	for _, r := range sortedRefs(cat.Packs) {
		need := cat.Packs[r].Detect.Files
		if len(need) > 0 && !slices.ContainsFunc(need, func(f string) bool { _, ok := files[f]; return !ok }) {
			matched = append(matched, r)
		}
	}
	var chosen pack.Ref
	switch {
	case packRef != "":
		r, err := pack.ParseRef(packRef)
		if err != nil {
			return Result{}, err
		}
		p, err := cat.Pack(r)
		if err != nil {
			return Result{}, err
		}
		if p == nil {
			return Result{}, fmt.Errorf("no pack %s (packs: %s)", r, strings.Join(refStrings(sortedRefs(cat.Packs)), ", "))
		}
		chosen = r
	case len(matched) > 0:
		chosen = matched[0]
	default:
		return res, nil
	}
	p := cat.Packs[chosen]
	res.Pack = &PackMatch{Ref: chosen.String(), Evidence: []string{}, Others: []string{}}
	for _, f := range p.Detect.Files {
		if _, ok := files[f]; ok {
			res.Pack.Evidence = append(res.Pack.Evidence, f)
		}
	}
	for _, r := range matched {
		if r != chosen {
			res.Pack.Others = append(res.Pack.Others, r.String())
		}
	}

	for _, slot := range config.Slots {
		def := cmp.Or(p.Defaults[slot], "none")
		m := OptionMatch{Key: slot, Ref: def, Source: "default", Others: []string{}}
		var hits []pack.Ref
		evidence := map[pack.Ref]string{}
		for _, r := range sortedRefs(cat.Options) {
			o := cat.Options[r]
			if o.Slot != slot || !slices.Contains(o.WorksWith, "*") && !slices.Contains(o.WorksWith, chosen.Name) {
				continue
			}
			if ev, ok := fires(o, files); ok {
				hits = append(hits, r)
				evidence[r] = ev
			}
		}
		if len(hits) > 0 {
			pick := hits[0]
			if i := slices.IndexFunc(hits, func(r pack.Ref) bool { return r.String() == def }); i >= 0 {
				pick = hits[i]
			}
			m.Ref, m.Source, m.Evidence = pick.String(), "repo", evidence[pick]
			for _, r := range hits {
				if r != pick {
					m.Others = append(m.Others, r.String())
				}
			}
		}
		res.Options = append(res.Options, m)
	}
	return res, nil
}

// fires is the evidence of o's first rule that matches a line of its file.
func fires(o *pack.Option, files map[string][]byte) (string, bool) {
	for _, d := range o.Detect {
		data, ok := files[d.File]
		if !ok {
			continue
		}
		re := d.Regexp()
		for line := range bytes.Lines(data) {
			if re.Match(bytes.TrimRight(line, "\r\n")) {
				return d.EvidenceText(), true
			}
		}
	}
	return "", false
}

func sortedRefs[V any](m map[pack.Ref]V) []pack.Ref {
	return slices.SortedFunc(maps.Keys(m), func(a, b pack.Ref) int { return strings.Compare(a.String(), b.String()) })
}

func refStrings(rs []pack.Ref) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.String()
	}
	return out
}
