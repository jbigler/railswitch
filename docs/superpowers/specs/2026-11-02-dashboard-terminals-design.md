# Dashboard terminals: xterm.js for the agent, nvim and a shell

Design for D149, agreed with the user on 2026-11-02. Builds on the
virtual office (D144–D148) and the shared agent session (D142).

## Intent

The office's agent console is a `tmux capture-pane` snapshot polled every
1.5 s and a text box that `agent send` pastes on Enter (D144). Claude
never sees keys as they are typed, so slash suggestions never appear, and
menus (`/plugin`) can't be driven: arrows, Esc, Tab and Space never
arrive. The user wants a real terminal in the browser, connected to:

- the worktree's agent (`railswitch agent`),
- the worktree's nvim (`railswitch nvim`),
- a shell at their desk: inside the worktree's app container
  (`railswitch exec`), never on the host.

Success: slash suggestions, `/plugin` menus, nvim and the shell behave as
in a real terminal, resize and colours included.

Decisions taken with the user:

- The desk shell runs in the app container (fenced by the container and
  the firewall). A host shell would make any dashboard bug a path to
  running anything on the machine.
- Terminals appear on the worktree page and in the office, one shared
  component and one endpoint.
- A shell ends when its panel closes. The agent and nvim outlive their
  viewers already (the agent's tmux session, nvim's server).
- Transport: a WebSocket through `github.com/coder/websocket`, the second
  dependency. Reason: a WebSocket server is about 200 lines of
  framing, masking, fragmentation, ping/close and size-limit parsing that
  a hostile page can push on; the library is small, maintained and has no
  dependencies. SSE plus POST was rejected (keystrokes need sequence
  numbers, a request per keypress); a hand-written WebSocket was rejected
  (security-sensitive parser to own).
- The pseudo-terminal is standard library (`creack/pty` would save about
  40 lines on a Linux-only tool).

## Endpoint and access

`GET /p/{prefix}/{slug}/tty/{kind}?cols=<c>&rows=<r>[&agent=<name>]`,
upgraded to a WebSocket. Kinds and their argv (the dashboard's own
executable, as jobs run it):

| kind    | argv                                              |
|---------|---------------------------------------------------|
| `agent` | `railswitch -C <ws> -w <slug> agent [<name>]`     |
| `nvim`  | `railswitch -C <ws> -w <slug> nvim`               |
| `shell` | `railswitch -C <ws> -w <slug> exec -- sh -c 'command -v bash >/dev/null && exec bash -l; exec sh -l'` |

`exec` needs a command; the shell prefers bash (the Rails images have it)
and falls back to `sh` for an image without it.

Nothing from the request reaches the argv but the agent name, which must
be a row of the worktree's `agent ls` (cached overview); omitted, the
worktree's default agent. The prefix and slug are checked against the
cached overview, as actions are.

Access:

- The guard (Host, cookie) runs first, unchanged.
- **Origin.** The guard checks Origin only on unsafe methods, and an
  upgrade is a GET. The Rails app, anything the agent writes and Dozzle
  are served on `*.localhost`: the same site as `railswitch.localhost`,
  so the `SameSite=Strict` cookie is sent with their requests. The
  endpoint therefore relies on `websocket.Accept`'s own Origin check
  (Origin host must equal the request Host): no `InsecureSkipVerify`, no
  `OriginPatterns`. A request with Origin `http://app.foo.localhost` must
  be refused (tested).
- CSP stays `default-src 'self'`; CSP3 browsers match same-host `ws:` to
  `'self'`. If a supported browser doesn't, add
  `connect-src 'self' ws://railswitch.localhost:7700 ws://localhost:7700 ws://127.0.0.1:7700`.

Limits:

- At most 8 terminals open across the dashboard.
- A browser frame is at most 64 KiB (`SetReadLimit`).
- `cols` and `rows` are integers in 1..1000.
- Ping every 30 s (`conn.Ping`, pong within 10 s, else the terminal
  ends). On loopback a closed tab is seen at once; the ping catches a dead
  `ssh -L` tunnel, which would otherwise hold a process and a slot.

Secrets (D33): a terminal carries only what the user types and the
program prints. Nothing is logged; a terminal has no log file, unlike a
job.

## Pseudo-terminal and process

`internal/pty` (Linux, standard library):

- `Open() (master, slave *os.File, err error)`: `/dev/ptmx` (`O_RDWR|O_CLOEXEC`),
  `TIOCSPTLCK` 0, `TIOCGPTN`, then `/dev/pts/<n>` with
  `O_RDWR|O_NOCTTY|O_CLOEXEC`.
- `Resize(f *os.File, rows, cols int) error`: `TIOCSWINSZ` (the kernel
  sends `SIGWINCH` to the foreground process group).

`sys.Cmd` gains `TTY *os.File`: when set, stdin, stdout and stderr are
that file and the child starts with `Setsid`, `Setctty`, `Ctty: 0`, so it
leads its own session and process group. It runs through `sys.Runner`
with `sys.NoTimeout` and `KillGrace: 3s`: cancellation is SIGTERM to the
group, then SIGKILL after the grace. `systest.Fake` records the field.

No `systemd-run` scope: jobs use one so a dashboard restart doesn't kill
them; a terminal should end with its connection, which the dashboard owns.

Start: open the pseudo-terminal, `Resize` to the requested size, start
the argv with `TTY` = slave, `Env` = the jobs' environment plus
`TERM=xterm-256color` and `COLORTERM=truecolor`; the dashboard closes its
copy of the slave once the child runs.

