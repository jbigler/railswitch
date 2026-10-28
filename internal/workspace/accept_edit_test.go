package workspace

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/lockstore"
	"github.com/jbigler/railswitch/internal/pack"
)

// Removed-remembered ref, re-added by hand: Accepted (ejects) returns the
// raw (unreviewed) content although the next lock pins the remembered copy.
func TestAcceptedRunsRemovedCopy(t *testing.T) {
	ctx := context.Background()
	root := projectRoot(t)
	if err := Lock(ctx, root, Accept{All: true}); err != nil {
		t.Fatal(err)
	}
	acceptVariant(t, root, pack.KindOption, "redis", func(m fstest.MapFS) { m["README.md"] = &fstest.MapFile{Data: []byte("accepted\n")} })
	proj := filepath.Join(root, "project.toml")
	replaceIn(t, proj, `cache    = "redis@1"`, `cache    = "none"`)
	replaceIn(t, proj, "redis    = \"8\"\n", "")
	if err := Lock(ctx, root, Accept{}); err != nil {
		t.Fatal(err)
	}
	replaceIn(t, proj, `cache    = "none"`, `cache    = "redis@1"`)
	replaceIn(t, proj, "postgres = \"16\"", "postgres = \"16\"\nredis    = \"8\"")
	p, err := config.Load(proj)
	if err != nil {
		t.Fatal(err)
	}
	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	acc, err := Accepted(root, p, cat)
	if err != nil {
		t.Fatal(err)
	}
	o, err := acc.Option(redis)
	if err != nil {
		t.Fatal(err)
	}
	b, err := fs.ReadFile(o.Files, "README.md")
	if err != nil || string(b) != "accepted\n" {
		t.Errorf("Accepted's redis README = %q, %v; want the accepted copy", b, err)
	}
	if err := Lock(ctx, root, Accept{}); err != nil {
		t.Fatal(err)
	}
	w, err := Load(ctx, root, "")
	if err != nil {
		t.Fatal(err)
	}
	b2, _ := fs.ReadFile(w.Stack.Option("cache").Option.Files, "README.md")
	if string(b) != string(b2) {
		t.Errorf("eject catalog (%q) differs from what the lock runs (%q)", b, b2)
	}
}

// A local pack and a local option sharing a ref: a pack file write's
// Edit (matched by ref only) accepts the option's unreviewed edit of the
// same path.
func TestLockEditMatchesKind(t *testing.T) {
	ctx := context.Background()
	root := projectRoot(t)
	cat, err := pack.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(root, "packs", "redis"), cat.Packs[pack.Ref{Name: "rails", Version: 1}].Files); err != nil {
		t.Fatal(err)
	}
	replaceIn(t, filepath.Join(root, "packs", "redis", "pack.toml"), `name = "rails"`, `name = "redis"`)
	if err := os.CopyFS(filepath.Join(root, "options", "redis"), cat.Options[redis].Files); err != nil {
		t.Fatal(err)
	}
	setPack(t, root, "local:redis@1")
	proj := filepath.Join(root, "project.toml")
	replaceIn(t, proj, `cache    = "redis@1"`, `cache    = "local:redis@1"`)
	if err := Lock(ctx, root, Accept{All: true}); err != nil {
		t.Fatal(err)
	}
	optSHA := lockOf(t, root).Options["redis"].SHA256
	// unreviewed edit of the option (text editor / agent)
	if err := os.WriteFile(filepath.Join(root, "options", "redis", "README.md"), []byte("unreviewed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// a pack file write of the pack's README.md
	if err := os.WriteFile(filepath.Join(root, "packs", "redis", "README.md"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ref := pack.Ref{Name: "redis", Version: 1, Local: true}
	if err := Lock(ctx, root, Accept{Edit: &FileEdit{Kind: pack.KindPack, Ref: ref, Path: "README.md"}}); err != nil {
		t.Fatalf("lock: %v", err)
	}
	if got := lockOf(t, root).Options["redis"].SHA256; got != optSHA {
		t.Errorf("the option's unreviewed README was accepted: %s -> %s", optSHA, got)
	}
}

// onlyEdited judges only bytes that hash to what the lock is about to pin:
// a file changed after the lock hashed the package is refused.
func TestOnlyEditedChangedMeanwhile(t *testing.T) {
	s := lockstore.Store{Dir: t.TempDir()}
	acc := fstest.MapFS{"pack.toml": {Data: []byte("a\n")}, "README.md": {Data: []byte("r\n")}}
	sha, err := s.Put(acc)
	if err != nil {
		t.Fatal(err)
	}
	edited := fstest.MapFS{"pack.toml": {Data: []byte("a\n")}, "README.md": {Data: []byte("mine\n")}}
	next, err := pack.ContentHash(edited)
	if err != nil {
		t.Fatal(err)
	}
	edit := FileEdit{Kind: pack.KindPack, Path: "README.md"}
	if err := onlyEdited(s, sha, next, edited, edit); err != nil {
		t.Fatalf("only the edited file changed: %v", err)
	}
	if err := onlyEdited(s, sha, "0000", edited, edit); err == nil {
		t.Error("bytes that don't hash to the locked entry were judged")
	}
}
