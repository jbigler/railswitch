package lifecycle

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/render"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/unithash"
)

// Shared agent memory (D136): the project's Dolt server, started by the
// first `railswitch agent` (or a memory command that writes), never by up,
// down or wt rm, and the bd calls railswitch makes itself.

// bdTimeout bounds one bd call railswitch makes (a transient container).
const bdTimeout = 2 * time.Minute

// errNoMemory is the error of a memory command in a project without [memory].
func errNoMemory(m *render.Model) error {
	return fmt.Errorf("project %s has no shared memory: add [memory] to project.toml (docs/guide/agents.md)", m.Prefix)
}

// memoryService is the project's memory server unit.
func memoryService(prefix string) string { return naming.MemoryUnit(prefix) + ".service" }

// MemoryUp starts the project's memory server unless it runs its current
// unit, then creates the beads database when the server has none. A
// project without [memory] has nothing to start.
func (l *L) MemoryUp(ctx context.Context, m *render.Model) error {
	if m.Inputs.Project.Memory == nil {
		return nil
	}
	sd := l.systemd()
	unit := memoryService(m.Prefix)
	ctr := naming.MemoryNetwork(m.Prefix)
	st, err := sd.UnitState(ctx, unit)
	if err != nil {
		return err
	}
	key := unithash.Key(m.Prefix)
	want, err := l.renderedHash(naming.MemoryUnit(m.Prefix)+".container", key)
	if err != nil {
		return fmt.Errorf("the memory unit is not rendered (railswitch render): %w", err)
	}
	if st.Load != "loaded" || st.NeedReload || unithash.FromExec(st.ExecStart, key) != want {
		if err := sd.DaemonReload(ctx); err != nil {
			return err
		}
	}
	if sd.Timeout, err = l.startTimeout([]string{naming.MemoryUnit(m.Prefix)}); err != nil {
		return err
	}
	switch stale, err := l.staleContainer(ctx, ctr, naming.MemoryUnit(m.Prefix)+".container", key); {
	case st.Active != "active":
		done := l.busy("starting the shared memory server "+ctr, "started "+ctr)
		err := sd.Start(ctx, unit)
		done(err)
		if err != nil {
			return fmt.Errorf("starting %s: %w (journalctl --user -u %s)", unit, err, unit)
		}
	case err != nil:
		return err
	case stale:
		if err := sd.Restart(ctx, unit); err != nil {
			return fmt.Errorf("restarting %s: %w", unit, err)
		}
	}
	return l.memoryInit(ctx, m)
}

// memoryInit creates the beads database (`bd init` in server mode, stealth,
// from outside any worktree, into a throwaway BEADS_DIR: railswitch writes
// the agents' own) when the server has none. bd's prefix for issue ids is
// the project's.
func (l *L) memoryInit(ctx context.Context, m *render.Model) error {
	out, err := l.doltSQL(ctx, m, "SHOW DATABASES")
	if err != nil {
		return err
	}
	if bytes.Contains(out, []byte(`"`+render.MemoryDatabase+`"`)) {
		return nil
	}
	_, err = l.bd(ctx, m, nil, "init", "--server", "--stealth", "--non-interactive", "--skip-hooks", "--skip-agents",
		"-p", m.Prefix, "--database", render.MemoryDatabase)
	if err != nil {
		return fmt.Errorf("creating the shared memory database: %w", err)
	}
	fmt.Fprintf(l.Out, "created the shared memory database of %s\n", m.Prefix)
	return nil
}

// doltSQL runs query in the running memory server (`dolt sql` there talks
// to the server) and returns its JSON result.
func (l *L) doltSQL(ctx context.Context, m *render.Model, query string) ([]byte, error) {
	res, err := l.R.Run(ctx, sys.Cmd{Name: "podman", Args: []string{"exec", naming.MemoryNetwork(m.Prefix), "dolt", "sql", "-r", "json", "-q", query}})
	if err != nil {
		return nil, fmt.Errorf("the shared memory server: %w", err)
	}
	return res.Stdout, nil
}

