// Package podman wraps the podman and systemctl CLIs. Every object is
// addressed by its exact name: never by --filter, a regex or prune.
package podman

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/sys"
)

// BuildTimeout bounds one image build; PullTimeout one pull.
const (
	BuildTimeout  = 30 * time.Minute
	PullTimeout   = 30 * time.Minute
	ImportTimeout = 10 * time.Minute
)

// Podman runs podman through R.
type Podman struct{ R sys.Runner }

// QuadletPaths are where distributions install podman's quadlet generator:
// Fedora, Debian and Ubuntu under libexec, Arch under lib.
var QuadletPaths = []string{"/usr/libexec/podman/quadlet", "/usr/lib/podman/quadlet"}

// FindQuadlet returns the first of QuadletPaths that exists.
func FindQuadlet() (string, bool) {
	for _, p := range QuadletPaths {
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return "", false
}

// BuildArgs describes one `podman build`.
type BuildArgs struct {
	File    string            // Containerfile path (-f)
	Tag     string            // image tag (-t)
	Context string            // build context dir
	Args    map[string]string // --build-arg K=V, emitted sorted by key
}

func (p Podman) run(ctx context.Context, timeout time.Duration, args ...string) (sys.Result, error) {
	return p.R.Run(ctx, sys.Cmd{Name: "podman", Args: args, Timeout: timeout})
}

func (p Podman) out(ctx context.Context, args ...string) (string, error) {
	r, err := p.run(ctx, 0, args...)
	return strings.TrimSpace(string(r.Stdout)), err
}

// isMissing reports whether a failed call failed because the object is
// absent, judged by the stderr text podman prints for that case.
func isMissing(r sys.Result, err error, needles ...string) bool {
	if !errors.Is(err, sys.ErrExit) {
		return false
	}
	msg := strings.ToLower(string(r.Stderr) + "\n" + err.Error())
	return slices.ContainsFunc(needles, func(n string) bool { return strings.Contains(msg, n) })
}

// exists maps `podman … exists` exit 1 to false.
func (p Podman) exists(ctx context.Context, args ...string) (bool, error) {
	r, err := p.run(ctx, 0, args...)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sys.ErrExit) && r.ExitCode == 1 {
		return false, nil
	}
	return false, err
}

// ImageExists runs `podman image exists <ref>`.
func (p Podman) ImageExists(ctx context.Context, ref string) (bool, error) {
	return p.exists(ctx, "image", "exists", ref)
}

// PullOnly runs `podman pull -q <ref>`.
func (p Podman) PullOnly(ctx context.Context, ref string) error {
	_, err := p.run(ctx, PullTimeout, "pull", "-q", ref)
	return err
}

// Pull runs `podman pull -q <ref>` and returns the pulled image's digest.
func (p Podman) Pull(ctx context.Context, ref string) (string, error) {
	if err := p.PullOnly(ctx, ref); err != nil {
		return "", err
	}
	return p.out(ctx, "image", "inspect", "--format", "{{.Digest}}", ref)
}

// Build runs `podman build --pull=never`; the caller prepares the pinned
// FROM images first (D36).
func (p Podman) Build(ctx context.Context, b BuildArgs) error {
	args := []string{"build", "--pull=never", "-f", b.File, "-t", b.Tag}
	keys := make([]string, 0, len(b.Args))
	for k := range b.Args {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		args = append(args, "--build-arg", k+"="+b.Args[k])
	}
	args = append(args, b.Context)
	_, err := p.run(ctx, BuildTimeout, args...)
	return err
}

// Tag runs `podman tag <src> <dst>`.
func (p Podman) Tag(ctx context.Context, src, dst string) error {
	_, err := p.run(ctx, 0, "tag", src, dst)
	return err
}

// VolumeExists runs `podman volume exists <name>`.
func (p Podman) VolumeExists(ctx context.Context, name string) (bool, error) {
	return p.exists(ctx, "volume", "exists", name)
}

// VolumeCreate runs `podman volume create <name>`.
func (p Podman) VolumeCreate(ctx context.Context, name string) error {
	_, err := p.run(ctx, 0, "volume", "create", name)
	return err
}

// VolumeImport runs `podman volume import <name> <tarball>`.
func (p Podman) VolumeImport(ctx context.Context, name, tarball string) error {
	_, err := p.run(ctx, ImportTimeout, "volume", "import", name, tarball)
	return err
}

// RemoveContainer runs `podman rm -f --ignore <name>`.
func (p Podman) RemoveContainer(ctx context.Context, name string) error {
	_, err := p.run(ctx, 0, "rm", "-f", "--ignore", name)
	return err
}

// RemoveVolume runs `podman volume rm -f <name>`; a missing volume is not an error.
func (p Podman) RemoveVolume(ctx context.Context, name string) error {
	r, err := p.run(ctx, 0, "volume", "rm", "-f", name)
	if err != nil && isMissing(r, err, "no such volume") {
		return nil
	}
	return err
}

// RemoveNetwork runs `podman network rm -f <name>`; a missing network is not an error.
func (p Podman) RemoveNetwork(ctx context.Context, name string) error {
	r, err := p.run(ctx, 0, "network", "rm", "-f", name)
	if err != nil && isMissing(r, err, "network not found") {
		return nil
	}
	return err
}

