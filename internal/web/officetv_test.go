package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// D168: the office page may load media from, and fetch from, the Jellyfin
// source's origin only; /office/tv.json hands the page the key from its
// file.
func TestOfficeTV(t *testing.T) {
	f := newFixture(t)
	f.s.c.Data = t.TempDir()
	f.cli.set("--json office tv", `{"jellyfin":{"url":"https://media.lan/jf","playlist":"Lounge","user":"sam","key_set":true}}`)
	if err := os.MkdirAll(filepath.Join(f.s.c.Data, "dashboard"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.s.c.Data, "dashboard", "jellyfin-key"), []byte("k3y\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := f.do(req("GET", "/p/ftest/virtual-office/cyberpunk/", nil))
	if got := w.Header().Get("Content-Security-Policy"); got != wantCSP+"; img-src 'self' data:; media-src https://media.lan; connect-src 'self' https://media.lan" {
		t.Errorf("CSP %q", got)
	}
	w = f.do(req("GET", "/office/tv.json", nil))
	var doc struct{ Jellyfin tvJellyfin }
	if err := json.Unmarshal(w.Body.Bytes(), &doc); w.Code != 200 || err != nil || doc.Jellyfin != (tvJellyfin{URL: "https://media.lan/jf", Playlist: "Lounge", User: "sam", Key: "k3y"}) {
		t.Errorf("tv.json %d %s", w.Code, w.Body)
	}
	f.cli.set("--json office tv", `{"jellyfin":{"url":"https://a.lan; script-src *","playlist":"x","user":"","key_set":false}}`)
	if got := f.do(req("GET", "/p/ftest/virtual-office/cyberpunk/", nil)).Header().Get("Content-Security-Policy"); strings.Contains(got, "media-src") {
		t.Errorf("a bad url reached the CSP: %q", got)
	}
}
