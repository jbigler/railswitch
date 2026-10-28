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
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/sys"
)

// gitTimeout bounds each git call of Fetch.
const gitTimeout = 2 * time.Minute

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

// Fetch is the contents of each of paths in branch of repo, from a shallow,
// blobless, sparse clone into a temp dir it always removes. A path that is
// missing, a symlink (or under one), not a regular file or over
// safefile.Limit is left out. No hook, LFS filter or prompt runs.
func Fetch(ctx context.Context, r sys.Runner, repo, branch string, paths []string) (map[string][]byte, error) {
	for _, a := range [][2]string{{"repository", repo}, {"branch", branch}} {
		if a[1] == "" || strings.HasPrefix(a[1], "-") || strings.ContainsAny(a[1], "\r\n\x00") {
			return nil, fmt.Errorf("%s %q is not a usable git value", a[0], a[1])
		}
	}
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	tmp, err := os.MkdirTemp(base, "railswitch-detect-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	env, err := sys.GitNetEnv(ctx, r, os.Environ())
	if err != nil {
		return nil, err
	}
	env = append(env, "GIT_LFS_SKIP_SMUDGE=1")
	git := func(args ...string) error {
		argv := append([]string{"-c", "core.hooksPath=/dev/null", "-c", "protocol.ext.allow=never"}, args...)
		_, err := r.Run(ctx, sys.Cmd{Name: "git", Args: argv, Env: env, Timeout: gitTimeout})
		return err
	}
	dir := filepath.Join(tmp, "repo")
	if err := git("clone", "--quiet", "--depth", "1", "--filter=blob:none", "--no-checkout", "--branch", branch, "--", repo, dir); err != nil {
		return nil, fmt.Errorf("git clone --branch %s %s: %w", branch, repo, err)
	}
	if len(paths) > 0 {
		// Leading "/" anchors each pattern at the root; pack.Load refused
		// any path holding pattern syntax.
		patterns := make([]string, len(paths))
		for i, p := range paths {
			patterns[i] = "/" + p
		}
		if err := git(append([]string{"-C", dir, "sparse-checkout", "set", "--no-cone", "--"}, patterns...)...); err != nil {
			return nil, fmt.Errorf("git sparse-checkout: %w", err)
		}
		if err := git("-C", dir, "checkout", "--quiet"); err != nil {
			return nil, fmt.Errorf("git checkout: %w", err)
		}
	}

	files := map[string][]byte{}
	for _, p := range paths {
		if underSymlink(dir, p) {
			continue
		}
		data, err := safefile.Read(filepath.Join(dir, filepath.FromSlash(p)), safefile.Limit)
		switch {
		case err == nil:
			files[p] = data
		case errors.Is(err, fs.ErrNotExist), errors.Is(err, safefile.ErrRefused):
		default:
			return nil, err
		}
	}
	return files, nil
}

// underSymlink reports whether a parent directory of rel inside dir is a
// symlink or not a directory: safefile.Read follows those.
func underSymlink(dir, rel string) bool {
	parts := strings.Split(rel, "/")
	cur := dir
	for _, part := range parts[:len(parts)-1] {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if err != nil || !fi.IsDir() {
			return true
		}
	}
	return false
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
