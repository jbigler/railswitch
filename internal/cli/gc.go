package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/lockstore"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/podman"
	"github.com/jbigler/railswitch/internal/workspace"
)

const gcUsage = "usage: railswitch gc [--dry-run] [--volumes] [--yes]"

// gcDoc is `gc --json` (docs/json.md): what gc removes (or, with
// --dry-run, would), and what it leaves for you to decide.
type gcDoc struct {
	DryRun   bool      `json:"dry_run"`
	Images   []gcImage `json:"images"`
	Unsure   []gcImage `json:"unsure"`
	Store    []string  `json:"store"`
	Volumes  []string  `json:"volumes"`
	Bytes    int64     `json:"bytes"` // the images' size, layers they share with kept images included
	Warnings []string  `json:"warnings"`
}

type gcImage struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

var (
	// localImageRe is an image railswitch builds: localhost/<prefix>/<repo>:<version>-<hash8>.
	localImageRe = regexp.MustCompile(`^localhost/([a-z][a-z0-9]{1,15})/[a-z0-9][a-z0-9._-]*:[A-Za-z0-9._-]+-[0-9a-f]{8}$`)
	storeEntryRe = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// gcKeep is what the registered projects use now.
type gcKeep struct {
	tags, volumes, shas map[string]bool
	prefixes            map[string]bool // registered
	blocked             map[string]bool // registered but unreadable: keep everything of theirs
	// volumeNames are, by prefix, the volume names a railswitch package
	// declares (placeholders as wildcards): only those are railswitch's.
	volumeNames map[string][]*regexp.Regexp
}

// railswitch gc (D120): remove the images, stored package copies and (with
// --volumes) volumes no registered project uses, each by its exact name.
// An image is kept when any worktree's current render builds it or any
// container uses it; a stored copy when any stack.lock or [template]
// names it; a volume when any worktree's render mounts it. A project that
// doesn't load keeps everything of its prefix, and stops the store's
// cleanup. Images of an unregistered prefix go only when they carry
// railswitch's build label; older ones are listed as unsure.
func cmdGC(ctx context.Context, e *env, args []string) int {
	fs := e.flags("gc")
	dry := fs.Bool("dry-run", false, "list what would be removed; remove nothing")
	vols := fs.Bool("volumes", false, "also remove the volumes no worktree mounts (their data is lost)")
	yes := fs.Bool("yes", false, "remove the volumes without asking")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return e.usageErr("%s", gcUsage)
	}
	dirs, err := machine.DefaultDirs()
	if err != nil {
		return e.fail(err)
	}
	doc := gcDoc{DryRun: *dry, Images: []gcImage{}, Unsure: []gcImage{}, Store: []string{}, Volumes: []string{}, Warnings: []string{}}
	keep, err := e.gcKeep(ctx, dirs, &doc)
	if err != nil {
		return e.fail(err)
	}
	p := podman.Podman{R: e.run}
	imgs, err := p.Images(ctx)
	if err != nil {
		return e.fail(err)
	}
	// podman lists an image once per tag, each entry with every name.
	seenName, seenID := map[string]bool{}, map[string]bool{}
	for _, img := range imgs {
		if seenID[img.ID] {
			continue
		}
		seenID[img.ID] = true
		gone := 0
		for _, name := range img.Names {
			if seenName[name] {
				continue
			}
			seenName[name] = true
			switch gcImageVerdict(name, img, keep) {
			case "remove":
				doc.Images = append(doc.Images, gcImage{Name: name, Size: img.Size})
				gone++
			case "unsure":
				doc.Unsure = append(doc.Unsure, gcImage{Name: name, Size: img.Size})
			}
		}
		if gone > 0 && gone == len(img.Names) {
			doc.Bytes += img.Size
		}
	}
	if len(keep.blocked) == 0 {
		doc.Store = gcStore(dirs, keep)
	}
	names, err := p.Volumes(ctx)
	if err != nil {
		return e.fail(err)
	}
	for _, v := range names {
		if pfx := volumePrefix(v); keep.prefixes[pfx] && !keep.blocked[pfx] && !keep.volumes[v] && keep.railswitchVolume(v) {
			doc.Volumes = append(doc.Volumes, v)
		}
	}
	slices.Sort(doc.Volumes)

	// The volumes are confirmed (listed by name) before anything at all is
	// removed: a refusal removes nothing.
	removeVolumes := false
	if !*dry && *vols && len(doc.Volumes) > 0 {
		ok, err := e.gcConfirm(doc.Volumes, *yes)
		if err != nil {
			return e.fail(err)
		}
		removeVolumes = ok
		if !ok {
			doc.Warnings = append(doc.Warnings, fmt.Sprintf("kept the %d volumes: not confirmed", len(doc.Volumes)))
		}
	}
	if !*dry {
		for _, img := range doc.Images {
			if err := p.ImageRemove(ctx, img.Name); err != nil {
				doc.Warnings = append(doc.Warnings, fmt.Sprintf("image %s: %v", img.Name, err))
			}
		}
		store := lockstore.Dir(dirs)
		for _, sha := range doc.Store {
			if err := os.RemoveAll(filepath.Join(store, sha)); err != nil {
				doc.Warnings = append(doc.Warnings, fmt.Sprintf("stored copy %s: %v", sha, err))
			}
		}
		if removeVolumes {
			for _, v := range doc.Volumes {
				if err := p.VolumeRemove(ctx, v); err != nil {
					doc.Warnings = append(doc.Warnings, fmt.Sprintf("volume %s: %v", v, err))
				}
			}
		}
	}
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(doc); err != nil {
			return e.fail(err)
		}
		return 0
	}
	e.printGC(doc, removeVolumes || *dry && *vols)
	return 0
}

