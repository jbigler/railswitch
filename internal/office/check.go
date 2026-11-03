package office

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/safefile"
)

// The limits of a set folder (D153).
const (
	MaxFiles     = 1000
	MaxFileSize  = 16 << 20
	MaxTotalSize = 256 << 20
	Tolerance    = 0.02 // meters, per face of the box
)

// Result is one piece's verdict: ok, missing, or refused with a reason.
type Result struct {
	Piece  string      `json:"piece"`
	Status string      `json:"status"`
	Reason string      `json:"reason,omitempty"`
	Min    *[3]float64 `json:"min,omitempty"`
	Max    *[3]float64 `json:"max,omitempty"`
}

// Report is office set check's answer.
type Report struct {
	Name    string   `json:"name"`
	Version int      `json:"version"`
	Pieces  int      `json:"pieces"`
	Look    Look     `json:"look"`
	Results []Result `json:"results"`
	Errors  []string `json:"errors"`
	// Files are the folder's files (slash paths) a check accepted: set.toml,
	// the pieces' glTFs and what they reference. Add copies these.
	Files []string `json:"-"`
}

// OK reports a set with nothing refused and no folder or set.toml error.
func (r *Report) OK() bool {
	return len(r.Errors) == 0 && !slices.ContainsFunc(r.Results, func(x Result) bool { return x.Status == "refused" })
}

// Have are the pieces a check found ok.
func (r *Report) Have() []string {
	var out []string
	for _, x := range r.Results {
		if x.Status == "ok" {
			out = append(out, x.Piece)
		}
	}
	return out
}

func (r *Report) errorf(format string, a ...any) {
	r.Errors = append(r.Errors, fmt.Sprintf(format, a...))
}

// Check reads the set folder dir as hostile and writes nothing: symlinks
// (dir included), anything but regular files and folders, too many or too
// large files, stray files and URIs that leave the folder are folder
// errors; each piece is ok, missing or refused against list. It fails only
// when dir can't be read at all.
func Check(dir string, list *Pieces) (*Report, error) {
	dir, err := filepath.Abs(dir) // Abs cleans: a trailing / would make Lstat follow a symlink
	if err != nil {
		return nil, err
	}
	r := &Report{Results: []Result{}, Errors: []string{}}
	fi, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		r.errorf("%s is a symbolic link: give the folder itself", dir)
		return r, nil
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a folder", dir)
	}
	files := map[string]bool{} // slash path → referenced or a known role
	var total int64
	walkErr := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			r.errorf("%s: a symbolic link", rel)
			return nil
		case d.IsDir():
			return nil
		case !d.Type().IsRegular():
			r.errorf("%s: not a regular file", rel)
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > MaxFileSize {
			r.errorf("%s: %d bytes, more than %d", rel, info.Size(), MaxFileSize)
		}
		total += info.Size()
		files[rel] = false
		if len(files) > MaxFiles {
			return fmt.Errorf("more than %d files", MaxFiles)
		}
		return nil
	})
	if walkErr != nil {
		r.errorf("%v", walkErr)
		return r, nil
	}
	if total > MaxTotalSize {
		r.errorf("%d bytes in all, more than %d", total, MaxTotalSize)
	}

	if _, ok := files["set.toml"]; !ok {
		r.errorf("set.toml: missing")
	} else if data, err := readCapped(filepath.Join(dir, "set.toml")); err != nil {
		r.errorf("set.toml: %v", err)
	} else if s, err := ParseSet(data); err != nil {
		r.errorf("set.toml: %v", err)
	} else {
		r.Name, r.Version, r.Pieces, r.Look = s.Name, s.Version, s.Pieces, s.Look
		if s.Pieces != list.Version {
			r.errorf("set.toml: built for piece list %d; this railswitch has %d", s.Pieces, list.Version)
		}
		files["set.toml"] = true
	}

	found := map[string]bool{}
	for rel := range files {
		name, ok := strings.CutSuffix(rel, ".gltf")
		if !ok || strings.Contains(rel, "/") {
			continue
		}
		files[rel] = true
		found[name] = true
		res := Result{Piece: name, Status: "ok"}
		p, known := list.Pieces[name]
		data, err := readCapped(filepath.Join(dir, rel))
		var g gltfDoc
		if err == nil {
			err = json.Unmarshal(data, &g)
		}
		switch {
		case !known:
			res.Status, res.Reason = "refused", name+": not in the piece list"
		case err != nil:
			res.Status, res.Reason = "refused", fmt.Sprintf("%s: not a glTF: %v", name, err)
		}
		if err == nil {
			for _, u := range g.uris() {
				ref, bad := refPath(u)
				if bad != "" {
					r.errorf("%s: URI %q %s", rel, u, bad)
				} else if _, ok := files[ref]; !ok {
					r.errorf("%s: URI %q: no such file in the folder", rel, u)
				} else {
					files[ref] = true
				}
			}
		}
		if res.Status == "ok" {
			if x := slices.IndexFunc(g.ExtensionsRequired, func(e string) bool { return slices.Contains(undecoded, e) }); x >= 0 {
				res.Status, res.Reason = "refused", fmt.Sprintf("%s: compressed (%s), which the office can't decode: export without compression", name, g.ExtensionsRequired[x])
			}
		}
		if res.Status == "ok" {
			lo, hi, err := g.bounds()
			if err != nil {
				res.Status, res.Reason = "refused", name+": "+err.Error()
			} else {
				res.Min, res.Max = &lo, &hi
				if !near(lo, p.Min) || !near(hi, p.Max) {
					res.Status = "refused"
					res.Reason = fmt.Sprintf("%s: %s m; the list says %s (±%.2f)", name, box(lo, hi), box(p.Min, p.Max), Tolerance)
				}
			}
		}
		r.Results = append(r.Results, res)
	}
	for name := range list.Pieces {
		if !found[name] {
			r.Results = append(r.Results, Result{Piece: name, Status: "missing"})
		}
	}
	slices.SortFunc(r.Results, func(a, b Result) int { return strings.Compare(a.Piece, b.Piece) })
	for rel, used := range files {
		if used {
			r.Files = append(r.Files, rel)
		} else {
			r.errorf("%s: neither set.toml, a piece's .gltf, nor a file one references", rel)
		}
	}
	slices.Sort(r.Files)
	slices.Sort(r.Errors)
	return r, nil
}

