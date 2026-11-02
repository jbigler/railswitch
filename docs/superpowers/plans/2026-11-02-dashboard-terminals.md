# Dashboard terminals Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A live xterm.js terminal in the dashboard (worktree page and office) connected to the worktree's agent, nvim, or a shell in the app container.

**Architecture:** The dashboard opens a Linux pseudo-terminal, runs `railswitch -C <ws> -w <slug> agent|nvim|exec …` on it through `sys.Runner`, and bridges it to the browser over a WebSocket (`github.com/coder/websocket`). The browser side is one ES module, `static/term/term.js`, over vendored xterm.js with its WebGL renderer.

**Tech Stack:** Go 1.27 stdlib + `github.com/coder/websocket` v1.8.15; `@xterm/xterm` 5.5.0, `@xterm/addon-fit` 0.10.0, `@xterm/addon-webgl` 0.18.0.

**Spec:** `docs/superpowers/specs/2026-11-02-dashboard-terminals-design.md`

## Global Constraints

- Dependencies: stdlib + `github.com/BurntSushi/toml` + `github.com/coder/websocket` (reason: the WebSocket parser is security-sensitive).
- Unit tests need no podman, systemd, gh or network (`git`, `bash` allowed).
- Commands: argv only, never a shell on the host; every call through `sys.Runner`; terminals use `sys.NoTimeout`, `KillGrace: 3 * time.Second`.
- Origin: `websocket.Accept` with default options only (no `InsecureSkipVerify`, no `OriginPatterns`).
- Limits: 8 terminals across the dashboard; browser frame ≤ 64 KiB; cols/rows integers 1..1000; ping every 30 s, pong within 10 s.
- Close codes: `4004 unknown worktree`, `4000 unknown agent <n>`, `4000 bad size`, `4000 bad message`, `4029 8 terminals are open: close one`, `1011 <reason>`; normal end `{"exit":N}` text frame then 1000.
- Env: the jobs' env plus `TERM=xterm-256color`, `COLORTERM=truecolor`.
- Nothing a terminal carries is logged.
- No inline script or style; CSP unchanged (`default-src 'self'; …`).
- `make check` before every commit.

## Spike result (spec "Risk checked first")

Checked statically on the 5.5.0 tarball: the DOM renderer creates two `<style>` elements (`_themeStyleElement`, `_dimensionsStyleElement`), which the CSP blocks; `@xterm/addon-webgl` 0.18.0 creates none. Decision: load the WebGL addon right after `open()` (it can't load before); the brief DOM renderer leaves at most two CSP reports in the console at open. Without WebGL (`new WebglAddon()` throws or `onContextLoss`), the panel shows "This terminal needs WebGL" and closes its socket.

## Review Focus

1. A paste larger than 64 KiB would exceed the server's read limit and kill the connection → the client splits sends into 16 KiB frames (Task 4, manual check: paste 200 KiB into the shell).
2. Keys typed before the socket opens are lost or sent early → input is ignored until `open`, and the overlay says "Connecting…" (Task 4).
3. The worktree detail is replaced on every SSE update, which would destroy a terminal inside it → the dock lives outside `#detail` (Task 4, test that the layout renders `#terms` outside `#detail`).
4. A child that leaves a background process holding the slave would keep the reader blocked after exit → after `Run` returns, close the slave and give the drain a 1 s read deadline (Task 3, test with `sh -c 'sleep 30 & exit 3'`: the client gets `{"exit":3}` within 2 s).
5. Office panels close by opening another → the terminal's socket closes with its panel (Task 5, `openPanel`'s returned cleanup).

---

### Task 1: `internal/pty`

**Files:**
- Create: `internal/pty/pty.go`, `internal/pty/pty_test.go`

**Interfaces:**
- Produces: `pty.Open() (master, slave *os.File, err error)`; `pty.Resize(f *os.File, rows, cols int) error`; `pty.Size(f *os.File) (rows, cols int, err error)` (used by tests).

