// Package firewall renders the egress firewall's workspace files: the OCI
// hook descriptor podman reads from --hooks-dir, the hook and fw scripts
// (embedded, ported from rails-dev-podman) and the profiles' allowlists.
// crun runs the hook; it stays shell.
package firewall

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/render"
)

// Dir is the firewall's workspace-relative directory; quadlet.go's
// --hooks-dir names its hooks.d.
const Dir = ".railswitch/firewall"

// hookPath is the PATH the hook runs with.
const hookPath = "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

//go:embed assets/hook.sh assets/firewall.sh
var assets embed.FS

// descriptor is an OCI hook descriptor (containers-common oci-hooks(5)).
type descriptor struct {
	Version string `json:"version"`
	Hook    struct {
		Path    string   `json:"path"`
		Args    []string `json:"args"`
		Env     []string `json:"env"`
		Timeout int      `json:"timeout"`
	} `json:"hook"`
	When struct {
		Annotations map[string]string `json:"annotations"`
	} `json:"when"`
	Stages []string `json:"stages"`
}

// Files are the firewall's workspace files for project prefix in workspace
// ws, keyed by workspace-relative slash path: hook.sh and firewall.sh
// (0755), app.allow (the pack's app.allow, then fw.Allow["app"]: the app
// and its workers, D119), deps.allow (the pack's deps.allow, then fw.Allow["deps"],
// de-duplicated, order kept), agent.allow (fw.Allow["agent"]), one
// agent-<name>.allow per agent (its Allow, then fw.Allow["agent"]: C10) and
// the hook descriptor hooks.d/<prefix>-firewall.json, whose hook reads
// netavark's network configs in networkDir.
func Files(prefix, ws, networkDir string, st *pack.Stack, fw config.Firewall) (map[string]render.File, error) {
	out := map[string]render.File{}
	for _, name := range []string{"hook.sh", "firewall.sh"} {
		data, err := assets.ReadFile("assets/" + name)
		if err != nil {
			return nil, err
		}
		out[path.Join(Dir, name)] = render.File{Data: data, Mode: 0o755}
	}

	packList, err := fs.ReadFile(st.Pack.Files, "deps.allow")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("pack %s: %w", st.Pack.Name, err)
	}
	var errs []error
	for _, p := range []string{"app", "deps", "agent"} {
		for i, e := range fw.Allow[p] {
			if e == "" || strings.ContainsAny(e, "# \t\r\n\v\f") {
				errs = append(errs, fmt.Errorf("firewall.allow.%s[%d] %q: want one hostname, IPv4 address or CIDR, or meta:<name>", p, i, e))
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	deps := allowlist("# The deps profile's allowlist: the pack's ("+st.Pack.Name+") deps.allow,\n# then [firewall] allow.deps. Written by railswitch render.\n",
		entries(packList), fw.Allow["deps"])
	agent := allowlist("# The agent profile's allowlist: [firewall] allow.agent. Written by\n# railswitch render.\n", fw.Allow["agent"])
	appList, err := fs.ReadFile(st.Pack.Files, "app.allow")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("pack %s: %w", st.Pack.Name, err)
	}
	app := allowlist("# The app profile's allowlist (the app and its workers, D119): the pack's\n# ("+st.Pack.Name+") app.allow, then [firewall] allow.app. Written by railswitch render.\n",
		entries(appList), fw.Allow["app"])
	out[path.Join(Dir, "app.allow")] = render.File{Data: app, Mode: 0o644}
	out[path.Join(Dir, "deps.allow")] = render.File{Data: deps, Mode: 0o644}
	out[path.Join(Dir, "agent.allow")] = render.File{Data: agent, Mode: 0o644}
	for _, a := range st.Agents {
		list := allowlist("# The "+a.Service+" profile's allowlist: agent "+a.Name+"'s package ("+a.Ref.String()+") allow\n# and [agents."+a.Name+"] allow, then [firewall] allow.agent. Written by\n# railswitch render.\n",
			a.Allow, fw.Allow["agent"])
		out[path.Join(Dir, AllowFile(a.Service))] = render.File{Data: list, Mode: 0o644}
	}

	fwDir := filepath.Join(ws, filepath.FromSlash(Dir))
	var d descriptor
	d.Version = "1.0.0"
	d.Hook.Path = filepath.Join(fwDir, "hook.sh")
	d.Hook.Args = []string{"hook.sh", prefix, fwDir}
	d.Hook.Env = []string{hookPath, "NETWORK_DIR=" + networkDir}
	d.Hook.Timeout = 60
	d.When.Annotations = map[string]string{`^` + prefix + `\.firewall$`: ".*"}
	d.Stages = []string{"createRuntime"}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(d); err != nil {
		return nil, err
	}
	out[descriptorRel(prefix)] = render.File{Data: b.Bytes(), Mode: 0o644}
	return out, nil
}

// descriptorRel is project prefix's hook descriptor, as a workspace-relative
// slash path.
func descriptorRel(prefix string) string {
	return path.Join(Dir, "hooks.d", prefix+"-firewall.json")
}

// AllowFile is profile's allowlist file name in Dir.
func AllowFile(profile string) string {
	return profile + ".allow"
}

// DescriptorPath is project prefix's hook descriptor in workspace ws:
// without it, podman starts the project's containers unfirewalled.
func DescriptorPath(ws, prefix string) string {
	return filepath.Join(ws, filepath.FromSlash(descriptorRel(prefix)))
}

// entries is an allowlist file's entries: comments and blank lines dropped,
// whitespace trimmed.
func entries(data []byte) []string {
	var out []string
	for l := range strings.SplitSeq(string(data), "\n") {
		l, _, _ = strings.Cut(l, "#")
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// allowlist is header, then every entry of lists in order, each once.
func allowlist(header string, lists ...[]string) []byte {
	b := []byte(header)
	seen := map[string]bool{}
	for _, l := range lists {
		for _, e := range l {
			if !seen[e] {
				seen[e] = true
				b = append(b, e...)
				b = append(b, '\n')
			}
		}
	}
	return b
}
