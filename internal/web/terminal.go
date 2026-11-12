package web

// Terminals (D149): a pseudo-terminal on which the dashboard runs
// `railswitch agent|nvim|exec|task` for the worktree, and a WebSocket per
// panel showing it. The terminal outlives its connection (D205): it runs
// until its panel is closed or its program exits, and a page that opens it
// again gets its recent output first.

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/jbigler/railswitch/internal/pty"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/term"
)

const (
	ttyMax      = 8        // terminals open across the dashboard
	ttyFrameMax = 64 << 10 // the largest frame a browser may send
	ttyChunk    = 32 << 10 // output read per frame
	ttySizeMax  = 1000     // cols and rows are 1..ttySizeMax
	ttyGrace    = 3 * time.Second
	ttyDrain    = time.Second // output still read after the program exits
	ttyPingDef  = 30 * time.Second
	ttyPongDef  = 10 * time.Second
	ttyKeep     = 1 << 20 // output kept per terminal, for the page that shows it next
	ttyEndedMax = 8       // ended terminals kept until a page has shown their end
	ttyQueue    = 256     // chunks a connection may fall behind before it is dropped
	ttyNudge    = 150 * time.Millisecond
)

// ttyShell starts a login shell in the app container: bash when the image
// has it.
var ttyShell = []string{"exec", "--", "sh", "-c", "command -v bash >/dev/null && exec bash -l; exec sh -l"}

// agentName is an agent's name as a URL gives it (the CLI checks it again).
var agentName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

// ttyArgv is the command a terminal of kind runs; false for an unknown
// kind. name is, for agent, an agent and, for task, a task (D175), each
// already checked against the worktree's; args are a task's arguments.
func ttyArgv(exe, ws, slug, kind, name string, args []string) ([]string, bool) {
	argv := []string{exe, "-C", ws, "-w", slug}
	switch kind {
	case "agent":
		argv = append(argv, "agent")
		if name != "" {
			argv = append(argv, name)
		}
	case "task":
		if name == "" {
			return nil, false
		}
		argv = append(append(argv, "task", name), args...)
	case "nvim":
		argv = append(argv, "nvim")
	case "shell":
		argv = append(argv, ttyShell...)
	default:
		return nil, false
	}
	return argv, true
}

func (s *Server) ttyOpen() int { return int(s.ttyN.Load()) }

// ttyRefusal is a close code and the reason the panel shows.
type ttyRefusal struct {
	code   websocket.StatusCode
	reason string
}

// ttyCommand checks the request and builds the command: worktree, kind,
// agent, size; label is what a panel calls the terminal.
func (s *Server) ttyCommand(ctx context.Context, r *http.Request) (argv []string, label string, rows, cols int, no *ttyRefusal) {
	p, ok, err := s.project(ctx, r.PathValue("prefix"))
	if err != nil {
		return nil, "", 0, 0, &ttyRefusal{websocket.StatusInternalError, err.Error()}
	}
	if !ok {
		return nil, "", 0, 0, &ttyRefusal{4004, "unknown project"}
	}
	ov, err := s.overviewOf(ctx, p.Prefix, p.Workspace)
	if err != nil {
		return nil, "", 0, 0, &ttyRefusal{websocket.StatusInternalError, err.Error()}
	}
	slug := r.PathValue("slug")
	if !slices.ContainsFunc(ov.Worktrees, func(w worktree) bool { return w.Slug == slug }) {
		return nil, "", 0, 0, &ttyRefusal{4004, "unknown worktree"}
	}
	kind := r.PathValue("kind")
	q := r.URL.Query()
	agent := q.Get("agent")
	if kind == "agent" && agent != "" {
		known := false
		if agentName.MatchString(agent) {
			rows, err := readAgents(ctx, s.c.CLI, p.Workspace, slug)
			if err != nil {
				return nil, "", 0, 0, &ttyRefusal{websocket.StatusInternalError, err.Error()}
			}
			known = slices.ContainsFunc(rows, func(a agentRow) bool { return a.Name == agent })
		}
		if !known {
			return nil, "", 0, 0, &ttyRefusal{4000, "unknown agent " + agent}
		}
	}
	name := agent
	var args []string
	if kind == "task" {
		name = q.Get("task")
		if args, err = SplitArgs(q.Get("args")); err != nil {
			return nil, "", 0, 0, &ttyRefusal{4000, "args: " + err.Error()}
		}
		tasks, err := readTasks(ctx, s.c.CLI, p.Workspace, slug)
		if err != nil {
			return nil, "", 0, 0, &ttyRefusal{websocket.StatusInternalError, err.Error()}
		}
		if !slices.ContainsFunc(tasks, func(t task) bool { return t.Name == name }) {
			return nil, "", 0, 0, &ttyRefusal{4000, "unknown task " + name}
		}
	}
	argv, ok = ttyArgv(s.c.Exe, p.Workspace, slug, kind, name, args)
	if !ok {
		return nil, "", 0, 0, &ttyRefusal{4000, "unknown terminal " + kind}
	}
	rows, rerr := strconv.Atoi(q.Get("rows"))
	cols, cerr := strconv.Atoi(q.Get("cols"))
	if rerr != nil || cerr != nil || !ttySizeOK(rows, cols) {
		return nil, "", 0, 0, &ttyRefusal{4000, "bad size"}
	}
	label = kind
	if name != "" {
		label = strings.Join(append([]string{name}, args...), " ")
	}
	return argv, label, rows, cols, nil
}

