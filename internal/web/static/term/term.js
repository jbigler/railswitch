// Terminals (D149): an xterm.js terminal over the dashboard's WebSocket
// (GET /p/{prefix}/tty/{slug}/{kind}). Binary frames carry keys and output;
// a text frame {"cols","rows"} resizes; the server's {"exit":N} comes
// before a normal close. Rendered on a canvas (WebGL as the fallback):
// xterm's DOM renderer adds <style> elements, which the dashboard's CSP blocks.

const sendMax = 16 << 10; // the server reads at most 64 KiB per frame

// FONT prefers Maple Mono (its Nerd Font builds first: nvim's icons, prompt
// glyphs), then JetBrains Mono's, then the system's monospace: the first one
// installed wins.
const FONT = '"Maple Mono NF", "Maple Mono NF CN", "Maple Mono", "JetBrainsMono Nerd Font", "JetBrainsMono NF", "JetBrains Mono", ui-monospace, Menlo, monospace';
const FONT_SIZE = 14;

// open counts live terminals: while any is, leaving the page asks first
// (Ctrl-W, delete-word in a shell, closes the tab before the terminal
// sees it). Both the dashboard and the office get it from here.
let open = 0;
addEventListener('beforeunload', (e) => {
  if (open > 0) e.preventDefault();
});

let loaded = null;
function script(src) {
  return new Promise((ok, fail) => {
    const s = document.createElement('script');
    s.src = src;
    s.onload = ok;
    s.onerror = () => fail(new Error(`could not load ${src}`));
    document.head.append(s);
  });
}
// load brings in xterm.js, its addons and stylesheet once.
function load() {
  if (!loaded) {
    for (const href of ['/static/term/xterm.css', '/static/term/term.css']) {
      const css = document.createElement('link');
      css.rel = 'stylesheet';
      css.href = href;
      document.head.append(css);
    }
    // the font first: the terminal measures its cell once, when it opens
    const font = document.fonts?.load(`${FONT_SIZE}px ${FONT}`).catch(() => {}) ?? Promise.resolve();
    loaded = Promise.all([font, script('/static/term/xterm.js')]).then(() => Promise.all([script('/static/term/addon-fit.js'), script('/static/term/addon-canvas.js'), script('/static/term/addon-webgl.js')]));
  }
  return loaded;
}

// cssTheme is the dashboard's colours (theme.css), where the page has them.
function cssTheme(el) {
  const cs = getComputedStyle(el);
  const v = (n) => cs.getPropertyValue(n).trim() || undefined;
  return {
    background: v('--bg'), foreground: v('--text'), cursor: v('--accent'), cursorAccent: v('--bg'),
    selectionBackground: v('--sel'), red: v('--red'), green: v('--green'), yellow: v('--yellow'),
    blue: v('--blue'), magenta: v('--purple'), cyan: v('--accent'),
  };
}

// shade is 'light' or 'dark' for a #rrggbb background, by the luminance
// weights Neovim (and docs/nvim.md's hook) use; undefined otherwise.
function shade(hex) {
  const m = /^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i.exec(hex || '');
  if (!m) return undefined;
  const [r, g, b] = m.slice(1).map((h) => parseInt(h, 16) / 255);
  return 0.299 * r + 0.587 * g + 0.114 * b < 0.5 ? 'dark' : 'light';
}

