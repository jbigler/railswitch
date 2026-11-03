package podman

import (
	"testing"

	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
)

// statsFixture is podman 5.x's `stats --no-stream --format json`: strings
// throughout, "--" for an unknown figure.
const statsFixture = `[
 {"id": "1f2e", "name": "filial-feat-rails", "cpu_time": "2.5s", "cpu_percent": "12.34%", "avg_cpu": "3.10%",
  "mem_usage": "1.611GB / 2.147GB", "mem_percent": "75.03%", "net_io": "1kB / 2kB", "block_io": "0B / 0B", "pids": "42"},
 {"id": "2a3b", "name": "filial-feat-db", "cpu_percent": "--", "mem_usage": "512KiB / 1GiB", "mem_percent": "--", "pids": "--"},
 {"Name": "filial-feat-cache", "CPU": 1.5, "cpu_percent": 0.5, "mem_usage": "garbage", "pids": 3},
 {"id": "noname"}
]`

func TestParseStats(t *testing.T) {
	got, err := parseStats([]byte(statsFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %v", got)
	}
	if s := got["filial-feat-rails"]; s != (Stat{CPUPercent: 12.34, MemUsed: 1_611_000_000, MemLimit: 2_147_000_000, MemPercent: 75.03, PIDs: 42}) {
		t.Errorf("rails %+v", s)
	}
	// no percent printed: computed from the sizes
	if s := got["filial-feat-db"]; s.MemUsed != 512<<10 || s.MemLimit != 1<<30 || s.PIDs != 0 || s.CPUPercent != 0 || s.MemPercent < 0.048 || s.MemPercent > 0.049 {
		t.Errorf("db %+v", s)
	}
	if s := got["filial-feat-cache"]; s != (Stat{CPUPercent: 0.5, PIDs: 3}) {
		t.Errorf("cache %+v", s)
	}
	if _, err := parseStats([]byte("Error: nope")); err == nil {
		t.Error("non-JSON accepted")
	}
}

func TestBytesOf(t *testing.T) {
	for in, want := range map[string]int64{"0B": 0, "12.5MB": 12_500_000, " 1.074GB ": 1_074_000_000, "3kB": 3000, "2 MiB": 2 << 20, "7": 7, "--": 0, "1XB": 0, "": 0} {
		if got := bytesOf(in); got != want {
			t.Errorf("bytesOf(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestStatsArgv(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"podman", "stats"}, sys.Result{Stdout: []byte(statsFixture)}, nil)
	p := Podman{R: f}
	if got, err := p.Stats(ctx, nil); err != nil || len(got) != 0 || len(f.Calls) != 0 {
		t.Fatalf("no names: %v %v, calls %q", got, err, f.Calls)
	}
	if _, err := p.Stats(ctx, []string{"filial-feat-rails", "filial-feat-db"}); err != nil {
		t.Fatal(err)
	}
	wantCalls(t, f, []string{"podman", "stats", "--no-stream", "--no-reset", "--format", "json", "filial-feat-rails", "filial-feat-db"})
	if f.Cmds[0].Timeout != 0 {
		t.Errorf("timeout %v: the default 30 s", f.Cmds[0].Timeout)
	}
}
