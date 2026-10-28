package template

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing/fstest"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/pack"
)

// Export is the template name@version of project p: its lines without the
// project-only ones (returned as dropped, each a line of text), a question
// per [options] key, [versions] pin and [env] value (default: p's value),
// p's secret declarations (never a value: project.toml holds none), and
// containerfiles, Containerfile.<name> by name: the ones the caller judged
// edited from their default, and user addons. p's [image] addons are kept
// only when containerfiles holds them: an option's addon comes from its
// eject in the new project.
func Export(p *config.Project, containerfiles map[string][]byte, name string, version int) (*Template, []string, error) {
	var dropped []string
	if p.From != "" {
		dropped = append(dropped, "from")
	}
	m := p.Project
	for _, kv := range [][2]string{{"name", m.Name}, {"prefix", m.Prefix}, {"repo", m.Repo}, {"base_branch", m.BaseBranch}} {
		if kv[1] != "" {
			dropped = append(dropped, "[project] "+kv[0])
		}
	}
	if len(m.SeedFiles) > 0 {
		dropped = append(dropped, "[project] seed_files")
	}
	for _, slug := range slices.Sorted(maps.Keys(p.Worktrees)) {
		dropped = append(dropped, "[worktrees."+slug+"]")
	}
	if len(p.Share.Env) > 0 {
		dropped = append(dropped, "[share]")
	}
	if p.Template != nil {
		dropped = append(dropped, "[template]")
	}

	lines := *p
	lines.From, lines.Project, lines.Worktrees, lines.Share, lines.Template = "", config.Meta{}, nil, config.Share{}, nil
	lines.Image = config.ImageConfig{}
	for _, a := range p.Image.Addons {
		if n, ok := strings.CutPrefix(a, cfDir+"/Containerfile."); ok && containerfiles[n] != nil {
			lines.Image.Addons = append(lines.Image.Addons, a)
		}
	}
	t := &Template{Schema: SchemaVersion, Name: name, Version: version, Lines: &lines, Ask: []Ask{}}
	for _, k := range pack.OptionKeys(p.Options) {
		if v, ok := p.Options[k]; ok {
			t.Ask = append(t.Ask, Ask{Key: "options." + k, Prompt: "Option for " + k + " (a ref, or none)", Default: v})
		}
	}
	for _, k := range slices.Sorted(maps.Keys(p.Versions)) {
		t.Ask = append(t.Ask, Ask{Key: "versions." + k, Prompt: "Version of " + k, Default: p.Versions[k]})
	}
	for _, k := range slices.Sorted(maps.Keys(p.Env)) {
		t.Ask = append(t.Ask, Ask{Key: "env." + k, Prompt: "Value of " + k, Default: p.Env[k]})
	}
	t.Secrets = slices.Sorted(maps.Keys(p.Secrets))
	t.Containerfiles = containerfiles
	out, err := t.Build()
	if err != nil {
		return nil, nil, fmt.Errorf("the exported template does not decode: %w", err)
	}
	return out, dropped, nil
}

// Build is t's files made from its fields (stack.toml encoded, its
// Containerfiles and README), decoded: what Write writes after a field
// changed.
func (t *Template) Build() (*Template, error) {
	files := fstest.MapFS{StackFile: {Data: t.Encode(), Mode: 0o644}}
	for n, data := range t.Containerfiles {
		files[cfDir+"/Containerfile."+n] = &fstest.MapFile{Data: data, Mode: 0o644}
	}
	if t.Readme != nil {
		files[ReadmeFile] = &fstest.MapFile{Data: t.Readme, Mode: 0o644}
	}
	return Decode(files)
}