// gcKeep loads every registered project and collects what it uses.
func (e *env) gcKeep(ctx context.Context, dirs machine.Dirs, doc *gcDoc) (gcKeep, error) {
	keep := gcKeep{tags: map[string]bool{}, volumes: map[string]bool{}, shas: map[string]bool{}, prefixes: map[string]bool{}, blocked: map[string]bool{}, volumeNames: map[string][]*regexp.Regexp{}}
	reg, err := readRegistry(dirs)
	if err != nil {
		return keep, err
	}
	host, err := workspace.ReadHost(ctx, e.run)
	if err != nil {
		return keep, err
	}
	for _, pe := range reg.Projects {
		keep.prefixes[pe.Prefix] = true
		block := func(err error) {
			keep.blocked[pe.Prefix] = true
			doc.Warnings = append(doc.Warnings, fmt.Sprintf("project %s (%s) doesn't load, so nothing of it is removed and stored copies are kept: %v", pe.Prefix, pe.Workspace, err))
		}
		w, err := workspace.Load(ctx, pe.Workspace, "")
		if err != nil {
			block(err)
			continue
		}
		m, err := w.Model(reg, host)
		if err != nil {
			block(err)
			continue
		}
		keep.volumeNames[pe.Prefix] = declaredVolumes(pe.Prefix, w.Catalog)
		if l := w.Lock; l != nil {
			for _, t := range []map[string]config.LockEntry{l.Packs, l.Options, l.Agents, l.Tools, l.Base} {
				for _, en := range t {
					keep.shas[en.SHA256] = true
				}
			}
			for _, r := range l.Removed {
				keep.shas[r.Entry().SHA256] = true
			}
		}
		if t := w.Project.Template; t != nil {
			keep.shas[t.SHA256] = true
		}
		for _, wt := range m.Inputs.Worktrees {
			in, err := m.Instance(wt)
			if err != nil {
				block(err)
				break
			}
			for _, b := range in.Builds {
				keep.tags[b.Tag] = true
			}
			for i := range m.Services {
				specs, err := in.VolumeSpecs(&m.Services[i])
				if err != nil {
					block(err)
					break
				}
				for _, s := range specs {
					if name, _, _ := strings.Cut(s, ":"); !strings.HasPrefix(name, "/") {
						keep.volumes[name] = true
					}
				}
			}
		}
	}
	return keep, nil
}

// placeholderRe is a {placeholder} in a declared volume name.
var placeholderRe = regexp.MustCompile(`\{[^}]*\}`)

// declaredVolumes are the volume names of prefix that cat's packages
// declare, as patterns: <prefix>_<name> for a pack's caches and a tool's
// or agent's shared volumes, <prefix>-<slug>-<name> for a pack's worktree
// volumes and an option's volumes. Another tool's <prefix>_… volume (a
// compose project of that name) matches none.
func declaredVolumes(prefix string, cat *pack.Catalog) []*regexp.Regexp {
	var out []*regexp.Regexp
	prefix = regexp.QuoteMeta(prefix) // a registry edited by hand
	add := func(sep string, vols []pack.Volume) {
		for _, v := range vols {
			parts := placeholderRe.Split(v.Name, -1)
			for i := range parts {
				parts[i] = regexp.QuoteMeta(parts[i])
			}
			name := strings.Join(parts, `[A-Za-z0-9._-]+`)
			if sep == "-" {
				out = append(out, regexp.MustCompile(`^`+prefix+`-[a-z0-9][a-z0-9-]*-`+name+`$`))
			} else {
				out = append(out, regexp.MustCompile(`^`+prefix+`_`+name+`$`))
			}
		}
	}
	for _, pk := range cat.Packs {
		if pk != nil {
			add("_", pk.Caches)
			add("-", pk.WorktreeVolumes)
		}
	}
	for _, o := range cat.Options {
		if o != nil {
			add("-", o.Volumes)
		}
	}
	for _, t := range cat.Tools {
		if t != nil {
			add("_", t.Shared)
		}
	}
	for _, a := range cat.Agents {
		if a != nil {
			add("_", a.Shared)
		}
	}
	return out
}

