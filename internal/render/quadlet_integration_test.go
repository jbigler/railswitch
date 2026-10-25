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

// quadletBin is podman's Quadlet generator, overridable by RAILSWITCH_QUADLET.
func quadletBin() string {
	if p := os.Getenv("RAILSWITCH_QUADLET"); p != "" {
		return p
	}
	return "/usr/libexec/podman/quadlet"
}

// TestQuadletDryRun feeds the rendered Filial units to the real Quadlet
// generator (`quadlet -dryrun -user`) and checks the generated services.
func TestQuadletDryRun(t *testing.T) {
	bin := quadletBin()
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("quadlet generator %s: %v", bin, err)
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

	rails, ok := services["filial-feat-rails.service"]
	if !ok {
		t.Fatalf("no filial-feat-rails.service in dry-run output; got %v\nstderr:\n%s", keys(services), stderr.String())
	}
	for _, want := range []string{
		"--hooks-dir=",
		"--health-cmd",
		"curl -fsS http://localhost:3000/up",
		"--sdnotify=healthy",
	} {
		if !strings.Contains(rails, want) {
			t.Errorf("filial-feat-rails.service lacks %q:\n%s", want, rails)
		}
	}
	requires := regexp.MustCompile(`(?m)^Requires=.*\bfilial-feat-dev-network\.service\b`)
	if !requires.MatchString(rails) {
		t.Errorf("filial-feat-rails.service has no Requires= naming filial-feat-dev-network.service:\n%s", rails)
	}

	deps, ok := services["filial-feat-deps.service"]
	if !ok {
		t.Fatalf("no filial-feat-deps.service in dry-run output; got %v", keys(services))
	}
	if strings.Contains(deps, "--health-cmd") {
		t.Errorf("filial-feat-deps.service has --health-cmd:\n%s", deps)
	}
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
