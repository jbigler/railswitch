// The whiteboard editor (D151, D218): Excalidraw in a given element, and
// its scene's export to PNG. Bundled by build.sh with React.
import { createElement as h } from 'react';
import { createRoot } from 'react-dom/client';
import { Excalidraw, exportToBlob, getSceneVersion, serializeAsJSON } from '@excalidraw/excalidraw';

// open mounts an editor on el with scene (null: empty), in the dark theme
// or the light one; returns { save(), dirty(), close() }. save resolves to
// {scene, png, light}: png is the drawing in dark mode, as the office's
// glass board shows it, light the same on plain white, as the dashboard's
// light theme shows it (both null for an empty board). dirty says whether
// the drawing changed since it opened.
export function open(el, scene, { dark = true } = {}) {
  let api = null;
  let first = null; // the scene's version as it opened (the first onChange)
  let now = null;
  const root = createRoot(el);
  root.render(h(Excalidraw, {
    initialData: scene ? { elements: scene.elements, appState: { ...scene.appState, collaborators: undefined }, files: scene.files, scrollToContent: true } : null,
    excalidrawAPI: (a) => { api = a; },
    onChange: (elements) => {
      now = getSceneVersion(elements);
      if (first === null) first = now;
    },
    theme: dark ? 'dark' : 'light',
    UIOptions: { canvasActions: { loadScene: false, saveToActiveFile: false, export: false, saveAsImage: false } },
  }));
  return {
    async save() {
      const elements = api.getSceneElements();
      const appState = api.getAppState();
      const files = api.getFiles();
      const json = serializeAsJSON(elements, appState, files, 'local');
      const pic = (state) => exportToBlob({ elements, appState: { ...appState, exportBackground: true, ...state }, files, mimeType: 'image/png', maxWidthOrHeight: 2048 });
      if (!elements.length) return { scene: JSON.parse(json), png: null, light: null };
      const png = await pic({ exportWithDarkMode: true });
      const light = await pic({ exportWithDarkMode: false, viewBackgroundColor: '#ffffff' });
      return { scene: JSON.parse(json), png, light };
    },
    dirty() { return first !== null && now !== first; },
    close() { root.unmount(); },
  };
}