- [ ] **Step 1: Write the failing tests**

`TestOpenEcho`: open; write `"hi\n"` to slave; read from master until it contains `"hi"` (2 s deadline). `TestResize`: `Resize(master, 40, 120)`; `Size(slave)` returns `40, 120`. Both `t.Skip` when `/dev/ptmx` doesn't exist.

- [ ] **Step 2: Run** `go test ./internal/pty/` — FAIL (package missing).
- [ ] **Step 3: Implement** with `syscall`: open `/dev/ptmx` `O_RDWR|O_NOCTTY|O_CLOEXEC`; `TIOCSPTLCK` with 0; `TIOCGPTN` → n; open `/dev/pts/<n>` `O_RDWR|O_NOCTTY|O_CLOEXEC`; `TIOCSWINSZ`/`TIOCGWINSZ` with a `struct{Row, Col, X, Y uint16}`. Open master with `os.OpenFile` so it is pollable (read deadlines work).
- [ ] **Step 4: Run** `go test ./internal/pty/` — PASS.
- [ ] **Step 5: Commit** `pty: open a pseudo-terminal and set its size (D149)`.

### Task 2: `sys.Cmd.TTY`

**Files:**
- Modify: `internal/sys/runner.go` (Cmd, Exec.Run)
- Test: `internal/sys/runner_test.go`