func ttySizeOK(rows, cols int) bool {
	return rows >= 1 && rows <= ttySizeMax && cols >= 1 && cols <= ttySizeMax
}

// ttyResize parses a resize message: exactly {"cols":c,"rows":r}.
func ttyResize(b []byte) (rows, cols int, ok bool) {
	var m struct {
		Cols *int `json:"cols"`
		Rows *int `json:"rows"`
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&m) != nil || d.More() || m.Cols == nil || m.Rows == nil || !ttySizeOK(*m.Rows, *m.Cols) {
		return 0, 0, false
	}
	return *m.Rows, *m.Cols, true
}

// closeWith closes c with code and reason, the reason cut to what a close
// frame holds.
func closeWith(c *websocket.Conn, code websocket.StatusCode, reason string) {
	if len(reason) > 123 {
		reason = reason[:123]
	}
	_ = c.Close(code, reason)
}

// ttyModes follows the DEC private modes a terminal's program sets
// (`CSI ? n h`, reset by `CSI ? n l`): the alternate screen, mouse
// reports, bracketed paste. A page that gets only the end of the output
// (more than ttyKeep was printed) is sent them first, or a full-screen
// program would come back without its mouse and on the wrong screen.
type ttyModes struct {
	on    map[int]bool
	carry []byte // an unfinished sequence at the end of the last chunk
}

var (
	ttyModeRe = regexp.MustCompile(`\x1b\[\?([0-9;]{1,64})([hl])`)
	ttyOpenRe = regexp.MustCompile(`\x1b(\[(\?[0-9;]{0,64})?)?$`)
)

func (m *ttyModes) scan(b []byte) {
	data := b
	if len(m.carry) > 0 {
		data = append(m.carry, b...)
	}
	for _, g := range ttyModeRe.FindAllSubmatch(data, -1) {
		for _, f := range strings.Split(string(g[1]), ";") {
			n, err := strconv.Atoi(f)
			if err != nil {
				continue
			}
			if g[2][0] == 'h' {
				if m.on == nil {
					m.on = map[int]bool{}
				}
				m.on[n] = true
			} else {
				delete(m.on, n)
			}
		}
	}
	m.carry = slices.Clone(ttyOpenRe.Find(data))
}

// set is the sequences that set the modes now on, lowest first.
func (m *ttyModes) set() []byte {
	var out []byte
	for _, n := range slices.Sorted(func(yield func(int) bool) {
		for n := range m.on {
			if !yield(n) {
				return
			}
		}
	}) {
		out = append(out, "\x1b[?"+strconv.Itoa(n)+"h"...)
	}
	return out
}

// ttySession is one terminal: the program on its pseudo-terminal, the end
// of its output, and the connection showing it, if any.
type ttySession struct {
	id                 string // names it in the dashboard's list
	key                string // its command: one terminal per command
	prefix, slug, kind string
	label, url         string // the panel's name, and the path that opens it (no size)
	master             *os.File
	end                context.CancelFunc // ends the program

	mu      sync.Mutex
	buf     []byte // the last ttyKeep bytes printed
	cut     bool   // buf lost its start
	modes   ttyModes
	att     *ttyAttach
	done    bool // the program ended: exit and err are set
	exit    int
	err     error
	endedAt time.Time
}

// ttyAttach is a connection showing a session: ch is its output from the
// attach on, gone closes when the session ended or (no set) the connection
// lost it.
type ttyAttach struct {
	ch   chan []byte
	gone chan struct{}
	no   *ttyRefusal
}

