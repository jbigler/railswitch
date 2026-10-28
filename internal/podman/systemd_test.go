package podman

import (
	"reflect"
	"testing"
	"time"

	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/sys/systest"
)

func TestShowManyParses(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"systemctl", "--user", "show"}, sys.Result{Stdout: []byte(
		"Id=a.service\nActiveState=active\nSubState=running\n\nActiveState=failed\nSubState=failed\nId=b.service\n")}, nil)
	got, err := Systemd{R: f}.ShowMany(ctx, []string{"a.service", "b.service"}, "ActiveState", "SubState")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]string{
		"a.service": {"Id": "a.service", "ActiveState": "active", "SubState": "running"},
		"b.service": {"Id": "b.service", "ActiveState": "failed", "SubState": "failed"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ShowMany = %v; want %v", got, want)
	}
	wantCalls(t, f, []string{"systemctl", "--user", "show", "-p", "Id,ActiveState,SubState", "a.service", "b.service"})
}

func TestShowManyNoUnits(t *testing.T) {
	f := &systest.Fake{}
	got, err := Systemd{R: f}.ShowMany(ctx, nil, "ActiveState")
	if err != nil || len(got) != 0 || len(f.Calls) != 0 {
		t.Errorf("ShowMany(nil) = %v, %v with calls %q; want empty, no call", got, err, f.Calls)
	}
}

func TestLabels(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"podman", "ps"}, sys.Result{Stdout: []byte(`[
{"Names":["p-a-rails"],"State":"running","Labels":{"p.unit-hash":"h1"}},
{"Names":["p-a-db"],"State":"running","Labels":{}},
{"Names":["p-a-worker"],"State":"exited","Labels":{"p.unit-hash":"h2"}},
{"Names":["p-b-rails"],"State":"running","Labels":null}]`)}, nil)
	got, err := Podman{R: f}.Labels(ctx, "p.unit-hash")
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"p-a-rails": "h1", "p-a-db": "", "p-b-rails": ""}; !reflect.DeepEqual(got, want) {
		t.Errorf("Labels = %v; want %v", got, want)
	}
	wantCalls(t, f, []string{"podman", "ps", "-a", "--format", "json"})
}

func TestLabelsBadJSON(t *testing.T) {
	f := &systest.Fake{}
	f.On([]string{"podman", "ps"}, sys.Result{Stdout: []byte("not json")}, nil)
	if _, err := (Podman{R: f}).Labels(ctx, "k"); err == nil {
		t.Error("Labels accepted non-JSON output")
	}
}

// TestActiveSince: the exact (us, UTC) ActiveEnterTimestamp; never active
// is the zero time; an unparsable value is an error.
func TestActiveSince(t *testing.T) {
	for _, c := range []struct {
		out  string
		want time.Time
		err  bool
	}{
		{"ActiveEnterTimestamp=Tue 2026-10-27 09:00:00.123456 UTC\n", time.Date(2026, 10, 27, 9, 0, 0, 123456000, time.UTC), false},
		{"ActiveEnterTimestamp=\n", time.Time{}, false},
		{"ActiveEnterTimestamp=Tue 2026-10-27 10:00:00 CET\n", time.Time{}, true},
	} {
		f := &systest.Fake{}
		f.On([]string{"systemctl"}, sys.Result{Stdout: []byte(c.out)}, nil)
		got, err := Systemd{R: f}.ActiveSince(ctx, "x.service")
		if (err != nil) != c.err || !got.Equal(c.want) {
			t.Errorf("%q: ActiveSince = %v, %v; want %v (error %v)", c.out, got, err, c.want, c.err)
		}
		wantCalls(t, f, []string{"systemctl", "--user", "show", "--timestamp=us+utc", "-p", "ActiveEnterTimestamp", "x.service"})
	}
}