Running, two loops:

- program → browser: read the master in 32 KiB chunks, each a binary
  frame;
- browser → program: a binary frame is written to the master as is; a
  text frame must be `{"cols":c,"rows":r}` (strict JSON, the same bounds)
  and becomes `Resize`; any other text frame closes with 4000.

End:

- The program exits: reading the master fails (`EIO`); the server sends a
  text frame `{"exit":N}` and closes with 1000. The panel shows
  `[exited N]` and **Reopen**.
- The panel closes, the ping fails, or the connection drops: the context
  is cancelled; the master closes (SIGHUP), then the group gets SIGTERM,
  then SIGKILL after 3 s. The agent and nvim lose a viewer and go on
  (the agent's tmux client is cleaned up by its exec hang-up watcher);
  the shell ends.
- Dashboard self-restart (its executable replaced while no job runs):
  open terminals don't delay it (a panel can stay open for hours). They
  end with the dashboard; panels show **Disconnected — Reconnect**. The
  agent and nvim resume where they were; the shell starts afresh.

## Browser

`internal/web/static/term/`: `@xterm/xterm` 5.5.0 (`xterm.js`,
`xterm.css`) and `@xterm/addon-fit` 0.10.0, with a `vendor.sh` pinning
versions and sha256s like `cyberpunk/vendor.sh`.

`term.js`, `openTerminal(el, url)`:

- a `Terminal` whose theme comes from the dashboard's CSS variables
  (`/theme.css`), so light/dark and `dashboard colors` apply;
- the fit add-on and a `ResizeObserver`; a size change sends a debounced
  `{"cols","rows"}`;
- `onData` → UTF-8 → binary frame; an incoming binary frame →
  `term.write`; `{"exit":N}` noted for the overlay;
- on close: an overlay with `[exited N] Reopen`, the close reason for a
  4000-range or 1011 code, else `Disconnected — Reconnect`.

Placement:

- Worktree page: **Agent ▣**, **Nvim ▣**, **Shell ▣** open a resizable
  panel docked under the worktree; several may be open.
- Office: the agent console's snapshot and text box become a live
  terminal; the desk gets **Shell** and **Nvim**. **Open console ▣**
  (a host terminal window) stays. `agent screen`/`send` stay for the
  office's read-only views.

Keyboard:

- A focused terminal receives every key; the office's key handler
  (`cyberpunk.js`) ignores events whose target is inside a terminal.
- `Ctrl-W`, `Ctrl-T`, `Ctrl-N`, `Ctrl-Tab` never reach a page. While a
  terminal is open, `beforeunload` asks before the tab closes. A
  **Fullscreen** button uses the Keyboard Lock API, which in Chromium
  browsers delivers those keys to the terminal; Firefox has only the
  prompt.

Risk checked first: xterm.js's DOM renderer may add `<style>` elements,
which `default-src 'self'` blocks. Fallbacks, in order:
`@xterm/addon-webgl` (canvas), moving the generated rules into a static
stylesheet, a `style-src` hash on the terminal pages only.

## Errors

A browser can't read the body of a refused upgrade (it sees 1006), so:

- refused before the upgrade with an HTTP error: Host, cookie, Origin
  (protection; a real page never meets them);
- accepted, then closed with a code and a reason the overlay shows:
  `4004 unknown worktree`, `4000 unknown agent <n>`, `4000 bad size`,
  `4000 bad message`, `4029 8 terminals are open: close one`,
  `1011 <reason>` (pseudo-terminal or start failure);
- railswitch's own refusals (another agent running, unset secrets, nvim
  failing) are printed by the command in the terminal before it exits;
  the panel shows `[exited 1]` with the text above it.

## Testing

Unit (no podman, systemd, gh or network):

- `internal/pty`: open, write the slave, read the master; `Resize`, then
  `TIOCGWINSZ` reads it back; skipped without `/dev/ptmx`.
- `sys` `TTY`: `sh -c 'tty; stty size; ps -o sid= -p $$'` prints a
  `/dev/pts/` path, the set size, and its own pid as session id;
  cancelling it leaves no process in the group.
- `internal/web` (`httptest`, `websocket.Dial`): Origin
  `http://app.foo.localhost` refused; no cookie refused; unknown kind,
  worktree, agent; bad size; slot limit; with a test spawn of `cat`/`sh`:
  typed text echoes, a resize changes `stty size`, the exit code arrives,
  closing the client kills the process; a short ping interval and a
  client that never reads is disconnected; each kind's argv through the
  fake runner.

Manual (no JavaScript test setup; the end-to-end run is already manual):
the CSP check in Chromium and Firefox first (console free of CSP errors),
then slash suggestions, `/plugin`, nvim with resizing, the shell, theme
colours, the `Ctrl-W` prompt, Fullscreen with keyboard lock, Reconnect
after a dashboard restart.

## Docs

- Roadmap: D149.
- `docs/dashboard.md`: the terminal panels, the endpoint and its access
  rules.
- `docs/guide/agents.md`: using the agent in the browser.
- `AGENTS.md`: `github.com/coder/websocket` on the dependency line with
  its reason; a line for the terminal panels.

## Out of scope

- A shell on the host.
- A shell that survives its panel (tmux in the app image).
- Read-only joining from the browser (`agent --watch`): later, if wanted.
- Remote access beyond what the dashboard already allows (127.0.0.1,
  `ssh -L`).