function tag(name, cls, text) {
  const e = document.createElement(name);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

// openTerminal fills el with a terminal connected to url (a path).
// opts.theme overrides colours. Returns {close, focus}.
export function openTerminal(el, url, opts = {}) {
  open++;
  el.classList.add('term-host');
  const screen = tag('div', 'term-screen');
  const overlay = tag('div', 'term-overlay');
  el.replaceChildren(screen, overlay);
  let term = null;
  let fit = null;
  let sock = null;
  let closed = false; // close() was called: no overlay, no reconnect
  let observer = null;

  const show = (text, again) => {
    overlay.replaceChildren(tag('span', '', text));
    if (again) {
      const b = tag('button', 'ln', again);
      b.type = 'button';
      b.addEventListener('click', connect);
      overlay.append(' ', b);
    }
    overlay.hidden = false;
  };

  function connect() {
    if (closed) return;
    show('Connecting…');
    const dims = fit.proposeDimensions() || { cols: 80, rows: 24 };
    const cols = Math.max(1, Math.min(1000, dims.cols || 80));
    const rows = Math.max(1, Math.min(1000, dims.rows || 24));
    term.resize(cols, rows);
    const q = new URL(url, location.href);
    q.protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
    q.searchParams.set('cols', cols);
    q.searchParams.set('rows', rows);
    const bg = shade(term.options.theme.background);
    if (bg) q.searchParams.set('bg', bg); // the panel's light or dark, for nvim's hook
    const ws = new WebSocket(q);
    ws.binaryType = 'arraybuffer';
    sock = ws;
    let exit = null;
    ws.onopen = () => {
      overlay.hidden = true;
      term.focus();
    };
    ws.onmessage = (ev) => {
      if (typeof ev.data === 'string') {
        try { exit = JSON.parse(ev.data).exit; } catch { /* not ours */ }
        return;
      }
      term.write(new Uint8Array(ev.data));
    };
    ws.onclose = (ev) => {
      if (sock !== ws) return;
      sock = null;
      if (closed) return;
      if (exit !== null && exit !== undefined) show(`[exited ${exit}]`, 'Reopen');
      else if ((ev.code >= 4000 && ev.code < 5000) || ev.code === 1011) show(ev.reason || `closed (${ev.code})`, 'Retry');
      else show('Disconnected', 'Reconnect');
    };
  }

  const enc = new TextEncoder();
  const sendBytes = (b) => {
    if (!sock || sock.readyState !== WebSocket.OPEN) return; // nothing typed before the open
    for (let i = 0; i < b.length; i += sendMax) sock.send(b.subarray(i, i + sendMax));
  };
  const send = (data) => sendBytes(enc.encode(data));
  let sized = '';
  let timer = 0;
  const resized = () => {
    clearTimeout(timer);
    timer = setTimeout(() => {
      if (!term || closed) return;
      fit.fit();
      const s = JSON.stringify({ cols: term.cols, rows: term.rows });
      if (s !== sized && sock?.readyState === WebSocket.OPEN) {
        sized = s;
        sock.send(s);
      }
    }, 100);
  };

  load().then(() => {
    if (closed) return;
    term = new window.Terminal({
      cursorBlink: true, scrollback: 5000, allowProposedApi: false,
      fontFamily: FONT, fontSize: FONT_SIZE,
      theme: { ...cssTheme(el), ...(opts.theme || {}) },
    });
    fit = new window.FitAddon.FitAddon();
    term.loadAddon(fit);
    term.open(screen);
    // the canvas renderer: the browser's own text drawing, crisp in Firefox
    // too (WebGL's glyph atlas comes out soft there); WebGL if it fails
    try {
      term.loadAddon(new window.CanvasAddon.CanvasAddon());
    } catch {
      try {
        const gl = new window.WebglAddon.WebglAddon();
        gl.onContextLoss(() => { show('This terminal lost its WebGL context'); sock?.close(); });
        term.loadAddon(gl);
      } catch {
        show('This terminal needs a canvas or WebGL, which this browser has turned off.');
        return;
      }
    }
    fit.fit();
    term.onData(send);
    term.onBinary((d) => sendBytes(Uint8Array.from(d, (c) => c.charCodeAt(0) & 0xff))); // mouse reports: one char per byte
    observer = new ResizeObserver(resized);
    observer.observe(el);
    connect();
  }, (e) => show(e.message));

  return {
    close() {
      if (closed) return;
      closed = true;
      open--;
      clearTimeout(timer);
      observer?.disconnect();
      sock?.close(1000);
      term?.dispose();
    },
    focus() { term?.focus(); },
  };
}
