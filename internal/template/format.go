// Package template is the project template (D108–D111): a pack, options
// and a project's own lines, ready to start a new project from, with the
// questions it asks. Its format and checks, export from a project, apply
// to a new project.toml, and where templates live: this machine's
// <config>/templates/<name>.stack/ or a library's templates/<name>/.
package template

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing/fstest"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/tomlfile"
)

// SchemaVersion is the stack.toml schema this railswitch reads (D39).
const SchemaVersion = 1

// The files of a template directory.
const (
	StackFile  = "stack.toml"
	ReadmeFile = "README.md"
	cfDir      = "containerfiles"
)

// Ask is one question a new project answers: the value at Key, a dotted
// project.toml path "options.<key>", "versions.<name>" or "env.<NAME>".
type Ask struct {
	Key     string `toml:"key" json:"key"`
	Prompt  string `toml:"prompt" json:"prompt"`
	Default string `toml:"default" json:"default"`
}

// Template is a decoded template directory.
type Template struct {
	Schema      int
	Name        string
	Version     int
	Description string
	// Lines are the project.toml lines a new project starts from: no
	// [project], [worktrees], [share] or [template]; Secrets are the
	// declarations of [[secret]]; Image holds only the user addons.
	Lines   *config.Project
	Ask     []Ask
	Secrets []string // the declared secrets' names, sorted
	// Containerfiles are containerfiles/Containerfile.<name>, by name:
	// only those edited from their default, and user addons.
	Containerfiles map[string][]byte
	Readme         []byte
	Files          fs.FS // every file, as decoded: what its content hash covers
}

// Ref is t's ref on this machine: "<name>@<version>".
func (t *Template) Ref() string { return fmt.Sprintf("%s@%d", t.Name, t.Version) }

// Packages are the package refs t's lines use: the pack, each option
// (slot order, then the extra services) and each agent's.
func (t *Template) Packages() []string {
	out := []string{t.Lines.Stack.Pack}
	for _, k := range pack.OptionKeys(t.Lines.Options) {
		if v, ok := t.Lines.Options[k]; ok && v != "none" {
			out = append(out, v)
		}
	}
	for _, n := range slices.Sorted(maps.Keys(t.Lines.Agents)) {
		out = append(out, t.Lines.Agents[n].Use)
	}
	return out
}

// stackFile is stack.toml as decoded: only these keys, so a [project],
// [worktrees], [share] or [secrets] line is an unknown key, refused.
type stackFile struct {
	Schema      int                               `toml:"schema"`
	Name        string                            `toml:"name"`
	Version     int                               `toml:"version"`
	Description string                            `toml:"description"`
	HideLinks   []string                          `toml:"hide_links"`
	Stack       config.StackRef                   `toml:"stack"`
	Options     map[string]string                 `toml:"options"`
	Versions    map[string]string                 `toml:"versions"`
	Env         map[string]string                 `toml:"env"`
	Services    map[string]config.ServiceOverride `toml:"services"`
	Tasks       map[string]string                 `toml:"tasks"`
	Links       []config.Link                     `toml:"links"`
	Routes      []config.Route                    `toml:"routes"`
	Tools       config.Tools                      `toml:"tools"`
	Agents      map[string]config.Agent           `toml:"agents"`
	Firewall    config.Firewall                   `toml:"firewall"`
	Image       struct {
		Addons []string `toml:"addons"`
	} `toml:"image"`
	Ask    []Ask        `toml:"ask"`
	Secret []secretDecl `toml:"secret"`
}

// secretDecl is one [[secret]]: a declaration, never a value (D33).
type secretDecl struct {
	Name string `toml:"name"`
	config.Secret
}

var (
	nameRe   = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	cfNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	askRe    = regexp.MustCompile(`^(options|versions|env)\.([A-Za-z_][A-Za-z0-9_-]*)$`)
)

// maxVersion keeps a version in the ref grammar (at most 9 digits).
const maxVersion = 999999999

// CheckPath refuses rel, a slash path in a template directory, unless it
// is stack.toml, README.md or containerfiles/Containerfile.<name>.
func CheckPath(rel string) error {
	if rel == StackFile || rel == ReadmeFile {
		return nil
	}
	if n, ok := strings.CutPrefix(rel, cfDir+"/Containerfile."); ok && cfNameRe.MatchString(n) {
		return nil
	}
	return fmt.Errorf("%q is not a template file: %s, %s or %s/Containerfile.<name>", rel, StackFile, ReadmeFile, cfDir)
}

// ReadDir reads the template directory dir into memory: regular files
// only, each through safefile (≤ 1 MiB); a symlink anywhere refuses.
func ReadDir(dir string) (fs.FS, error) {
	return pack.ReadTree(dir, "")
}