// bdImage is the agent base image of the first worktree that has one
// built: bd is installed there (D136).
func (l *L) bdImage(ctx context.Context, m *render.Model) (string, error) {
	p := l.podman()
	for _, wt := range m.Inputs.Worktrees {
		in, err := m.Instance(wt)
		if err != nil {
			return "", err
		}
		img := in.Values["image.agent-base"]
		if img == "" {
			return "", fmt.Errorf("project %s has no agents: shared memory needs one", m.Prefix)
		}
		ok, err := p.ImageExists(ctx, img)
		if err != nil {
			return "", err
		}
		if ok {
			return img, nil
		}
	}
	return "", errors.New("no worktree has its agent images built yet: railswitch up (or railswitch agent) first")
}

// bd runs `bd args…` in a transient container (inMemory) and returns its
// stdout; a non-zero exit is an error carrying stderr's tail.
func (l *L) bd(ctx context.Context, m *render.Model, stdin io.Reader, args ...string) ([]byte, error) {
	res, err := l.inMemory(ctx, m, stdin, "bd", args...)
	if err != nil {
		return nil, fmt.Errorf("bd %s: %w", strings.Join(args, " "), err)
	}
	return res.Stdout, nil
}

// inMemory runs entrypoint with args in a transient container from
// bdImage on the memory network only (internal: no way out), read-only,
// every capability dropped, as the agents' user, with the agents' server
// settings and BEADS_DIR read-only from the workspace; stdin may be nil.
// bd's --json output comes in its envelope ({"schema_version", "data"}).
func (l *L) inMemory(ctx context.Context, m *render.Model, stdin io.Reader, entrypoint string, args ...string) (sys.Result, error) {
	run, err := l.memoryRunArgs(ctx, m, entrypoint, args)
	if err != nil {
		return sys.Result{}, err
	}
	return l.R.Run(ctx, sys.Cmd{Name: "podman", Args: run, Stdin: stdin, Timeout: bdTimeout})
}

// memoryRunArgs is inMemory's `podman run` argv.
func (l *L) memoryRunArgs(ctx context.Context, m *render.Model, entrypoint string, args []string) ([]string, error) {
	img, err := l.bdImage(ctx, m)
	if err != nil {
		return nil, err
	}
	const dir = "/tmp/beads"
	run := []string{"run", "--rm", "--interactive",
		"--network", naming.MemoryNetwork(m.Prefix),
		"--userns", render.WorktreeUserNS, "--user", render.WorktreeUser,
		"--read-only", "--tmpfs", "/tmp", "--workdir", "/tmp",
		"--cap-drop", "all", "--security-opt", "no-new-privileges", "--memory", "1g", "--pids-limit", "512",
		"--volume", filepath.Join(m.Inputs.Workspace, render.MemoryDir, "beads") + ":" + dir + ":ro,z",
		"--entrypoint", entrypoint,
	}
	env := render.MemoryEnv("railswitch")
	env["BEADS_DIR"] = dir
	env["HOME"] = "/tmp"
	env["BD_JSON_ENVELOPE"] = "1"
	if entrypoint == "bd" && len(args) > 0 && args[0] == "init" {
		env["BEADS_DIR"] = "/tmp/.beads" // init writes; the agents' stays as railswitch wrote it
	}
	for _, k := range slices.Sorted(maps.Keys(env)) {
		run = append(run, "--env", k+"="+env[k])
	}
	return append(append(run, img), args...), nil
}

// MemoryBackupDir holds `railswitch memory backup`'s files, 0700.
const MemoryBackupDir = "memory-backups"

