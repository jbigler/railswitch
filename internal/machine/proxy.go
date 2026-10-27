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

// The machine-wide units' file names, and the machine network's name.
const (
	ProxyUnitName          = "railswitch-traefik.container"
	MachineNetworkUnitName = "railswitch-machine.network"
	DozzleUnitName         = "railswitch-dozzle.container"
	MachineNetwork         = "railswitch_machine"

	// ProxyContainer and DozzleContainer are Traefik's and Dozzle's
	// containers; ProxyService and DozzleService are the services Quadlet
	// generates from the machine's .container units.
	ProxyContainer  = "railswitch-traefik"
	DozzleContainer = "railswitch-dozzle"
	ProxyService    = "railswitch-traefik.service"
	DozzleService   = "railswitch-dozzle.service"
)

// machineUnitNames are every unit ProxyUnits renders.
var machineUnitNames = []string{ProxyUnitName, MachineNetworkUnitName, DozzleUnitName}

// traefikFlags are Traefik's static configuration (fd7336b) minus
// --providers.docker.network: each routed container carries its own
// traefik.docker.network label. The API is served only through the
// railswitch-api router (traefik.localhost), never --api.insecure.
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
	"--api=true",
	"--log.level=INFO",
	"--accesslog=false",
}

// machineMarker is line 1 of every machine-wide unit.
const machineMarker = Marker + "railswitch (machine). Edit the railswitch registry, not this file."

// hostRule is the rule of a machine router serving host h to the host only
// (D50), double-quoted for Label=: it holds spaces, no quote or backslash.
func hostRule(router, h string) string {
	return `"traefik.http.routers.` + router + ".rule=Host(`" + h + "`) && (" + HostClientClause() + `)"`
}

// isNetworkLine reports a Network= line: Traefik's unit-hash ignores them,
// so adding or removing a project doesn't restart it (it joins live).
func isNetworkLine(l string) bool { return strings.HasPrefix(l, "Network=") }

// ProxyUnits renders the machine-wide units, keyed by filename: the shared
// Traefik container, joined to every registered project's proxy network at
// its fixed address (D21) and to the machine network; the machine network;
// and Dozzle on it. With no project registered it renders nothing.
// Prefixes and subnets come from the registry and hold no % or $.
func ProxyUnits(r *Registry) map[string][]byte {
	if len(r.Projects) == 0 {
		return map[string][]byte{}
	}
	ps := slices.Clone(r.Projects)
	slices.SortFunc(ps, func(a, b ProjectEntry) int { return cmp.Compare(a.Subnet, b.Subnet) })
	lines := func(s ...string) []byte { return []byte(strings.Join(s, "\n") + "\n") }

	// The networks' Requires=/After= come from Quadlet itself (D27).
	traefik := []string{machineMarker,
		"",
		"[Unit]",
		"Description=railswitch Traefik",
		"",
		"[Container]",
		"Image=docker.io/traefik:v3.7",
		"ContainerName=" + ProxyContainer,
		"Network=" + MachineNetworkUnitName + ":ip=" + TraefikIP(MachineSubnet)}
	for _, p := range ps {
		traefik = append(traefik, "Network="+naming.ProxyNetworkUnit(p.Prefix)+":ip="+TraefikIP(p.Subnet))
	}
	traefik = append(traefik,
		"PublishPort=127.0.0.1:80:80",
		"Volume=%t/podman/podman.sock:/var/run/docker.sock:ro",
		"LogDriver=journald",
		"Label=traefik.enable=true",
		"Label="+hostRule("railswitch-api", "traefik.localhost"),
		"Label=traefik.http.routers.railswitch-api.service=api@internal",
		"Exec="+strings.Join(traefikFlags, " \\\n  "),
		"NoNewPrivileges=true",
		"Memory=512m",
		"PidsLimit=1024",
		"",
		"[Service]",
		"Restart=on-failure")

	network := lines(machineMarker,
		"",
		"[Unit]",
		"Description=railswitch machine network",
		"",
		"[Network]",
		"NetworkName="+MachineNetwork,
		"Subnet="+Subnet(MachineSubnet),
		"Gateway="+Gateway(MachineSubnet),
		"IPRange="+IPRange(MachineSubnet),
		"Options=mtu=1400",
		"Options=isolate=strict")

	dozzle := lines(machineMarker,
		"",
		"[Unit]",
		"Description=railswitch Dozzle",
		"",
		"[Container]",
		"Image=docker.io/amir20/dozzle:v11.3.5",
		"ContainerName="+DozzleContainer,
		"Network="+MachineNetworkUnitName+":ip="+addr(MachineSubnet, ".3"),
		"Volume=%t/podman/podman.sock:/var/run/docker.sock:ro",
		// The socket's :ro doesn't stop API writes over it: Dozzle's own
		// settings are the guard, pinned rather than left to its defaults.
		"Environment=DOZZLE_ENABLE_ACTIONS=false",
		"Environment=DOZZLE_ENABLE_SHELL=false",
		"LogDriver=journald",
		"Label=traefik.enable=true",
		"Label=traefik.docker.network="+MachineNetwork,
		"Label="+hostRule("railswitch-logs", "logs.localhost"),
		"Label=traefik.http.routers.railswitch-logs.entrypoints=web",
		"Label=traefik.http.routers.railswitch-logs.service=railswitch-logs",
		"Label=traefik.http.services.railswitch-logs.loadbalancer.server.port=8080",
		"DropCapability=all",
		"NoNewPrivileges=true",
		"Memory=256m",
		"PidsLimit=256",
		"",
		"[Service]",
		"Restart=on-failure")

	key := unithash.Key("railswitch")
	return map[string][]byte{
		ProxyUnitName:          unithash.StampExcept(lines(traefik...), key, isNetworkLine),
		MachineNetworkUnitName: network,
		DozzleUnitName:         unithash.Stamp(dozzle, key),
	}
}

// RefreshProxyUnits brings the machine-wide units already in quadletDir in
// line with r, for a change of r made without a render (a project
// unregistered after its render failed): a changed unit is replaced, one r
// no longer renders is removed when railswitch wrote it (Marker first). A
// unit not on disk is left absent; the next render writes it.
func RefreshProxyUnits(r *Registry, quadletDir string) error {
	want := ProxyUnits(r)
	for _, name := range machineUnitNames {
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
