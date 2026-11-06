package lifecycle

import (
	"os"
	"testing"

	"github.com/jbigler/railswitch/internal/workspace"
)

// TestMain keeps a host's dnsmasq out of the tests: a load would run
// `dnsmasq --version` through the fake runner, which counts calls.
func TestMain(m *testing.M) {
	workspace.DNSFilterDirs = nil
	os.Exit(m.Run())
}