**Interfaces:**
- Consumes: `pty.Open`, `pty.Resize` (tests only).
- Produces: `sys.Cmd.TTY *os.File` — when set, stdin/stdout/stderr are that file (Stdin/Stdout/Stderr ignored) and the child starts with `SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}`; with `KillGrace` the SIGTERM/SIGKILL go to `-pid` (the session's group). `systest.Fake` needs no change (it records argv; tests reading `TTY` use their own runner).

- [ ] **Step 1: Write the failing tests**

`TestTTY`: open a pty, `Resize(master, 30, 100)`, run `sh -c 'tty; stty size; ps -o sid= -p $$; echo pid=$$'` with `TTY: slave`, `Timeout: sys.NoTimeout`, reading master in a goroutine; output contains `/dev/pts/`, `30 100`, and the sid equals the printed pid. `TestTTYCancel`: run `sh -c 'trap "" TERM; sleep 30 & wait'` with `TTY`, `KillGrace: 200ms`; cancel after 100 ms; `Run` returns within 2 s and `syscall.Kill(-pid, 0)` is `ESRCH` (pid from `echo $$` first).

- [ ] **Step 2: Run** `go test ./internal/sys/ -run TTY` — FAIL (no field).
- [ ] **Step 3: Implement** in `Exec.Run`: if `c.TTY != nil`, set `cmd.Stdin/Stdout/Stderr = c.TTY` and use the Setsid attr in place of `Setpgid` in both the KillGrace and timed branches (Setpgid after Setsid fails).
- [ ] **Step 4: Run** `go test ./internal/sys/` — PASS.
- [ ] **Step 5: Commit** `sys: run a command on a terminal, in its own session (D149)`.

### Task 3: The terminal endpoint

**Files:**
- Create: `internal/web/terminal.go`, `internal/web/terminal_test.go`
- Modify: `internal/web/server.go` (Config: `Run sys.Runner`, `Env func(context.Context) []string`; route; Server: open-terminal counter), `internal/cli/dashboard_serve.go` (pass `e.run` and the jobs' env closure), `go.mod`/`go.sum`

**Interfaces:**
- Consumes: `pty.Open/Resize`, `sys.Cmd.TTY`, `s.lookup`, `readAgents`, `agentName`.
- Produces: route `GET /p/{prefix}/{slug}/tty/{kind}`; `ttyArgv(exe, ws, slug, kind, agent string) ([]string, bool)`; wire protocol: client→server binary = input, text = `{"cols":c,"rows":r}`; server→client binary = output, text `{"exit":N}` before a 1000 close.

Constants in `terminal.go`: `ttyMax = 8`, `ttyFrameMax = 64 << 10`, `ttyChunk = 32 << 10`, `ttyPing = 30 * time.Second`, `ttyPong = 10 * time.Second`, `ttyGrace = 3 * time.Second`, `ttyDrain = time.Second`, and the shell's argv tail `[]string{"exec", "--", "sh", "-c", "command -v bash >/dev/null && exec bash -l; exec sh -l"}`. Ping and pong are Server fields (default the constants) so tests shorten them.

- [ ] **Step 1: Add the dependency** `go get github.com/coder/websocket@v1.8.15`; `go mod tidy`.

- [ ] **Step 2: Write the failing tests** (`httptest.NewServer(f.s.Handler())`; a dialer whose transport sets `req.Host = testHost`, sends the cookie and `Origin: http://railswitch.localhost:7700`; `Exe` set to a temp bash script so the real argv runs a test program; `Run: sys.Exec{}`):

  - `TestTTYArgv` (table): `agent ""` → `[exe -C /ws/ftest -w b agent]`; `agent probe` → `[… agent probe]`; `nvim` → `[… nvim]`; `shell` → `[… exec -- sh -c "<the line>"]`; kind `bash` → not ok.
  - `TestTTYOrigin`: Origin `http://app.foo.localhost` → dial fails with HTTP 403; no cookie → 401.
  - `TestTTYRefusals`: unknown worktree → close 4004; `?agent=nope` (not in `agent ls`) → 4000 `unknown agent nope`; `cols=0` → 4000 `bad size`; text frame `hello` → 4000 `bad message`; 9th terminal → 4029.
  - `TestTTYEcho`: exe script `exec cat`; send binary `abc\r`; read output contains `abc`.
  - `TestTTYResize`: exe script `read x; stty size; exit 0`; send `{"cols":90,"rows":20}`, then `\r`; output contains `20 90`; then text `{"exit":0}` and close 1000.
  - `TestTTYExitCode`: exe `sh -c 'sleep 30 & exit 3'` → `{"exit":3}` within 2 s.
  - `TestTTYClientClose`: exe `echo $$ > <tmp>/pid; exec sleep 60`; client closes; within 5 s `kill -0 <pid>` fails; the open-terminal count is 0.
  - `TestTTYPing`: Server ping 50 ms, pong 50 ms; a client that never reads → server closes within 1 s and the process ends.
  - `TestTTYEnv`: exe `echo "$TERM $COLORTERM"` → `xterm-256color truecolor`.

- [ ] **Step 3: Run** `go test ./internal/web/ -run TTY` — FAIL.

- [ ] **Step 4: Implement `func (s *Server) tty(w http.ResponseWriter, r *http.Request)`**

  Order: guard (already) → `websocket.Accept(w, r, nil)` (Origin refusal is its HTTP 403) → `SetReadLimit(ttyFrameMax)` → validate (lookup worktree by hand so the refusal is a close code, not `s.lookup`'s HTTP error; kind via `ttyArgv`; agent name via `agentName` then `readAgents`; cols/rows) → take a slot (mutex counter, release on return) → `pty.Open` → `Resize` → `s.c.Run.Run` in a goroutine with `TTY: slave, Env: append(s.c.Env(ctx), "TERM=xterm-256color", "COLORTERM=truecolor"), Dir: p.Workspace, Timeout: sys.NoTimeout, KillGrace: ttyGrace` and a ctx cancelled when the handler ends. Loops: master → binary frames (`ttyChunk`); frames → master / Resize (strict JSON: `DisallowUnknownFields`, bounds); a ticker pinging with `ttyPong` timeout. On `Run` return: close slave, `master.SetReadDeadline(now + ttyDrain)`, finish the reader, send `{"exit":N}`, `Close(1000, "")`. On read error from the socket or failed ping: cancel ctx, close master. Errors before start: `Close(1011, err.Error())` cut to 123 bytes.

- [ ] **Step 5: Wire** `dashboard_serve.go`: `Run: e.run, Env: <the same closure jobs.New gets>`.
- [ ] **Step 6: Run** `go test ./internal/web/ ./internal/cli/` — PASS; `make check` — PASS.
- [ ] **Step 7: Commit** `dashboard: terminals over a WebSocket on a pseudo-terminal (D149)`.

### Task 4: The browser terminal and the worktree page

**Files:**
- Create: `internal/web/static/term/vendor.sh`, `xterm.js`, `xterm.css`, `addon-fit.js`, `addon-webgl.js`, `xterm.LICENSE.txt`, `term.js`, `term.css`
- Modify: `internal/web/templates/layout.tmpl` (or the page template holding `#detail`: a `<div id="terms" class="terms"></div>` after it), `internal/web/templates/detail.tmpl` (a "Terminal" row), `internal/web/view.go` (`TermURLs`), `internal/web/static/app.js` (click handler, `beforeunload`), `internal/web/static/app.css` (dock)
- Test: `internal/web/view_test.go`

**Interfaces:**
- Consumes: the route and wire protocol of Task 3.
- Produces: ES module `/static/term/term.js` exporting `openTerminal(el: HTMLElement, url: string, opts?: {theme?: object}) → {close(): void, focus(): void}`; it loads the UMD files once (script tags, `'self'`) and `/static/term/xterm.css`.

`vendor.sh` downloads the three tarballs, checks these sha256s and copies the files:
`xterm.js 1f991ac3b4b283ebf96e60ae23a00a52765dd3a2e46fa6fdda9f1aab032f7495`, `xterm.css ba8e6985669488981ccf40c0cefe3aba80722cb6c92de7ad628b0bd717faf2b6`, `addon-fit.js bdaefa370b1bfc42ee88d46fe6072400902a4d4b2d45cd93438dda9b23c97089`, `addon-webgl.js 9ffa9ac3ff6d47d4e6216ed1972ca8e0b5336cef744f50ebdc3f67b0ed727cdb`.

- [ ] **Step 1: Vendor** — run `vendor.sh`; commit the files with it.

- [ ] **Step 2: Write the failing view test** `TestDetailTerminals`: the ftest detail for `b` (agent claude, service `nvim` present in the fixture or added) renders buttons with `data-term="/p/ftest/b/tty/agent"`, `…/tty/nvim`, `…/tty/shell`; a worktree without an nvim service has no nvim button; the page has `id="terms"` and it is not inside `id="detail"`.

- [ ] **Step 3: Run** `go test ./internal/web/ -run TestDetailTerminals` — FAIL.

- [ ] **Step 4: Implement the view**: `detailView.Terms []actionView` (labels `agent`, `nvim`, `shell`; agent when `wt.Agent != nil`, nvim when a service `nvim` exists, shell always); template row `<div class="lbl">Terminal</div><div class="actions">{{range .Terms}}<button class="ln" data-term="{{.URL}}">{{.Label}} ▣</button>{{end}}</div>`; `#terms` in the layout after the detail.

- [ ] **Step 5: Implement `term.js`**:
  - `ws://${location.host}${url}?cols=&rows=` (sizes from `fit.proposeDimensions()`), `binaryType = 'arraybuffer'`;
  - theme from CSS variables when present (`--bg`, `--fg`, `--accent`… whatever `theme.css` defines), else xterm defaults; `opts.theme` overrides;
  - `open()`, then `loadAddon(new WebglAddon())` in try/catch → on failure or `onContextLoss`: overlay "This terminal needs WebGL", close socket;
  - input ignored until `open`; `onData` → `TextEncoder` → send in ≤ 16 KiB slices;
  - `ResizeObserver` → `fit.fit()` → debounced (100 ms) `{"cols","rows"}` when changed;
  - messages: ArrayBuffer → `term.write(new Uint8Array(…))`; text `{"exit":N}` noted;
  - close: overlay text `[exited N]` + **Reopen**, or the close reason for 4000–4999/1011, else `Disconnected` + **Reconnect** (re-runs the same open in the same element); "Connecting…" overlay until open.
  - Overlay via DOM nodes and `textContent`, classes from `term.css`.

- [ ] **Step 6: Implement the dock in `app.js`**: delegated click on `[data-term]` → `import('/static/term/term.js')` → a panel `<section class="term-panel">` with a title (`<slug> · <label>`), **Fullscreen** (`requestFullscreen()` then `navigator.keyboard?.lock?.()`), **✕** (calls `close()` and removes the panel); appended to `#terms`; `beforeunload` sets `returnValue` while `#terms` has a panel. `app.css`: panels stack in the dock, each resizable (`resize: vertical`, min height 12rem).

- [ ] **Step 7: Run** `go test ./internal/web/` — PASS; `make check` — PASS.

- [ ] **Step 8: Manual check** (`make build`, restart the dashboard with the new binary through `railswitch dashboard`; Chromium via Chrome DevTools tools): open each kind; slash suggestions appear as typed; `/plugin` menu works with arrows/Enter/Esc; nvim draws and resizes with the panel; shell runs `stty size` matching the panel; paste of 200 KiB into `cat > /dev/null` keeps the connection; console shows no CSP errors other than at most two at open; Ctrl-W asks before closing.

- [ ] **Step 9: Commit** `dashboard: xterm.js terminals on the worktree page (D149)`.

### Task 5: The office

**Files:**
- Modify: `internal/web/static/cyberpunk/cyberpunk.js` (`showDesk`, `showAgent`, the key handler), `internal/web/static/cyberpunk/cyberpunk.css` (terminal size in the panel)

**Interfaces:**
- Consumes: `openTerminal` from `/static/term/term.js` (static `import`; `cyberpunk.js` is a module).

- [ ] **Step 1: `showAgent`**: replace the `pre.screen`, the textarea and the Send/Enter-only buttons with a `div.term` filled by `openTerminal(div, \`${base}/${slug}/tty/agent?agent=${name}\`, {theme: <the office's cyan-on-black>})`; the agent select reopens it for the new name; keep the status line and **Open console ▣**; the panel's cleanup closes the terminal. A floor without a service agent still gets a terminal (`railswitch agent` runs an on-demand agent tied to it), with the note that it ends when the panel closes.
- [ ] **Step 2: `showDesk`**: **▣ Shell** and **✎ Neovim** open a terminal in the panel (nvim only when the floor has an `nvim` service); the existing window buttons stay, relabelled "… window".
- [ ] **Step 3: Key handler**: return early when `e.target.closest?.('.xterm')`; Escape inside a terminal goes to the terminal, not `closePanel`.
- [ ] **Step 4: Manual check**: the office agent console shows live suggestions and `/plugin`; walking keys don't type into the terminal unless it has focus, and typing in it doesn't walk; closing or switching panels ends the shell.
- [ ] **Step 5: Run** `make check` — PASS. **Commit** `office: live terminals for the agent, nvim and a shell (D149)`.

### Task 6: Docs

**Files:**
- Modify: `docs/superpowers/plans/2026-10-25-railswitch-roadmap.md` (D149 row), `docs/dashboard.md`, `docs/guide/agents.md`, `AGENTS.md`

- [ ] **Step 1:** D149 row summarising the spec's decisions (shell in the app container, WebSocket via `coder/websocket` and why, WebGL renderer for the CSP, limits, close codes, Origin rule).
- [ ] **Step 2:** `docs/dashboard.md`: a "Terminals" section (where the buttons are, what each kind runs, what closing does per kind, browser-reserved keys and Fullscreen, the Origin rule and limits). `docs/guide/agents.md`: the agent in the browser. `AGENTS.md`: dependency line; a Stage line for `GET /p/{prefix}/{slug}/tty/{kind}`; `internal/pty` in the package map.
- [ ] **Step 3:** `make check` — PASS. **Commit** `docs: dashboard terminals (D149)`.