// NetworkSubnets maps every podman network's name to its subnets, from
// `podman network ls --format json` (a []types.Network: "name",
// "subnets":[{"subnet":"10.88.0.0/16",…}]). Empty output means no networks.
func (p Podman) NetworkSubnets(ctx context.Context) (map[string][]netip.Prefix, error) {
	out, err := p.out(ctx, "network", "ls", "--format", "json")
	if err != nil {
		return nil, err
	}
	nets := map[string][]netip.Prefix{}
	if out == "" {
		return nets, nil
	}
	var ls []struct {
		Name    string `json:"name"`
		Subnets []struct {
			Subnet string `json:"subnet"`
		} `json:"subnets"`
	}
	if err := json.Unmarshal([]byte(out), &ls); err != nil {
		return nil, fmt.Errorf("podman network ls: %w", err)
	}
	for _, n := range ls {
		var subnets []netip.Prefix
		for _, s := range n.Subnets {
			pfx, err := netip.ParsePrefix(s.Subnet)
			if err != nil {
				return nil, fmt.Errorf("podman network %s: %w", n.Name, err)
			}
			subnets = append(subnets, pfx)
		}
		nets[n.Name] = subnets
	}
	return nets, nil
}

// inspectContainer returns the formatted field, or "" for a missing container.
func (p Podman) inspectContainer(ctx context.Context, format, name string) (string, error) {
	r, err := p.run(ctx, 0, "container", "inspect", "--format", format, name)
	if err != nil {
		if isMissing(r, err, "no such container", "no such object") {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(r.Stdout)), nil
}

// Running reports `{{.State.Running}}`; a missing container is not running.
func (p Podman) Running(ctx context.Context, name string) (bool, error) {
	s, err := p.inspectContainer(ctx, "{{.State.Running}}", name)
	return s == "true", err
}

// GraphRoot returns podman's storage graph root.
func (p Podman) GraphRoot(ctx context.Context) (string, error) {
	return p.out(ctx, "info", "--format", "{{.Store.GraphRoot}}")
}

// ImageDigest returns the image's digest, or "" when the image is absent.
func (p Podman) ImageDigest(ctx context.Context, ref string) (string, error) {
	r, err := p.run(ctx, 0, "image", "inspect", "--format", "{{.Digest}}", ref)
	if err != nil {
		if isMissing(r, err, "no such image", "image not known") {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(r.Stdout)), nil
}

// Health returns `{{.State.Health.Status}}`, or "" for a missing container
// or one without a healthcheck.
func (p Podman) Health(ctx context.Context, name string) (string, error) {
	// .State.Health is nil without a healthcheck; guard it so the
	// template prints "" instead of failing.
	return p.inspectContainer(ctx, "{{if .State.Health}}{{.State.Health.Status}}{{end}}", name)
}

// Driver is a podman secret driver with its options (D37). The zero value
// is podman's default driver (file).
type Driver struct {
	Name string
	Opts map[string]string
}

// SecretExists runs `podman secret exists <name>` (exit 1 = false).
func (p Podman) SecretExists(ctx context.Context, name string) (bool, error) {
	return p.exists(ctx, "secret", "exists", name)
}

// SecretCreate runs `podman secret create [--replace] [--driver=<name>
// --driver-opts=<k=v,…>] <name> -`. The value travels on stdin only (D33).
// podman's --replace fails on a missing secret, so the caller passes
// replace = SecretExists(name).
func (p Podman) SecretCreate(ctx context.Context, name string, value io.Reader, replace bool, d Driver) error {
	args := []string{"secret", "create"}
	if replace {
		args = append(args, "--replace")
	}
	if d.Name != "" {
		args = append(args, "--driver="+d.Name)
		if len(d.Opts) > 0 {
			opts := make([]string, 0, len(d.Opts))
			for _, k := range slices.Sorted(maps.Keys(d.Opts)) {
				opts = append(opts, k+"="+d.Opts[k])
			}
			args = append(args, "--driver-opts="+strings.Join(opts, ","))
		}
	}
	args = append(args, name, "-")
	_, err := p.R.Run(ctx, sys.Cmd{Name: "podman", Args: args, Stdin: value})
	return err
}

// SecretDriver returns the driver secret name is stored with.
func (p Podman) SecretDriver(ctx context.Context, name string) (string, error) {
	return p.out(ctx, "secret", "inspect", "--format", "{{.Spec.Driver.Name}}", name)
}

// SecretRemove runs `podman secret rm <name>`.
func (p Podman) SecretRemove(ctx context.Context, name string) error {
	_, err := p.run(ctx, 0, "secret", "rm", name)
	return err
}

// SecretCreated returns when secret name was created
// (`{{.CreatedAt.Unix}}`: CreatedAt is a time.Time).
func (p Podman) SecretCreated(ctx context.Context, name string) (time.Time, error) {
	s, err := p.out(ctx, "secret", "inspect", "--format", "{{.CreatedAt.Unix}}", name)
	if err != nil {
		return time.Time{}, err
	}
	sec, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("secret %s: created at %q: %w", name, s, err)
	}
	return time.Unix(sec, 0), nil
}

// Label returns the running container's label key, or "" for a missing
// container or label.
func (p Podman) Label(ctx context.Context, name, key string) (string, error) {
	return p.inspectContainer(ctx, `{{index .Config.Labels "`+key+`"}}`, name)
}
