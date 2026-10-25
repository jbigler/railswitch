package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func sampleLock() *Lock {
	l := &Lock{
		Schema:  SchemaVersion,
		Packs:   map[string]LockEntry{"rails": {Version: "1", Source: "builtin", Commit: "abc123", SHA256: "deadbeef"}},
		Options: map[string]LockEntry{"postgres": {Version: "1", Source: "builtin", SHA256: "01"}, "redis": {Version: "1", Source: "builtin", SHA256: "02"}},
	}
	l.SetDigest("docker.io/library/redis:7", "sha256:r")
	l.SetDigest("docker.io/library/postgres:16", "sha256:p")
	l.SetBaseDigest("docker.io/library/ruby:3.3.6", "sha256:b")
	return l
}

func TestLockRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stack.lock")
	want := sampleLock()
	if err := want.Write(path); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ReadLock = %+v; want %+v", got, want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "# Written by railswitch. Do not edit; review updates in the dashboard.\n") {
		t.Errorf("missing header:\n%s", data)
	}
}

func TestLockWriteDeterministic(t *testing.T) {
	dir := t.TempDir()
	l := sampleLock()
	a, b := filepath.Join(dir, "a.lock"), filepath.Join(dir, "b.lock")
	if err := l.Write(a); err != nil {
		t.Fatal(err)
	}
	if err := l.Write(b); err != nil {
		t.Fatal(err)
	}
	da, _ := os.ReadFile(a)
	db, _ := os.ReadFile(b)
	if !bytes.Equal(da, db) {
		t.Errorf("two writes differ:\n%s\n---\n%s", da, db)
	}
}

func TestLockDigests(t *testing.T) {
	l := &Lock{Schema: LockSchemaVersion}
	if d := l.Digest("docker.io/library/postgres:16"); d != "" {
		t.Errorf("Digest(unknown) = %q", d)
	}
	const ref = "docker.io/library/postgres:16"
	l.SetDigest(ref, "sha256:aa")
	path := filepath.Join(t.TempDir(), "stack.lock")
	if err := l.Write(path); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if d := got.Digest(ref); d != "sha256:aa" {
		t.Errorf("Digest after round trip = %q", d)
	}
	got.SetDigest(ref, "sha256:bb")
	if d := got.Digest(ref); d != "sha256:bb" || len(got.Images) != 1 {
		t.Errorf("after replace: Digest = %q, Images = %+v", d, got.Images)
	}
	if d := got.BaseDigest(ref); d != "" {
		t.Errorf("BaseDigest shares entries with Digest: %q", d)
	}
}

func TestLockDigestsSorted(t *testing.T) {
	l := &Lock{}
	for _, r := range []string{"c", "a", "b"} {
		l.SetDigest(r, "d"+r)
		l.SetBaseDigest(r, "b"+r)
	}
	want := []LockImage{{"a", "da"}, {"b", "db"}, {"c", "dc"}}
	if !reflect.DeepEqual(l.Images, want) {
		t.Errorf("Images = %+v", l.Images)
	}
	if l.BaseImages[0].Ref != "a" || l.BaseDigest("c") != "bc" {
		t.Errorf("BaseImages = %+v", l.BaseImages)
	}
}

func TestReadLockMissing(t *testing.T) {
	if _, err := ReadLock(filepath.Join(t.TempDir(), "stack.lock")); !errors.Is(err, ErrNoLock) {
		t.Errorf("ReadLock(missing) = %v; want ErrNoLock", err)
	}
}

func TestReadLockNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stack.lock")
	if err := os.WriteFile(path, []byte("schema = 2\npack = \"changed type\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLock(path); err == nil || !strings.Contains(err.Error(), "newer railswitch") {
		t.Errorf("ReadLock = %v; want newer railswitch", err)
	}
}

func TestReadLockMissingSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stack.lock")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLock(path); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Errorf("ReadLock(empty file) = %v; want a schema error", err)
	}
}

func TestReadLockUnknownKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stack.lock")
	if err := os.WriteFile(path, []byte("schema = 1\n[[imgae]]\nref = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLock(path); err == nil || !strings.Contains(err.Error(), "imgae") {
		t.Errorf("ReadLock = %v; want an error naming imgae", err)
	}
}
