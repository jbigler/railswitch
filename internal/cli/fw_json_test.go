package cli

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
)

// fw show --json is firewall.sh's tab-separated lines as one document.
func TestFwShowJSON(t *testing.T) {
	ws, _ := lockedWS(t)
	mustRun(t, "-C", ws, "render")
	script := filepath.Join(ws, ".railswitch", "firewall", "firewall.sh")
	f := unitsFake()
	f.On([]string{script, "show"}, sys.Result{Stdout: []byte("C\tfilial-master-rails\tfirewalled\tapp\tlog\nR\tfilial-master-rails\t104.16.0.1\ttcp\t443\tapi.example.com\nC\tfilial-master-browser\tself\t\t\nC\tfilial-master-db\tfirewalled\tnone\tenforce\n")}, nil)
	code, out, errs := runWith(t, f, "-C", ws, "--json", "fw", "show")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var doc fwDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Containers) != 3 {
		t.Fatalf("%+v", doc)
	}
	r := doc.Containers[0]
	if r.Name != "filial-master-rails" || r.Mode != "log" || len(r.Profiles) != 1 || r.Profiles[0] != "app" || len(r.Rejected) != 1 || r.Rejected[0] != (fwRejected{Addr: "104.16.0.1", Proto: "tcp", Port: "443", Host: "api.example.com"}) {
		t.Errorf("rails = %+v", r)
	}
	if b := doc.Containers[1]; b.Firewall != "self" || b.Profiles == nil || b.Rejected == nil {
		t.Errorf("browser = %+v", b)
	}
	var env []string
	for _, c := range f.Cmds {
		if c.Name == script {
			env = c.Env
		}
	}
	found := false
	for _, kv := range env {
		found = found || kv == "FW_FORMAT=tsv"
	}
	if !found {
		t.Error("firewall.sh ran without FW_FORMAT=tsv")
	}
}