// railswitchVolume reports whether v is a volume a package of a registered
// prefix declares.
func (k gcKeep) railswitchVolume(v string) bool {
	return slices.ContainsFunc(k.volumeNames[volumePrefix(v)], func(re *regexp.Regexp) bool { return re.MatchString(v) })
}

// gcImageVerdict is what gc does with the image name of img: "remove",
// "unsure" (looks like railswitch's, of no registered project, without the
// build label) or "" (keep, or not railswitch's).
func gcImageVerdict(name string, img podman.Image, keep gcKeep) string {
	m := localImageRe.FindStringSubmatch(name)
	switch {
	case m == nil, img.Containers > 0, keep.tags[name], keep.blocked[m[1]]:
		return ""
	case keep.prefixes[m[1]], img.Labels[podman.PrefixLabel] == m[1]:
		return "remove"
	}
	return "unsure"
}

// gcStore is the stored copies (D96) no stack.lock or [template] names.
// A copy changed in the last storeGrace is kept: a lock or an import may
// be writing the file that will name it.
func gcStore(dirs machine.Dirs, keep gcKeep) []string {
	out := []string{}
	ents, _ := os.ReadDir(lockstore.Dir(dirs))
	for _, ent := range ents {
		if !ent.IsDir() || !storeEntryRe.MatchString(ent.Name()) || keep.shas[ent.Name()] {
			continue
		}
		if fi, err := ent.Info(); err != nil || time.Since(fi.ModTime()) < storeGrace {
			continue
		}
		out = append(out, ent.Name())
	}
	return out
}

const storeGrace = 10 * time.Minute

// volumePrefix is the project prefix a railswitch volume name starts with:
// <prefix>-<slug>-<name> or <prefix>_<name>; "" for none.
func volumePrefix(v string) string {
	i := strings.IndexAny(v, "-_")
	if i <= 0 {
		return ""
	}
	return v[:i]
}

// gcConfirm asks on the terminal (stderr, so --json stays one document)
// whether to delete vols, each named; --yes answers for it.
func (e *env) gcConfirm(vols []string, yes bool) (bool, error) {
	if yes {
		return true, nil
	}
	in, ok := e.stdin.(*os.File)
	if !ok || !isTerminal(in) {
		return false, fmt.Errorf("gc --volumes deletes %d volumes and their data (%s): pass --yes (no terminal to confirm on); nothing was removed", len(vols), strings.Join(vols, ", "))
	}
	fmt.Fprintln(e.stderr, "These volumes, and the data in them, would be deleted:")
	for _, v := range vols {
		fmt.Fprintf(e.stderr, "  %s\n", v)
	}
	fmt.Fprintf(e.stderr, "Delete these %d volumes? [y/N] ", len(vols))
	line, _ := bufio.NewReader(in).ReadString('\n')
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes", nil
}

func (e *env) printGC(doc gcDoc, vols bool) {
	verb := "removed"
	if doc.DryRun {
		verb = "would remove"
	}
	fmt.Fprintf(e.stdout, "images: %s %d (%s)\n", verb, len(doc.Images), humanBytes(doc.Bytes))
	for _, img := range doc.Images {
		fmt.Fprintf(e.stdout, "  %s\n", img.Name)
	}
	fmt.Fprintf(e.stdout, "stored package copies: %s %d\n", verb, len(doc.Store))
	switch {
	case len(doc.Volumes) == 0:
		fmt.Fprintln(e.stdout, "volumes: none unused")
	case vols:
		fmt.Fprintf(e.stdout, "volumes: %s %d (their data with them)\n", verb, len(doc.Volumes))
	default:
		fmt.Fprintf(e.stdout, "volumes: %d no worktree mounts (railswitch gc --volumes removes them, with their data):\n", len(doc.Volumes))
	}
	for _, v := range doc.Volumes {
		fmt.Fprintf(e.stdout, "  %s\n", v)
	}
	if len(doc.Unsure) > 0 {
		fmt.Fprintln(e.stdout, "not removed: images of no registered project, built before railswitch labelled its images; remove each you don't need:")
		for _, img := range doc.Unsure {
			fmt.Fprintf(e.stdout, "  podman rmi %s\n", img.Name)
		}
	}
	for _, w := range doc.Warnings {
		fmt.Fprintf(e.stderr, "warning: %s\n", w)
	}
}

// humanBytes is n as "1.4 GB".
func humanBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}
