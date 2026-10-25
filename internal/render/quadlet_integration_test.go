//go:build integration

package render

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const defaultQuadlet = "/usr/libexec/podman/quadlet"

// TestQuadletDryRun feeds the rendered Filial units to the real Quadlet
// generator (`quadlet -dryrun -user`) and checks the generated services.
// RAILSWITCH_QUADLET names the generator; when it is set (as in CI) a missing
// binary fails the test, when unset a missing default binary skips it.
func TestQuadletDryRun(t *testing.T) {
	bin := os.Getenv("RAILSWITCH_QUADLET")
	if bin == "" {
		bin = defaultQuadlet
		if _, err := os.Stat(bin); err != nil {
			t.Skipf("quadlet generator %s: %v (set RAILSWITCH_QUADLET to require it)", bin, err)
		}
	} else if _, err := os.Stat(bin); err != nil {
		t.Fatalf("RAILSWITCH_QUADLET=%s: %v", bin, err)
	}
	_, units := renderUnits(t, fixtureDir, nil)
	dir := t.TempDir()
	for name, data := range units {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-dryrun", "-user")
	cmd.Env = append(os.Environ(), "QUADLET_UNIT_DIRS="+dir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s -dryrun -user: %v\nstderr:\n%s", bin, err, stderr.String())
	}
	services := splitDryRun(stdout.String())

	rails := execStart(t, services, "filial-feat-rails.service")
	// Quadlet writes the health command quoted with spaces as \x20.
	for _, want := range []*regexp.Regexp{
		regexp.MustCompile(`--hooks-dir=\S+`),
		regexp.MustCompile(`--health-cmd "?curl(\\x20| )-fsS(\\x20| )http://localhost:3000/up"?(\s|$)`),
		regexp.MustCompile(`--sdnotify=healthy(\s|$)`),
	} {
		if !want.MatchString(rails) {
			t.Errorf("filial-feat-rails.service ExecStart does not match %s:\n%s", want, rails)
		}
	}
	requires := regexp.MustCompile(`(?m)^Requires=.*\bfilial-feat-dev-network\.service\b`)
	if !requires.MatchString(services["filial-feat-rails.service"]) {
		t.Errorf("filial-feat-rails.service has no Requires= naming filial-feat-dev-network.service:\n%s", services["filial-feat-rails.service"])
	}

	if deps := execStart(t, services, "filial-feat-deps.service"); strings.Contains(deps, "--health-cmd") {
		t.Errorf("filial-feat-deps.service ExecStart has --health-cmd:\n%s", deps)
	}
}

// execStart is the generated ExecStart= line of unit name; the dry-run output
// also echoes the source keys in [X-Container], which must not satisfy checks.
func execStart(t *testing.T, services map[string]string, name string) string {
	t.Helper()
	svc, ok := services[name]
	if !ok {
		t.Fatalf("no %s in dry-run output; got %v", name, keys(services))
	}
	line := regexp.MustCompile(`(?m)^ExecStart=.*$`).FindString(svc)
	if line == "" {
		t.Fatalf("%s has no ExecStart=:\n%s", name, svc)
	}
	return line
}

// splitDryRun splits `quadlet -dryrun` output, a "---name.service---" header
// before each generated unit, into unit name → contents.
func splitDryRun(out string) map[string]string {
	header := regexp.MustCompile(`(?m)^---(.+)---$`)
	services := map[string]string{}
	locs := header.FindAllStringSubmatchIndex(out, -1)
	for i, loc := range locs {
		end := len(out)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		services[out[loc[2]:loc[3]]] = out[loc[1]:end]
	}
	return services
}

func keys(m map[string]string) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
