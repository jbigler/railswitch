// The whiteboard (D151): a floor's board opens in Excalidraw over the
// office; saving keeps the scene and a picture of it, which the board then
// shows. Both live with the worktree (railswitch whiteboard), so the
// drawing stays until wt rm. Excalidraw is vendored in /static/whiteboard/
// (tools/whiteboard/build.sh) and loaded on first use. The dashboard's own
// editor page (board.js, D218) loads it through here too.
const ASSETS = '/static/whiteboard/';
let lib = null;

export function load() {
  if (!lib) {
    window.EXCALIDRAW_ASSET_PATH = new URL(ASSETS, location.href).href; // its fonts, from here (an absolute URL, or it falls back to a CDN)
    const css = document.createElement('link');
    css.rel = 'stylesheet';
    css.href = ASSETS + 'excalidraw.css';
    document.head.append(css);
    lib = import(ASSETS + 'excalidraw.js').catch((e) => { lib = null; throw e; });
  }
  return lib;
}

const b64 = async (blob) => {
  const bytes = new Uint8Array(await blob.arrayBuffer());
  let s = '';
  for (let i = 0; i < bytes.length; i += 0x8000) s += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  return btoa(s);
};

// boardBody is what saving editor ed sends ({scene, png, png_light} as
// JSON), or null for an empty board, which a save clears; png is the dark
// picture's base64 ("" when cleared), for the office's glass board.
export async function boardBody(ed) {
  const { scene, png, light } = await ed.save();
  if (!scene.elements.length) return { body: null, png: '' };
  const pic = await b64(png);
  return { body: JSON.stringify({ scene, png: pic, png_light: await b64(light) }), png: pic };
}

// picture decodes a board's base64 PNG for the 3D board ("" → null), with
// no URL: the page's CSP allows images from 'self' only.
export async function picture(png) {
  if (!png) return null;
  const bytes = Uint8Array.from(atob(png), (c) => c.charCodeAt(0));
  return createImageBitmap(new Blob([bytes], { type: 'image/png' }));
}

// editBoard opens the editor on doc ({hash, scene}) full window. save(body)
// stores a drawing ({scene, png} as JSON; null clears the board) and returns
// the new hash; onSaved(png) gets the saved picture's base64 ("": cleared).
export async function editBoard(title, doc, save, onSaved) {
  document.exitPointerLock?.();
  const host = document.createElement('div');
  host.className = 'board-host';
  const status = document.createElement('span');
  status.className = 'board-status';
  const mk = (text, cls) => {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'hud-btn' + (cls ? ' ' + cls : '');
    b.textContent = text;
    return b;
  };
  const done = mk('Save and close', 'primary');
  const cancel = mk('Discard changes');
  const head = document.createElement('header');
  const name = document.createElement('span');
  name.textContent = title;
  head.append(name, status, cancel, done);
  const modal = document.createElement('div');
  modal.className = 'board-modal';
  modal.append(head, host);
  document.body.append(modal);
  status.textContent = 'loading…';
  let ed;
  try {
    ed = (await load()).open(host, doc.scene);
    status.textContent = '';
  } catch (e) {
    status.textContent = `the editor did not load: ${e.message}`;
  }
  return new Promise((resolve) => {
    const close = () => { ed?.close(); modal.remove(); resolve(); };
    cancel.addEventListener('click', close);
    done.addEventListener('click', async () => {
      if (!ed) return close();
      done.disabled = cancel.disabled = true;
      status.textContent = 'saving…';
      try {
        const { body, png } = await boardBody(ed);
        doc.hash = await save(body);
        onSaved(png);
        close();
      } catch (e) {
        status.textContent = `not saved: ${e.message}`;
        done.disabled = cancel.disabled = false;
      }
    });
  });
}
