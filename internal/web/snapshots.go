package web

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/jbigler/railswitch/internal/jobs"
)

// snapshot is a row of `db snapshot ls` (D157).
type snapshot struct {
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
	Bytes   int64     `json:"bytes"`
}

func readSnapshots(ctx context.Context, c CLI, ws, slug string) ([]snapshot, error) {
	return readJSON[[]snapshot](ctx, c, "-C", ws, "-w", slug, "db", "snapshot", "ls")
}

// dumps is `db dump ls`: the starting data in db-dumps/ (D208).
type dumps struct {
	Modified *time.Time `json:"modified"`
	Bytes    int64      `json:"bytes"`
	Files    []string   `json:"files"`
}

type snapshotsView struct {
	Slug, TakeURL, RestoreURL, RmURL string
	Snapshots                        []snapshotRow
	// The Database tab's panel only: the starting data, its POST, and
	// Written "" when db-dumps/ holds none.
	DumpURL, Written, DumpSize string
	DumpFiles                  []string
	// Stopped: the worktree's stack is stopped, so take, restore and Use,
	// which need its database running, are disabled (#255).
	Stopped bool
}

type snapshotRow struct{ Name, Created, Size string }

func megabytes(n int64) string { return fmt.Sprintf("%.1f MB", float64(n)/1e6) }

// snapshotsOf is worktree slug's snapshots dialog, or with panel the
// Database tab's content, which shows the starting data too.
func (s *Server) snapshotsOf(r *http.Request, p projectRow, slug string, panel bool) (snapshotsView, error) {
	snaps, err := readSnapshots(r.Context(), s.c.CLI, p.Workspace, slug)
	if err != nil {
		return snapshotsView{}, err
	}
	base := worktreePath(p, slug) + "/snapshots/"
	v := snapshotsView{Slug: slug, TakeURL: base + "take", RestoreURL: base + "restore", RmURL: base + "rm", Stopped: s.stopped(p.Prefix, slug)}
	for _, sn := range snaps {
		v.Snapshots = append(v.Snapshots, snapshotRow{Name: sn.Name, Created: sn.Created.Local().Format("2006-01-02 15:04"), Size: megabytes(sn.Bytes)})
	}
	if panel {
		d, err := readJSON[dumps](r.Context(), s.c.CLI, "-C", p.Workspace, "db", "dump", "ls")
		if err != nil {
			return snapshotsView{}, err
		}
		v.DumpURL, v.DumpFiles, v.DumpSize = base+"dump", d.Files, megabytes(d.Bytes)
		if d.Modified != nil {
			v.Written = d.Modified.Local().Format("2006-01-02 15:04")
		}
	}
	return v, nil
}

// stopped reports whether worktree slug's stack is stopped, as the last
// overview read it; false before one.
func (s *Server) stopped(prefix, slug string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.state[prefix]
	if p == nil || p.ov == nil {
		return false
	}
	for _, wt := range p.ov.Worktrees {
		if wt.Slug == slug {
			return wt.Stack == "stopped"
		}
	}
	return false
}

// snapshotsDialog lists the worktree's database snapshots, each with
// restore and delete, and takes a new one.
func (s *Server) snapshotsDialog(w http.ResponseWriter, r *http.Request) {
	p, wt, ok := s.lookup(w, r)
	if !ok {
		return
	}
	v, err := s.snapshotsOf(r, p, wt.Slug, false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.html(w, http.StatusOK, "snapshots", v)
}

// databasePanel is GET /p/{prefix}/{slug}/database-panel: the Database
// tab's content (#222), which the page loads each time the tab shows, as
// the Whiteboard's (D218).
func (s *Server) databasePanel(w http.ResponseWriter, r *http.Request) {
	p, wt, ok := s.lookup(w, r)
	if !ok {
		return
	}
	v, err := s.snapshotsOf(r, p, wt.Slug, true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.html(w, http.StatusOK, "db-panel", v)
}

// snapshotAction runs `db snapshot [<name>]`, `db snapshot restore --yes
// <name>` (the page asked first), `db snapshot rm <name>` or, for dump,
// `db dump` (the starting data of new worktrees, D208) as a job. A
// name to restore or delete must be one ls lists; a new one is checked by
// the CLI.
func (s *Server) snapshotAction(w http.ResponseWriter, r *http.Request) {
	verb := r.PathValue("verb")
	if verb != "take" && verb != "restore" && verb != "rm" && verb != "dump" {
		http.Error(w, "unknown action", http.StatusNotFound)
		return
	}
	p, wt, ok := s.lookup(w, r)
	if !ok {
		return
	}
	name := r.PostFormValue("name")
	argv := []string{s.c.Exe, "-C", p.Workspace, "-w", wt.Slug, "db", "snapshot"}
	label := "snapshot"
	switch {
	case verb == "dump":
		argv, label = []string{s.c.Exe, "-C", p.Workspace, "-w", wt.Slug, "db", "dump"}, "db dump"
	case verb == "take" && name == "":
	case verb == "take":
		if badName(name) {
			http.Error(w, "bad snapshot name", http.StatusUnprocessableEntity)
			return
		}
		argv = append(argv, name)
		label += " " + name
	default:
		snaps, err := readSnapshots(r.Context(), s.c.CLI, p.Workspace, wt.Slug)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !slices.ContainsFunc(snaps, func(x snapshot) bool { return x.Name == name }) {
			http.Error(w, "unknown snapshot", http.StatusNotFound)
			return
		}
		if verb == "restore" {
			argv = append(argv, "restore", "--yes", name)
			label = "restore snapshot " + name
		} else {
			argv = append(argv, "rm", name)
			label = "delete snapshot " + name
		}
	}
	s.start(w, r, jobs.Spec{Project: p.Prefix, Slug: wt.Slug, Label: label, Dir: p.Workspace, Argv: argv}, nil, worktreePath(p, wt.Slug))
}
