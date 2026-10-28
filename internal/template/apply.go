package template

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/pack"
)

// Apply is t's lines with every question answered: answers by key, else
// the question's default. An answer for a key t does not ask is an error.
// The lines are a copy: t is unchanged.
func Apply(t *Template, answers map[string]string) (*config.Project, error) {
	for _, k := range slices.Sorted(maps.Keys(answers)) {
		if !slices.ContainsFunc(t.Ask, func(a Ask) bool { return a.Key == k }) {
			keys := make([]string, len(t.Ask))
			for i, a := range t.Ask {
				keys[i] = a.Key
			}
			return nil, fmt.Errorf("--answer %s: template %s asks no such question (it asks: %s)", k, t.Ref(), orNone(keys))
		}
	}
	p := *t.Lines
	p.Options, p.Versions, p.Env = maps.Clone(p.Options), maps.Clone(p.Versions), maps.Clone(p.Env)
	for _, a := range t.Ask {
		v, ok := answers[a.Key]
		if !ok {
			v = a.Default
		}
		table, k, _ := strings.Cut(a.Key, ".")
		var m *map[string]string
		switch table {
		case "options":
			m = &p.Options
		case "versions":
			m = &p.Versions
		case "env":
			m = &p.Env
		}
		if *m == nil {
			*m = map[string]string{}
		}
		(*m)[k] = v
	}
	return &p, nil
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}

// Dir is this machine's template store: <config>/templates (D109).
func Dir(d machine.Dirs) string { return filepath.Join(d.Config, "templates") }

// LocalDir is the store's directory of the template name.
func LocalDir(d machine.Dirs, name string) string {
	return filepath.Join(Dir(d), name+".stack")
}

// Local is the names of the templates in this machine's store, sorted:
// every <name>.stack entry, whether or not it decodes.
func Local(d machine.Dirs) ([]string, error) {
	ents, err := os.ReadDir(Dir(d))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if n, ok := strings.CutSuffix(e.Name(), ".stack"); ok && nameRe.MatchString(n) {
			out = append(out, n)
		}
	}
	return out, nil
}

// Resolve is the template ref names and its content hash: "<name>@N" from
// this machine's store, "<alias>:<name>@N" from that library's verified
// checkout (as its signed index lists it, with the hash its directory has
// now). A local: ref, a template that doesn't decode, or one whose
// stack.toml declares another name or version, is an error.
func Resolve(ref string, d machine.Dirs, libs pack.Libraries) (*Template, string, error) {
	r, err := pack.ParseRef(ref)
	if err != nil {
		return nil, "", err
	}
	if r.Local {
		return nil, "", fmt.Errorf("%s: a template is this machine's (<name>@N) or a library's (<alias>:<name>@N), never a local: ref", ref)
	}
	var files fs.FS
	var sum string
	if r.Library != "" {
		if libs == nil {
			return nil, "", fmt.Errorf("%s: library templates need the machine's libraries", ref)
		}
		if files, sum, _, err = pack.LibraryTree(libs, pack.KindTemplate, r); err != nil {
			return nil, "", err
		}
	} else {
		dir := LocalDir(d, r.Name)
		if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
			return nil, "", fmt.Errorf("no template %s on this machine (%s is missing): railswitch template ls lists them", ref, dir)
		}
		if files, err = ReadDir(dir); err != nil {
			return nil, "", fmt.Errorf("template %s: %w", ref, err)
		}
		if sum, err = pack.ContentHash(files); err != nil {
			return nil, "", fmt.Errorf("template %s: %w", ref, err)
		}
	}
	t, err := Decode(files)
	if err != nil {
		return nil, "", fmt.Errorf("template %s: %w", ref, err)
	}
	if t.Name != r.Name || t.Version != r.Version {
		return nil, "", fmt.Errorf("template %s: its %s declares %s", ref, StackFile, t.Ref())
	}
	return t, sum, nil
}