// attach makes a the session's connection, dropping the one before, and
// returns the output to show first.
func (t *ttySession) attach() (replay []byte, a *ttyAttach) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.drop(&ttyRefusal{4001, "this terminal was opened in another page"})
	if t.cut {
		// From a line start where there is one near: the cut fell anywhere.
		tail := t.buf
		if i := bytes.IndexByte(tail[:min(len(tail), 4096)], '\n'); i >= 0 {
			tail = tail[i+1:]
		}
		replay = append(t.modes.set(), tail...)
	} else {
		replay = slices.Clone(t.buf)
	}
	a = &ttyAttach{ch: make(chan []byte, ttyQueue), gone: make(chan struct{})}
	if t.done {
		close(a.gone)
	} else {
		t.att = a
	}
	return replay, a
}

// drop takes the session's connection from it, with the reason it is told
// (nil: the program ended). t.mu is held.
func (t *ttySession) drop(no *ttyRefusal) {
	if t.att == nil {
		return
	}
	t.att.no = no
	close(t.att.gone)
	t.att = nil
}

// detach is a's connection going away: the program keeps running.
func (t *ttySession) detach(a *ttyAttach) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.att == a {
		t.att = nil
	}
}

// pump keeps the program's output, and passes it to the connection, until
// the master fails.
func (t *ttySession) pump() {
	buf := make([]byte, ttyChunk)
	for {
		n, err := t.master.Read(buf)
		if n > 0 {
			b := slices.Clone(buf[:n])
			t.mu.Lock()
			t.modes.scan(b)
			t.buf = append(t.buf, b...)
			if over := len(t.buf) - ttyKeep; over > 0 {
				t.buf = slices.Delete(t.buf, 0, over)
				t.cut = true
			}
			if t.att != nil {
				select {
				case t.att.ch <- b:
				default:
					t.drop(&ttyRefusal{4008, "this page fell behind the terminal"})
				}
			}
			t.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// ttySessions are the project's terminals, ended ones included, by label.
func (s *Server) ttySessions(prefix string) []*ttySession {
	s.ttyMu.Lock()
	defer s.ttyMu.Unlock()
	var out []*ttySession
	for _, t := range s.ttys {
		if t.prefix == prefix {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b *ttySession) int { return strings.Compare(a.url, b.url) })
	return out
}

// ttyList answers the project's terminals as JSON, for a page to show the
// ones opened before it loaded.
func (s *Server) ttyList(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	type row struct {
		Slug  string `json:"slug"`
		Kind  string `json:"kind"`
		Label string `json:"label"`
		URL   string `json:"url"`
	}
	rows := []row{}
	for _, t := range s.ttySessions(p.Prefix) {
		rows = append(rows, row{t.slug, t.kind, t.label, t.url})
	}
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false) // a URL's & as it is
	_ = enc.Encode(rows)
}

// ttyURL is the request's path and query without the page's own
// parameters: what opens the same terminal again.
func ttyURL(r *http.Request) string {
	var keep []string
	for _, kv := range strings.Split(r.URL.RawQuery, "&") {
		k, _, _ := strings.Cut(kv, "=")
		if kv != "" && k != "cols" && k != "rows" && k != "bg" {
			keep = append(keep, kv)
		}
	}
	if len(keep) == 0 {
		return r.URL.EscapedPath()
	}
	return r.URL.EscapedPath() + "?" + strings.Join(keep, "&")
}

// ttySession returns the running or ended terminal of argv, or starts it;
// fresh is whether it started.
func (s *Server) ttySession(r *http.Request, argv []string, label string, rows, cols int) (t *ttySession, fresh bool, no *ttyRefusal) {
	key := strings.Join(argv, "\x00")
	s.ttyMu.Lock()
	defer s.ttyMu.Unlock()
	if t := s.ttys[key]; t != nil {
		return t, false, nil
	}
	if s.ttyOpen() >= ttyMax {
		return nil, false, &ttyRefusal{4029, strconv.Itoa(ttyMax) + " terminals are running: end one from ▣ in the header"}
	}
	master, slave, err := pty.Open()
	if err != nil {
		return nil, false, &ttyRefusal{websocket.StatusInternalError, err.Error()}
	}
	if err := pty.Resize(master, rows, cols); err != nil {
		master.Close()
		slave.Close()
		return nil, false, &ttyRefusal{websocket.StatusInternalError, err.Error()}
	}
	// The program's context is the dashboard's, not the request's: it ends
	// with the session (SIGTERM to its session, SIGKILL after ttyGrace).
	ctx, end := context.WithCancel(s.ttyBase)
	env := os.Environ()
	if s.c.Env != nil {
		env = s.c.Env(ctx)
	}
	env = append(slices.Clip(env), term.BrowserEnv...)
	// The panel's own light or dark, for the nvim-attach hook (docs/nvim.md).
	if bg := r.URL.Query().Get("bg"); r.PathValue("kind") == "nvim" && (bg == "light" || bg == "dark") {
		env = append(env, "NVIM_BACKGROUND="+bg)
	}
	s.ttySeq++
	t = &ttySession{
		id: strconv.Itoa(s.ttySeq), key: key, prefix: r.PathValue("prefix"), slug: r.PathValue("slug"), kind: r.PathValue("kind"),
		label: label, url: ttyURL(r), master: master, end: end,
	}
	if s.ttys == nil {
		s.ttys = map[string]*ttySession{}
	}
	s.ttys[key] = t
	s.ttyN.Add(1)
	go func() {
		defer end()
		type ended struct {
			code int
			err  error
		}
		exited := make(chan ended, 1)
		go func() {
			res, err := s.c.Run.Run(ctx, sys.Cmd{
				Name: argv[0], Args: argv[1:], Env: env, // -C names the workspace: no Dir
				TTY: slave, Timeout: sys.NoTimeout, KillGrace: ttyGrace,
			})
			exited <- ended{res.ExitCode, err}
		}()
		output := make(chan struct{})
		go func() {
			defer close(output)
			t.pump()
		}()
		e := <-exited
		// Drain what the program printed; a background child still holding
		// the terminal gets ttyDrain.
		slave.Close()
		_ = master.SetReadDeadline(time.Now().Add(ttyDrain))
		<-output
		master.Close()
		closed := ctx.Err() != nil // its panel was closed: nothing left to show
		s.ttyMu.Lock()
		t.mu.Lock()
		t.done, t.exit, t.err, t.endedAt = true, e.code, e.err, time.Now()
		shown := t.att != nil
		t.drop(nil)
		t.mu.Unlock()
		s.ttyN.Add(-1)
		switch {
		case s.ttys[key] != t: // closed: ttyClose forgot it already
		case shown || closed:
			delete(s.ttys, key)
		default:
			s.ttyPrune()
		}
		s.ttyMu.Unlock()
		s.ttyChanged()
	}()
	return t, true, nil
}

// ttyPrune forgets the oldest ended terminals no page has shown the end
// of, past ttyEndedMax. s.ttyMu is held.
func (s *Server) ttyPrune() {
	var ended []*ttySession
	for _, t := range s.ttys {
		t.mu.Lock()
		if t.done {
			ended = append(ended, t)
		}
		t.mu.Unlock()
	}
	slices.SortFunc(ended, func(a, b *ttySession) int { return a.endedAt.Compare(b.endedAt) })
	for i := 0; i < len(ended)-ttyEndedMax; i++ {
		delete(s.ttys, ended[i].key)
	}
}

// ttyClose ends t's program and forgets t at once: the same terminal
// opened again while it dies is a new one.
func (s *Server) ttyClose(t *ttySession) {
	s.ttyForget(t)
	t.mu.Lock()
	t.drop(&ttyRefusal{4003, "this terminal was ended"})
	t.mu.Unlock()
	t.end()
}

// ttyChanged shows every page the header's new count of terminals.
func (s *Server) ttyChanged() {
	s.mu.Lock()
	s.publish()
	s.mu.Unlock()
}

// ttysView is the header's list of every terminal (D205).
type ttysView struct {
	Running, Max int
	Items        []ttysRow
}

type ttysRow struct {
	ID, Prefix, Slug, URL, Text string
	Ended                       bool
	Exit                        int
}

// ttysDialog lists the dashboard's terminals, every project's: where each
// is, and a button that ends it.
func (s *Server) ttysDialog(w http.ResponseWriter, r *http.Request) {
	v := ttysView{Max: ttyMax}
	s.ttyMu.Lock()
	for _, t := range s.ttys {
		t.mu.Lock()
		row := ttysRow{ID: t.id, Prefix: t.prefix, Slug: t.slug, URL: "/p/" + t.prefix + "/" + t.slug, Text: t.kind, Ended: t.done, Exit: t.exit}
		t.mu.Unlock()
		if t.label != t.kind {
			row.Text += ": " + t.label
		}
		if !row.Ended {
			v.Running++
		}
		v.Items = append(v.Items, row)
	}
	s.ttyMu.Unlock()
	slices.SortFunc(v.Items, func(a, b ttysRow) int {
		return cmp.Or(strings.Compare(a.Prefix, b.Prefix), strings.Compare(a.Slug, b.Slug), strings.Compare(a.Text, b.Text))
	})
	s.html(w, http.StatusOK, "ttys", v)
}

// ttyEnd ends the terminal the list names, as its panel's close does.
func (s *Server) ttyEnd(w http.ResponseWriter, r *http.Request) {
	s.ttyMu.Lock()
	var found *ttySession
	for _, t := range s.ttys {
		if t.id == r.PathValue("id") {
			found = t
		}
	}
	s.ttyMu.Unlock()
	if found == nil {
		http.Error(w, "that terminal has ended", http.StatusNotFound)
		return
	}
	s.ttyClose(found)
	s.ttyChanged() // an ended one the list held is gone from it now
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte("{}\n"))
}

// ttyForget drops a terminal: one closed, or an ended one a page has now
// shown the end of.
func (s *Server) ttyForget(t *ttySession) {
	s.ttyMu.Lock()
	defer s.ttyMu.Unlock()
	if s.ttys[t.key] == t {
		delete(s.ttys, t.key)
	}
}

// ttyMessage is a text message from the page: a resize, or the panel
// closing.
func ttyMessage(b []byte) (rows, cols int, closing, ok bool) {
	if string(b) == `{"close":true}` {
		return 0, 0, true, true
	}
	rows, cols, ok = ttyResize(b)
	return rows, cols, false, ok
}

func (s *Server) tty(w http.ResponseWriter, r *http.Request) {
	// Accept refuses an Origin other than the request's Host (HTTP 403):
	// pages on *.localhost are same-site to the cookie (spec "Access").
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(ttyFrameMax)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	argv, label, rows, cols, no := s.ttyCommand(ctx, r)
	if no != nil {
		closeWith(c, no.code, no.reason)
		return
	}
	t, fresh, no := s.ttySession(r, argv, label, rows, cols)
	if no != nil {
		closeWith(c, no.code, no.reason)
		return
	}
	replay, a := t.attach()
	defer t.detach(a)
	if fresh {
		s.ttyChanged()
	} else {
		// A full-screen program draws again when its size changes: one
		// column narrower, then the page's size.
		go func() {
			if pty.Resize(t.master, rows, max(1, cols-1)) == nil {
				time.Sleep(ttyNudge)
				_ = pty.Resize(t.master, rows, cols)
			}
		}()
	}

	// Input: keys to the terminal, resizes to its size, the panel's close.
	// Anything else ends the terminal.
	go func() {
		defer cancel()
		for {
			typ, b, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				if _, err := t.master.Write(b); err != nil {
					return
				}
				continue
			}
			rows, cols, closing, ok := ttyMessage(b)
			switch {
			case closing:
				s.ttyClose(t)
				return
			case !ok:
				closeWith(c, 4000, "bad message")
				s.ttyClose(t)
				return
			}
			_ = pty.Resize(t.master, rows, cols)
		}
	}()

	// Liveness: a peer that doesn't answer a ping is gone (a dead tunnel).
	// Its terminal keeps running for the page that opens it next.
	go func() {
		tk := time.NewTicker(s.ttyPing)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
				pctx, pcancel := context.WithTimeout(ctx, s.ttyPong)
				err := c.Ping(pctx)
				pcancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	// Output: what was printed before, then what is printed now.
	for len(replay) > 0 {
		n := min(len(replay), ttyChunk)
		if c.Write(ctx, websocket.MessageBinary, replay[:n]) != nil {
			return
		}
		replay = replay[n:]
	}
	for {
		select {
		case b := <-a.ch:
			if c.Write(ctx, websocket.MessageBinary, b) != nil {
				return
			}
			continue
		case <-ctx.Done():
			return // the page went: the terminal stays
		case <-a.gone:
		}
		break
	}
	for len(a.ch) > 0 { // gone closes after the last chunk is queued
		if c.Write(ctx, websocket.MessageBinary, <-a.ch) != nil {
			return
		}
	}
	if a.no != nil {
		closeWith(c, a.no.code, a.no.reason)
		return
	}
	s.ttyForget(t)
	t.mu.Lock()
	code, perr := t.exit, t.err
	t.mu.Unlock()
	if perr != nil && !errors.Is(perr, sys.ErrExit) {
		closeWith(c, websocket.StatusInternalError, perr.Error())
		return
	}
	_ = c.Write(ctx, websocket.MessageText, []byte(`{"exit":`+strconv.Itoa(code)+`}`))
	closeWith(c, websocket.StatusNormalClosure, "")
}
