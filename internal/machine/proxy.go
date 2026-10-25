package machine

import (
	"bytes"
	"cmp"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/tomlfile"
	"github.com/jbigler/railswitch/internal/unithash"
)

// Marker starts the first line of every rendered unit file. It lives here
// rather than in render because render imports machine; render.Marker is it.
const Marker = "# railswitch: rendered from "

// ProxyUnitName is the shared Traefik unit's file name.
const ProxyUnitName = "railswitch-traefik.container"

// traefikFlags are Traefik's static configuration (fd7336b) minus
// --providers.docker.network: each routed container carries its own
// traefik.docker.network label.
var traefikFlags = []string{
	"--providers.docker=true",
	"--providers.docker.exposedByDefault=false",
	"--entryPoints.web.address=:80",
	"--entryPoints.web.http.encodedcharacters.allowencodedslash=true",
	"--entryPoints.web.http.encodedcharacters.allowencodedbackslash=true",
	"--entryPoints.web.http.encodedcharacters.allowencodednullcharacter=true",
	"--entryPoints.web.http.encodedcharacters.allowencodedsemicolon=true",
	"--entryPoints.web.http.encodedcharacters.allowencodedpercent=true",
	"--entryPoints.web.http.encodedcharacters.allowencodedquestionmark=true",
	"--entryPoints.web.http.encodedcharacters.allowencodedhash=true",
	"--api.insecure=true",
	"--log.level=INFO",
	"--accesslog=false",
}

// ProxyUnits renders the machine-wide units, keyed by filename: the shared
// Traefik container, joined to every registered project's proxy network at
// its fixed address (D21). With no project registered it renders nothing.
// Prefixes and subnets come from the registry and hold no % or $.
func ProxyUnits(r *Registry) map[string][]byte {
	if len(r.Projects) == 0 {
		return map[string][]byte{}
	}
	ps := slices.Clone(r.Projects)
	slices.SortFunc(ps, func(a, b ProjectEntry) int { return cmp.Compare(a.Subnet, b.Subnet) })

	var b strings.Builder
	line := func(s ...string) {
		for _, l := range s {
			b.WriteString(l + "\n")
		}
	}
	line(Marker+"railswitch (machine). Edit the railswitch registry, not this file.",
		"",
		"[Unit]",
		"Description=railswitch Traefik")
	for _, p := range ps {
		svc := strings.TrimSuffix(naming.ProxyNetworkUnit(p.Prefix), ".network") + "-network.service"
		line("Requires="+svc, "After="+svc)
	}
	line("", "[Container]",
		"Image=docker.io/traefik:v3.7",
		"ContainerName=railswitch-traefik")
	for _, p := range ps {
		line("Network=" + naming.ProxyNetworkUnit(p.Prefix) + ":ip=" + TraefikIP(p.Subnet))
	}
	line("PublishPort=127.0.0.1:80:80",
		"PublishPort=127.0.0.1:8080:8080",
		"Volume=%t/podman/podman.sock:/var/run/docker.sock:ro",
		"LogDriver=k8s-file",
		"LogOpt=max-size=20m",
		"Exec="+strings.Join(traefikFlags, " \\\n  "),
		"NoNewPrivileges=true",
		"Memory=512m",
		"PidsLimit=1024",
		"",
		"[Service]",
		"Restart=on-failure")
	return map[string][]byte{ProxyUnitName: unithash.Stamp([]byte(b.String()), unithash.Key("railswitch"))}
}

// RefreshProxyUnits brings the machine-wide units already in quadletDir in
// line with r, for a change of r made without a render (a project
// unregistered after its render failed): a changed unit is replaced, one r
// no longer renders is removed when railswitch wrote it (Marker first). A
// unit not on disk is left absent; the next render writes it.
func RefreshProxyUnits(r *Registry, quadletDir string) error {
	want := ProxyUnits(r)
	for _, name := range []string{ProxyUnitName} {
		path := filepath.Join(quadletDir, name)
		old, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		data, ok := want[name]
		switch {
		case ok && !bytes.Equal(old, data):
			err = tomlfile.WriteAtomic(path, data, 0o644)
		case !ok && bytes.HasPrefix(old, []byte(Marker)):
			err = os.Remove(path)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
