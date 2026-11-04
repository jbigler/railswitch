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

type snapshotsView struct {
	Slug, TakeURL, RestoreURL, RmURL string
	Snapshots                        []snapshotRow
}

type snapshotRow struct{ Name, Created, Size string }

// snapshotsDialog lists the worktree's database snapshots, each with
// restore and delete, and takes a new one.
func (s *Server) snapshotsDialog(w http.ResponseWriter, r *http.Request) {
	p, wt, ok := s.lookup(w, r)
	if !ok {
		return
	}
	snaps, err := readSnapshots(r.Context(), s.c.CLI, p.Workspace, wt.Slug)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	base := worktreePath(p, wt.Slug) + "/snapshots/"
	v := snapshotsView{Slug: wt.Slug, TakeURL: base + "take", RestoreURL: base + "restore", RmURL: base + "rm"}
	for _, sn := range snaps {
		v.Snapshots = append(v.Snapshots, snapshotRow{Name: sn.Name, Created: sn.Created.Local().Format("2006-01-02 15:04"), Size: fmt.Sprintf("%.1f MB", float64(sn.Bytes)/1e6)})
	}
	s.html(w, http.StatusOK, "snapshots", v)
}

// snapshotAction runs `db snapshot [<name>]`, `db snapshot restore --yes
// <name>` (the page asked first) or `db snapshot rm <name>` as a job. A
// name to restore or delete must be one ls lists; a new one is checked by
// the CLI.
func (s *Server) snapshotAction(w http.ResponseWriter, r *http.Request) {
	verb := r.PathValue("verb")
	if verb != "take" && verb != "restore" && verb != "rm" {
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