// Decode reads the template in fsys strictly: only CheckPath's files,
// regular ones; stack.toml with schema 1 and no key it doesn't define;
// every question's key a path its lines can hold, each asked once; and the
// lines with every default applied a valid project.toml once project new
// adds [project].
func Decode(fsys fs.FS) (*Template, error) {
	mem := fstest.MapFS{}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir():
			if p != "." && p != cfDir {
				return fmt.Errorf("%s/ is not a template directory: only %s/", p, cfDir)
			}
			return nil
		case !d.Type().IsRegular():
			return fmt.Errorf("%s: not a regular file; refused", p)
		}
		if err := CheckPath(p); err != nil {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		mem[p] = &fstest.MapFile{Data: data, Mode: 0o644}
		return nil
	})
	if err != nil {
		return nil, err
	}
	data, ok := mem[StackFile]
	if !ok {
		return nil, fmt.Errorf("no %s", StackFile)
	}
	var f stackFile
	if err := tomlfile.Decode(data.Data, SchemaVersion, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", StackFile, err)
	}
	t := &Template{
		Schema: f.Schema, Name: f.Name, Version: f.Version, Description: f.Description,
		Ask: f.Ask, Secrets: []string{}, Containerfiles: map[string][]byte{}, Files: mem,
		Lines: &config.Project{
			Schema: config.SchemaVersion, Stack: f.Stack, Options: f.Options, Versions: f.Versions, Env: f.Env,
			Services: f.Services, Tasks: f.Tasks, Links: f.Links, HideLinks: f.HideLinks, Routes: f.Routes,
			Tools: f.Tools, Agents: f.Agents, Firewall: f.Firewall, Image: config.ImageConfig{Addons: f.Image.Addons},
		},
	}
	if t.Ask == nil {
		t.Ask = []Ask{}
	}
	if r, ok := mem[ReadmeFile]; ok {
		t.Readme = r.Data
	}
	for p, file := range mem {
		if n, ok := strings.CutPrefix(p, cfDir+"/Containerfile."); ok {
			t.Containerfiles[n] = file.Data
		}
	}
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	switch {
	case f.Schema == 0:
		bad("%s: schema: missing; add `schema = %d` at the top", StackFile, SchemaVersion)
	case f.Schema != SchemaVersion:
		bad("%s: schema: %d is not %d", StackFile, f.Schema, SchemaVersion)
	}
	if !nameRe.MatchString(f.Name) {
		bad("%s: name: %q must match %s", StackFile, f.Name, nameRe)
	}
	if f.Version < 1 || f.Version > maxVersion {
		bad("%s: version: %d must be 1 or more", StackFile, f.Version)
	}
	if strings.ContainsAny(f.Description, "\r\n") {
		bad("%s: description: contains a newline", StackFile)
	}
	for i, a := range t.Ask {
		where := fmt.Sprintf("%s: ask[%d]", StackFile, i)
		if !askRe.MatchString(a.Key) {
			bad("%s.key: %q is not options.<key>, versions.<name> or env.<NAME>", where, a.Key)
		}
		if slices.ContainsFunc(t.Ask[:i], func(b Ask) bool { return b.Key == a.Key }) {
			bad("%s.key: %s is asked twice", where, a.Key)
		}
		if strings.ContainsAny(a.Prompt+a.Default, "\r\n") {
			bad("%s: contains a newline", where)
		}
	}
	for i, s := range f.Secret {
		if t.Lines.Secrets == nil {
			t.Lines.Secrets = map[string]config.Secret{}
		}
		if _, dup := t.Lines.Secrets[s.Name]; dup {
			bad("%s: secret[%d]: %s is declared twice", StackFile, i, s.Name)
		}
		t.Lines.Secrets[s.Name] = s.Secret
		t.Secrets = append(t.Secrets, s.Name)
	}
	slices.Sort(t.Secrets)
	for i, a := range t.Lines.Image.Addons {
		n, ok := strings.CutPrefix(a, cfDir+"/Containerfile.")
		if _, have := t.Containerfiles[n]; !ok || !have {
			bad("%s: image.addons[%d]: %q is no file of the template (%s/Containerfile.<name>)", StackFile, i, a, cfDir)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	p, err := Apply(t, nil)
	if err != nil {
		return nil, err
	}
	if err := checkLines(p); err != nil {
		return nil, fmt.Errorf("%s: %w", StackFile, err)
	}
	return t, nil
}

// checkLines validates p, a template's lines, as the project.toml project
// new makes of them: with [project] and [image] base filled in.
func checkLines(p *config.Project) error {
	q := *p
	q.Project = config.Meta{Name: "x", Prefix: "template", Repo: "x", BaseBranch: "x"}
	q.Image.Base = cfDir + "/Containerfile.base"
	return q.Validate()
}

// Encode is t's stack.toml.
func (t *Template) Encode() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "schema = %d\nname = %s\nversion = %d\n", SchemaVersion, config.QuoteString(t.Name), t.Version)
	if t.Description != "" {
		fmt.Fprintf(&b, "description = %s\n", config.QuoteString(t.Description))
	}
	b.WriteString(config.HideLinksTOML(t.Lines))
	b.WriteString(config.LinesTOML(t.Lines))
	if a := t.Lines.Image.Addons; len(a) > 0 {
		q := make([]string, len(a))
		for i, s := range a {
			q[i] = config.QuoteString(s)
		}
		fmt.Fprintf(&b, "\n[image]                    # user addons, applied after the options' own\naddons = [%s]\n", strings.Join(q, ", "))
	}
	for _, a := range t.Ask {
		fmt.Fprintf(&b, "\n[[ask]]\nkey = %s\nprompt = %s\ndefault = %s\n", config.QuoteString(a.Key), config.QuoteString(a.Prompt), config.QuoteString(a.Default))
	}
	for _, n := range t.Secrets {
		fmt.Fprintf(&b, "\n[[secret]]                 # a declaration: values live in podman secrets (D33)\nname = %s\n%s", config.QuoteString(n), config.SecretTOML(t.Lines.Secrets[n]))
	}
	return []byte(b.String())
}

// Write writes t's files as the directory dir: a temp directory beside it
// is written, then renamed into place; an existing dir (an older version:
// the caller decides) is moved aside first and removed after.
func (t *Template) Write(dir string) error {
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".template-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	err = fs.WalkDir(t.Files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(t.Files, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(tmp, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	old := ""
	switch _, err := os.Lstat(dir); {
	case err == nil:
		old = tmp + ".old"
		if err := os.Rename(dir, old); err != nil {
			return err
		}
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		if old != "" {
			err = errors.Join(err, os.Rename(old, dir))
		}
		return err
	}
	if old != "" {
		return os.RemoveAll(old)
	}
	return nil
}