// MemoryBackup exports the project's shared memory with `bd export --all`
// (JSONL: every issue with its labels, dependencies and comments, and the
// memories) to path, "" for <workspace>/memory-backups/<prefix>-<UTC
// time>.jsonl, written whole or not at all, 0600. It starts the server
// when it isn't running. It returns the path.
func (l *L) MemoryBackup(ctx context.Context, path string) (string, error) {
	m, err := l.model(ctx)
	if err != nil {
		return "", err
	}
	if m.Inputs.Project.Memory == nil {
		return "", errNoMemory(m)
	}
	if err := l.MemoryUp(ctx, m); err != nil {
		return "", err
	}
	if path == "" {
		dir := filepath.Join(m.Inputs.Workspace, MemoryBackupDir)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		path = filepath.Join(dir, m.Prefix+"-"+time.Now().UTC().Format("20060102T150405Z")+".jsonl")
	}
	run, err := l.memoryRunArgs(ctx, m, "bd", []string{"export", "--all"})
	if err != nil {
		return "", err
	}
	n, err := writeStream(ctx, l.R, path, false, sys.Cmd{Name: "podman", Args: run, Timeout: dumpTimeout})
	if err != nil {
		return "", fmt.Errorf("bd export: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil { // project notes: the user's only
		return "", err
	}
	fmt.Fprintf(l.Out, "wrote %s (%d bytes)\n", path, n)
	return path, nil
}

// MemoryRestore imports a MemoryBackup file with `bd import --allow-stale`:
// every issue and memory in it is written back over the one with its id,
// older or not; what the file doesn't hold stays. On a new volume the
// database is created first. It asks first unless yes; without a terminal
// it refuses.
func (l *L) MemoryRestore(ctx context.Context, path string, yes bool) error {
	m, err := l.model(ctx)
	if err != nil {
		return err
	}
	if m.Inputs.Project.Memory == nil {
		return errNoMemory(m)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil {
		return err
	} else if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s: not a regular file", path)
	}
	if !yes {
		what := fmt.Sprintf("write every issue and memory in %s over the shared memory of %s", path, m.Prefix)
		if !l.TTY {
			return fmt.Errorf("memory restore would %s: pass --yes (no terminal to confirm on)", what)
		}
		fmt.Fprintf(l.Out, "This will %s; their current text is lost. Continue? [y/N] ", what)
		line, _ := bufio.NewReader(l.In).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintln(l.Out, "aborted")
			return nil
		}
	}
	if err := l.MemoryUp(ctx, m); err != nil {
		return err
	}
	run, err := l.memoryRunArgs(ctx, m, "bd", []string{"import", "--allow-stale", "-"})
	if err != nil {
		return err
	}
	res, err := l.R.Run(ctx, sys.Cmd{Name: "podman", Args: run, Stdin: f, Timeout: dumpTimeout})
	if err != nil {
		return fmt.Errorf("bd import: %w", err)
	}
	l.Out.Write(res.Stdout)
	return nil
}

// MemoryMigrate applies the pending beads schema migrations to the
// project's database (`bd migrate schema`: the verb is the consent bd
// asks for before migrating a shared server's database). Run it once every
// agent image has the new bd: an older one refuses the migrated database.
func (l *L) MemoryMigrate(ctx context.Context) error {
	m, err := l.model(ctx)
	if err != nil {
		return err
	}
	if m.Inputs.Project.Memory == nil {
		return errNoMemory(m)
	}
	if err := l.MemoryUp(ctx, m); err != nil {
		return err
	}
	out, err := l.bd(ctx, m, nil, "migrate", "schema")
	if err != nil {
		return err
	}
	l.Out.Write(out)
	return nil
}

// MemoryReport is `railswitch memory --json` (docs/json.md).
type MemoryReport struct {
	Server    MemoryServer     `json:"server"`
	Schema    int              `json:"schema"` // the database's beads schema version; 0 when the server isn't running
	Issues    *MemoryCounts    `json:"issues"` // nil when the server isn't running
	Memories  int              `json:"memories"`
	Worktrees []MemoryWorktree `json:"worktrees"`
	Activity  []MemoryEvent    `json:"activity"`
	Cursor    int64            `json:"cursor"` // pass as --since to read only newer activity
	Error     string           `json:"error"`
}

// MemoryServer is the memory server's unit and container.
type MemoryServer struct {
	Unit      string `json:"unit"`
	Container string `json:"container"`
	Image     string `json:"image"`
	Volume    string `json:"volume"`
	State     string `json:"state"` // systemd's ActiveState; "" when the unit isn't loaded
}

// MemoryCounts are bd status's summary.
type MemoryCounts struct {
	Total      int `json:"total"`
	Open       int `json:"open"`
	InProgress int `json:"in_progress"`
	Blocked    int `json:"blocked"`
	Ready      int `json:"ready"`
	Closed     int `json:"closed"`
}

// MemoryWorktree is the work labelled worktree:<slug>.
type MemoryWorktree struct {
	Slug    string        `json:"slug"`
	Ready   []MemoryIssue `json:"ready"`
	Claimed []MemoryIssue `json:"claimed"`
	Blocked []MemoryIssue `json:"blocked"`
}

