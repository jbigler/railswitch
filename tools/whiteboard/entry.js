// The whiteboard editor the office opens (D151): Excalidraw in a given
// element, and its scene's export to PNG. Bundled by build.sh with React.
import { createElement as h, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { Excalidraw, exportToBlob, serializeAsJSON } from '@excalidraw/excalidraw';

// open mounts an editor on el with scene (null: empty); returns
// { save(): Promise<{scene, png}>, close() }: png is the drawing in dark
// mode, as the office's glass board shows it (null for an empty board).
export function open(el, scene, { dark = true } = {}) {
  let api = null;
  const root = createRoot(el);
  root.render(h(Excalidraw, {
    initialData: scene ? { elements: scene.elements, appState: { ...scene.appState, collaborators: undefined }, files: scene.files, scrollToContent: true } : null,
    excalidrawAPI: (a) => { api = a; },
    theme: dark ? 'dark' : 'light',
    UIOptions: { canvasActions: { loadScene: false, saveToActiveFile: false, export: false, saveAsImage: false } },
  }));
  return {
    async save() {
      const elements = api.getSceneElements();
      const appState = api.getAppState();
      const files = api.getFiles();
      const json = serializeAsJSON(elements, appState, files, 'local');
      const png = elements.length ? await exportToBlob({ elements, appState: { ...appState, exportBackground: true, exportWithDarkMode: true }, files, mimeType: 'image/png', maxWidthOrHeight: 2048 }) : null;
      return { scene: JSON.parse(json), png };
    },
    close() { root.unmount(); },
  };
}
