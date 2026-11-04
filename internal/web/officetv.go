package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/jbigler/railswitch/internal/dashconf"
	"github.com/jbigler/railswitch/internal/machine"
	"github.com/jbigler/railswitch/internal/safefile"
)

// tvLs is office tv --json: the lounge screen's sources (D168).
type tvLs struct {
	Jellyfin *struct {
		URL      string `json:"url"`
		Playlist string `json:"playlist"`
		User     string `json:"user"`
		KeySet   bool   `json:"key_set"`
	} `json:"jellyfin"`
}

// tvJellyfin is a Jellyfin source as the office page gets it: with its
// API key, which the page needs for Jellyfin's API and its stream URLs.
type tvJellyfin struct {
	URL      string `json:"url"`
	Playlist string `json:"playlist"`
	User     string `json:"user"`
	Key      string `json:"key"`
}

// tvOrigins are the origins the sources play from, for the office page's
// Content-Security-Policy; none when office tv can't say.
func (s *Server) tvOrigins(ctx context.Context) []string {
	ls, err := readJSON[tvLs](ctx, s.c.CLI, "office", "tv")
	if err != nil || ls.Jellyfin == nil {
		return nil
	}
	o, err := dashconf.TVOrigin(ls.Jellyfin.URL)
	if err != nil {
		return nil
	}
	return []string{o}
}

// officeTVJSON answers GET /office/tv.json: {jellyfin: {url, playlist,
// user, key} | null}. The key is read from its file here, not through
// railswitch --json, so no CLI output ever holds it (D168, like the
// dashboard's own token).
func (s *Server) officeTVJSON(w http.ResponseWriter, r *http.Request) {
	ls, err := readJSON[tvLs](r.Context(), s.c.CLI, "office", "tv")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	var doc struct {
		Jellyfin *tvJellyfin `json:"jellyfin"`
	}
	if j := ls.Jellyfin; j != nil {
		doc.Jellyfin = &tvJellyfin{URL: j.URL, Playlist: j.Playlist, User: j.User}
		if s.c.Data != "" {
			if key, err := safefile.Read(dashconf.JellyfinKeyPath(machine.Dirs{Data: s.c.Data}), 4096); err == nil {
				doc.Jellyfin.Key = string(bytes.TrimSpace(key))
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(doc)
}