// MemoryIssue is the part of a bd issue the report keeps.
type MemoryIssue struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Status    string   `json:"status"`
	Priority  int      `json:"priority"`
	Assignee  string   `json:"assignee"`
	Labels    []string `json:"labels"`
	UpdatedAt string   `json:"updated_at"`
}

// MemoryEvent is one bd_events_journal record (bd events tail).
type MemoryEvent struct {
	Seq      int64  `json:"seq"`
	TS       string `json:"ts"`
	Op       string `json:"op"`
	IssueID  string `json:"issue_id"`
	Actor    string `json:"actor"`
	Worktree string `json:"worktree"` // the actor's worktree (BEADS_ACTOR <slug>/<agent>); "" for another actor
	Agent    string `json:"agent"`
}

// activityLimit caps the activity one report returns: poll again with
// the cursor for more.
const activityLimit = 200

// memoryScript runs every query of a report in one container: one line
// per query, "<tag> <compact JSON>" or "<tag> null" when bd failed (its
// stderr says why). $1 is the activity cursor.
var memoryScript = `emit() {
  tag=$1; shift
  if out=$("$@"); then printf '%s %s\n' "$tag" "$(printf '%s' "$out" | jq -c .)"; else printf '%s null\n' "$tag"; fi
}
emit status bd status --json
emit memories bd memories --json
emit ready bd ready -n 0 --json
emit claimed bd list -s in_progress -n 0 --json
emit blocked bd blocked --json
emit activity sh -c 'bd events tail --since "$1" --limit ` + strconv.Itoa(activityLimit) + ` | jq -cs .' sh "$1"
`

// Memory reads the project's shared memory: its server, and when it runs,
// bd's counts, each worktree's ready, claimed and blocked work (by its
// worktree:<slug> label) and the journal's activity after since. It never
// starts the server. A failure to read is the report's Error.
func (l *L) Memory(ctx context.Context, since int64) (*MemoryReport, error) {
	m, err := l.model(ctx)
	if err != nil {
		return nil, err
	}
	if m.Inputs.Project.Memory == nil {
		return nil, errNoMemory(m)
	}
	r := &MemoryReport{
		Server: MemoryServer{
			Unit: memoryService(m.Prefix), Container: naming.MemoryNetwork(m.Prefix),
			Image: render.MemoryImage, Volume: naming.MemoryNetwork(m.Prefix),
		},
		Worktrees: []MemoryWorktree{}, Activity: []MemoryEvent{}, Cursor: since,
	}
	for _, wt := range m.Inputs.Worktrees {
		r.Worktrees = append(r.Worktrees, MemoryWorktree{Slug: wt.Slug, Ready: []MemoryIssue{}, Claimed: []MemoryIssue{}, Blocked: []MemoryIssue{}})
	}
	st, err := l.systemd().UnitState(ctx, r.Server.Unit)
	if err != nil {
		return nil, err
	}
	if st.Load == "loaded" {
		r.Server.State = st.Active
	}
	if r.Server.State != "active" {
		return r, nil
	}
	if out, err := l.doltSQL(ctx, m, "SELECT COALESCE(MAX(version),0) AS v FROM `"+render.MemoryDatabase+"`.schema_migrations"); err != nil {
		r.Error = err.Error()
		return r, nil
	} else if r.Schema, err = schemaVersion(out); err != nil {
		r.Error = err.Error()
		return r, nil
	}
	res, err := l.inMemory(ctx, m, nil, "sh", "-c", memoryScript, "sh", strconv.FormatInt(since, 10))
	if err != nil {
		r.Error = err.Error()
		return r, nil
	}
	if err := r.fill(res.Stdout); err != nil {
		r.Error = err.Error()
		if tail := strings.TrimSpace(string(res.Stderr)); tail != "" {
			r.Error += ": " + lastLine(tail)
		}
	}
	return r, nil
}

// schemaVersion reads `dolt sql -r json`'s {"rows":[{"v":N}]}; dolt
// 2.2.0 quotes the number.
func schemaVersion(out []byte) (int, error) {
	var doc struct {
		Rows []struct {
			V json.RawMessage `json:"v"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(out, &doc); err == nil && len(doc.Rows) == 1 {
		if v, err := strconv.Atoi(strings.Trim(string(doc.Rows[0].V), `"`)); err == nil {
			return v, nil
		}
	}
	return 0, fmt.Errorf("the memory database's schema version: unexpected answer %q", bytes.TrimSpace(out))
}

