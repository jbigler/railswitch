package unithash

import (
	"strings"
	"testing"
)

const unit = "# marker\n\n[Unit]\nDescription=x\n\n[Container]\nImage=a\n\n[Service]\nRestart=always\n"

func TestStamp(t *testing.T) {
	got := string(Stamp([]byte(unit), "api.unit-hash"))
	h := Sum([]byte(unit))
	if len(h) != 12 || strings.Trim(h, "0123456789abcdef") != "" {
		t.Fatalf("Sum = %q", h)
	}
	want := strings.Replace(unit, "[Container]\n", "[Container]\nLabel=api.unit-hash="+h+"\n", 1)
	if got != want {
		t.Fatalf("Stamp =\n%s\nwant\n%s", got, want)
	}
	if v := Read([]byte(got), "api.unit-hash"); v != h {
		t.Errorf("Read = %q; want %q", v, h)
	}
	if v := Read([]byte(got), "api2.unit-hash"); v != "" {
		t.Errorf("Read of another key = %q", v)
	}
	// The hash is of the unit with its label line excluded.
	if Sum([]byte(strings.Replace(got, "Label=api.unit-hash="+h+"\n", "", 1))) != h {
		t.Error("hash does not exclude the label line")
	}
	if Sum([]byte(unit+"#\n")) == h {
		t.Error("a changed unit has the same hash")
	}
}
