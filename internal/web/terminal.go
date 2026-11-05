package web

// Terminals (D149): a WebSocket per panel, bridged to a pseudo-terminal on
// which the dashboard runs `railswitch agent|nvim|exec` for the worktree.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
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

// ttyTake claims a terminal slot; false when ttyMax are open.
func (s *Server) ttyTake() bool {
	for {
		n := s.ttyN.Load()
		if n >= ttyMax {
			return false
		}
		if s.ttyN.CompareAndSwap(n, n+1) {
			return true
		}
	}
}

func (s *Server) ttyOpen() int { return int(s.ttyN.Load()) }

// ttyRefusal is a close code and the reason the panel shows.
type ttyRefusal struct {
	code   websocket.StatusCode
	reason string
}

// ttyCommand checks the request and builds the command: worktree, kind,
// agent, size.
func (s *Server) ttyCommand(ctx context.Context, r *http.Request) (argv []string, rows, cols int, no *ttyRefusal) {
	p, ok, err := s.project(ctx, r.PathValue("prefix"))
	if err != nil {
		return nil, 0, 0, &ttyRefusal{websocket.StatusInternalError, err.Error()}
	}
	if !ok {
		return nil, 0, 0, &ttyRefusal{4004, "unknown project"}
	}
	ov, err := s.overviewOf(ctx, p.Prefix, p.Workspace)
	if err != nil {
		return nil, 0, 0, &ttyRefusal{websocket.StatusInternalError, err.Error()}
	}
	slug := r.PathValue("slug")
	if !slices.ContainsFunc(ov.Worktrees, func(w worktree) bool { return w.Slug == slug }) {
		return nil, 0, 0, &ttyRefusal{4004, "unknown worktree"}
	}
	kind := r.PathValue("kind")
	q := r.URL.Query()
	agent := q.Get("agent")
	if kind == "agent" && agent != "" {
		known := false
		if agentName.MatchString(agent) {
			rows, err := readAgents(ctx, s.c.CLI, p.Workspace, slug)
			if err != nil {
				return nil, 0, 0, &ttyRefusal{websocket.StatusInternalError, err.Error()}
			}
			known = slices.ContainsFunc(rows, func(a agentRow) bool { return a.Name == agent })
		}
		if !known {
			return nil, 0, 0, &ttyRefusal{4000, "unknown agent " + agent}
		}
	}
	name := agent
	var args []string
	if kind == "task" {
		name = q.Get("task")
		if args, err = SplitArgs(q.Get("args")); err != nil {
			return nil, 0, 0, &ttyRefusal{4000, "args: " + err.Error()}
		}
		tasks, err := readTasks(ctx, s.c.CLI, p.Workspace, slug)
		if err != nil {
			return nil, 0, 0, &ttyRefusal{websocket.StatusInternalError, err.Error()}
		}
		if !slices.ContainsFunc(tasks, func(t task) bool { return t.Name == name }) {
			return nil, 0, 0, &ttyRefusal{4000, "unknown task " + name}
		}
	}
	argv, ok = ttyArgv(s.c.Exe, p.Workspace, slug, kind, name, args)
	if !ok {
		return nil, 0, 0, &ttyRefusal{4000, "unknown terminal " + kind}
	}
	rows, rerr := strconv.Atoi(q.Get("rows"))
	cols, cerr := strconv.Atoi(q.Get("cols"))
	if rerr != nil || cerr != nil || !ttySizeOK(rows, cols) {
		return nil, 0, 0, &ttyRefusal{4000, "bad size"}
	}
	return argv, rows, cols, nil
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

	argv, rows, cols, no := s.ttyCommand(ctx, r)
	if no != nil {
		closeWith(c, no.code, no.reason)
		return
	}
	if !s.ttyTake() {
		closeWith(c, 4029, strconv.Itoa(ttyMax)+" terminals are open: close one")
		return
	}
	defer s.ttyN.Add(-1)
	master, slave, err := pty.Open()
	if err != nil {
		closeWith(c, websocket.StatusInternalError, err.Error())
		return
	}
	defer master.Close()
	defer slave.Close()
	if err := pty.Resize(master, rows, cols); err != nil {
		closeWith(c, websocket.StatusInternalError, err.Error())
		return
	}
	env := os.Environ()
	if s.c.Env != nil {
		env = s.c.Env(ctx)
	}
	env = append(slices.Clip(env), term.BrowserEnv...)
	// The panel's own light or dark, for the nvim-attach hook (docs/nvim.md).
	if bg := r.URL.Query().Get("bg"); r.PathValue("kind") == "nvim" && (bg == "light" || bg == "dark") {
		env = append(env, "NVIM_BACKGROUND="+bg)
	}

	// The program: its context ends with the connection (SIGTERM to its
	// session, SIGKILL after ttyGrace).
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

	// Output: the terminal to the browser, until the master fails.
	output := make(chan struct{})
	go func() {
		defer close(output)
		buf := make([]byte, ttyChunk)
		for {
			n, err := master.Read(buf)
			if n > 0 && c.Write(ctx, websocket.MessageBinary, buf[:n]) != nil {
				return
			}
			if err != nil {
				return
			}
		}
	}()

	// Input: keys to the terminal, resizes to its size. Anything else ends
	// the terminal.
	go func() {
		defer cancel()
		for {
			typ, b, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				if _, err := master.Write(b); err != nil {
					return
				}
				continue
			}
			rows, cols, ok := ttyResize(b)
			if !ok {
				closeWith(c, 4000, "bad message")
				return
			}
			_ = pty.Resize(master, rows, cols)
		}
	}()

	// Liveness: a peer that doesn't answer a ping is gone (a dead tunnel).
	go func() {
		t := time.NewTicker(s.ttyPing)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
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

	select {
	case e := <-exited:
		// Drain what the program printed; a background child still holding
		// the terminal gets ttyDrain.
		slave.Close()
		_ = master.SetReadDeadline(time.Now().Add(ttyDrain))
		<-output
		if e.err != nil && !errors.Is(e.err, sys.ErrExit) && ctx.Err() == nil {
			closeWith(c, websocket.StatusInternalError, e.err.Error())
			return
		}
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"exit":`+strconv.Itoa(e.code)+`}`))
		closeWith(c, websocket.StatusNormalClosure, "")
	case <-ctx.Done():
		// The panel closed, the peer went silent or sent a bad message:
		// hang up, and the cancelled context ends the program's session.
		master.Close()
		select {
		case <-exited:
		case <-time.After(ttyGrace + 3*time.Second):
		}
	}
}