func lastLine(s string) string {
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// fill reads memoryScript's lines into r. bd's output is hostile only in
// what it says, never in its shape: anything that doesn't decode is an
// error naming the query.
func (r *MemoryReport) fill(out []byte) error {
	byTag := map[string][]byte{}
	for line := range strings.Lines(string(out)) {
		tag, js, ok := strings.Cut(strings.TrimRight(line, "\n"), " ")
		if ok {
			byTag[tag] = []byte(js)
		}
	}
	var failed []string
	data := func(tag string, v any) {
		js := byTag[tag]
		if len(js) == 0 || string(js) == "null" {
			failed = append(failed, tag)
			return
		}
		env := struct {
			Data json.RawMessage `json:"data"`
		}{}
		if tag == "activity" { // JSON lines, slurped: no envelope
			env.Data = js
		} else if err := json.Unmarshal(js, &env); err != nil {
			failed = append(failed, tag)
			return
		}
		if err := json.Unmarshal(env.Data, v); err != nil {
			failed = append(failed, tag)
		}
	}
	var status struct {
		Summary struct {
			Total      int `json:"total_issues"`
			Open       int `json:"open_issues"`
			InProgress int `json:"in_progress_issues"`
			Blocked    int `json:"blocked_issues"`
			Ready      int `json:"ready_issues"`
			Closed     int `json:"closed_issues"`
		} `json:"summary"`
	}
	data("status", &status)
	sm := status.Summary
	r.Issues = &MemoryCounts{Total: sm.Total, Open: sm.Open, InProgress: sm.InProgress, Blocked: sm.Blocked, Ready: sm.Ready, Closed: sm.Closed}
	var memories map[string]any
	data("memories", &memories)
	r.Memories = len(memories)
	for _, q := range []struct {
		tag string
		get func(*MemoryWorktree) *[]MemoryIssue
	}{
		{"ready", func(w *MemoryWorktree) *[]MemoryIssue { return &w.Ready }},
		{"claimed", func(w *MemoryWorktree) *[]MemoryIssue { return &w.Claimed }},
		{"blocked", func(w *MemoryWorktree) *[]MemoryIssue { return &w.Blocked }},
	} {
		var issues []MemoryIssue
		data(q.tag, &issues)
		for _, is := range issues {
			if is.Labels == nil {
				is.Labels = []string{}
			}
			for i := range r.Worktrees {
				if slices.Contains(is.Labels, "worktree:"+r.Worktrees[i].Slug) {
					list := q.get(&r.Worktrees[i])
					*list = append(*list, is)
				}
			}
		}
	}
	var events []MemoryEvent
	data("activity", &events)
	for _, ev := range events {
		ev.Worktree, ev.Agent, _ = strings.Cut(ev.Actor, "/")
		if ev.Agent == "" {
			ev.Worktree = ""
		}
		r.Activity = append(r.Activity, ev)
		r.Cursor = max(r.Cursor, ev.Seq)
	}
	if len(failed) > 0 {
		return fmt.Errorf("bd could not answer: %s", strings.Join(failed, ", "))
	}
	return nil
}

// bdPinRe is the agent base's bd pin (D138).
var bdPinRe = regexp.MustCompile(`(?m)^ARG BD_VERSION=(\S+)\s*$`)

// MemoryCheck is doctor's "memory version" (D145): every agent container
// running in the project and every agent image built for its current units
// must hold the bd the agent base pins (an image's tag covers the
// Containerfile, so a new pin means new images, which railswitch agent
// builds before it runs one; a running agent keeps its old image until it
// stops), and that bd must open the server's database (bd refuses one a
// newer bd migrated, and won't migrate a shared one on its own). ok with
// what was checked, else what is wrong and the fix. A server that isn't
// running, or no image built yet, is not asked.
func (l *L) MemoryCheck(ctx context.Context) (ok bool, detail, fix string) {
	m, err := l.model(ctx)
	if err != nil {
		return false, err.Error(), "railswitch render"
	}
	data, err := os.ReadFile(filepath.Join(m.Inputs.Workspace, "containerfiles", "Containerfile.agent-base"))
	if err != nil {
		return false, err.Error(), "railswitch lock --review (the project's agent base)"
	}
	pm := bdPinRe.FindSubmatch(data)
	if pm == nil {
		return false, "containerfiles/Containerfile.agent-base pins no bd (no ARG BD_VERSION=)",
			"railswitch lock --review, then accept the agent base's update; an edited copy: railswitch containerfile show agent-base --json shows the default's bd lines"
	}
	pin := string(pm[1])
	// bdOf reads an image's or a container's bd labels (D138).
	bdOf := func(kind, name string) (string, bool) {
		res, err := l.R.Run(ctx, sys.Cmd{Name: "podman", Args: []string{kind, "inspect", "--format", `{{index .Config.Labels "railswitch.memory"}} {{index .Config.Labels "railswitch.bd.version"}}`, name}})
		if err != nil {
			return "", false // not built, or not running
		}
		mem, ver, _ := strings.Cut(strings.TrimSpace(string(res.Stdout)), " ")
		if mem != "1" {
			return "no bd", true
		}
		return "bd " + ver, true
	}
	var stale, fixes []string
	built := 0
	checked := map[string]bool{}
	p := l.podman()
	for _, wt := range m.Inputs.Worktrees {
		in, err := m.Instance(wt)
		if err != nil {
			return false, err.Error(), "railswitch render"
		}
		for _, s := range m.Agents() {
			ctr := naming.Container(m.Prefix, wt.Slug, s.ID)
			if running, err := p.Running(ctx, ctr); err == nil && running {
				if have, ok := bdOf("container", ctr); ok && have != "bd "+pin {
					stale = append(stale, ctr+" runs "+have)
					fixes = append(fixes, "railswitch -w "+wt.Slug+" agent stop "+strings.TrimPrefix(s.ID, "agent-"))
				}
			}
			for _, img := range []string{in.Values["image.agent-base"], in.Values["image."+s.ID]} {
				if img == "" || checked[img] {
					continue
				}
				checked[img] = true
				have, ok := bdOf("image", img)
				if !ok {
					continue
				}
				built++
				if have != "bd "+pin {
					stale = append(stale, img+" holds "+have)
					fixes = append(fixes, "the agent base pins bd "+pin+" but doesn't install it: compare containerfiles/Containerfile.agent-base with railswitch containerfile show agent-base --json")
				}
			}
		}
	}
	if len(stale) > 0 {
		slices.Sort(fixes)
		return false, "the agent base pins bd " + pin + "; " + strings.Join(stale, ", "), strings.Join(slices.Compact(fixes), "; ")
	}
	st, err := l.systemd().UnitState(ctx, memoryService(m.Prefix))
	if err != nil {
		return false, err.Error(), ""
	}
	switch {
	case built == 0:
		return true, "the agent base pins bd " + pin + "; no agent image built for it yet (railswitch agent builds it)", ""
	case st.Load != "loaded" || st.Active != "active":
		return true, "bd " + pin + " in every built agent image; the memory server isn't running (not asked)", ""
	}
	if _, err := l.bd(ctx, m, nil, "status", "--json"); err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "schema version mismatch"):
			return false, lineWith(msg, "schema version mismatch"), "a newer bd migrated the database: pin that bd in containerfiles/Containerfile.agent-base (or accept the agent base's update: railswitch lock --review), then railswitch agent rebuilds the images"
		case strings.Contains(msg, "refusing to auto-apply"):
			return false, lineWith(msg, "refusing to auto-apply"), "bd " + pin + " needs the database migrated: stop every older agent, then railswitch memory migrate"
		}
		return false, msg, "journalctl --user -u " + memoryService(m.Prefix)
	}
	return true, "bd " + pin + " in every built agent image and running agent, and it opens the memory database", ""
}

// lineWith is the line of s holding phrase, trimmed of JSON quoting: bd's
// errors come as JSON on stderr.
func lineWith(s, phrase string) string {
	for l := range strings.Lines(s) {
		if i := strings.Index(l, phrase); i >= 0 {
			return "bd: " + strings.TrimRight(strings.TrimSpace(l[i:]), `",`)
		}
	}
	return s
}