func (g *gltfDoc) uris() []string {
	var out []string
	for _, b := range g.Buffers {
		if b.URI != "" {
			out = append(out, b.URI)
		}
	}
	for _, i := range g.Images {
		if i.URI != "" {
			out = append(out, i.URI)
		}
	}
	return out
}

// undecoded are the glTF extensions the page's three.js loader has no
// decoder for: the page sets no Draco, Meshopt or KTX2 decoder.
var undecoded = []string{"KHR_draco_mesh_compression", "EXT_meshopt_compression", "KHR_texture_basisu"}

// refPath is a glTF URI's file in the folder, or why it is refused: a
// scheme (data: included: the page's CSP blocks three.js fetching one),
// an absolute path, or one that leaves the folder.
func refPath(u string) (string, string) {
	if i := strings.IndexAny(u, ":/?#"); i >= 0 && u[i] == ':' {
		return "", "has a scheme: export as glTF Separate, files beside the .gltf"
	}
	p, err := url.PathUnescape(u)
	if err != nil {
		return "", "is not a valid URI"
	}
	if strings.ContainsAny(p, "\\\x00") || path.IsAbs(p) {
		return "", "is absolute"
	}
	c := path.Clean(p)
	if c == ".." || strings.HasPrefix(c, "../") || c == "." {
		return "", "leaves the folder"
	}
	return c, ""
}

// readCapped reads a file of the folder again refusing a symlink and a
// file over MaxFileSize: it may have changed since the walk.
func readCapped(p string) ([]byte, error) { return safefile.Read(p, MaxFileSize) }

func near(a, b [3]float64) bool {
	for k := range 3 {
		if math.Abs(a[k]-b[k]) > Tolerance+1e-9 {
			return false
		}
	}
	return true
}

func box(lo, hi [3]float64) string {
	return fmt.Sprintf("x %.2f..%.2f, y %.2f..%.2f, z %.2f..%.2f", lo[0], hi[0], lo[1], hi[1], lo[2], hi[2])
}
