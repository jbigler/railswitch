#!/bin/sh
# Rebuilds three.js: three, its GLTFLoader and the bloom post-processing
# passes as one minified ES module (the page's CSP allows no import map, so
# bare "three" imports can't resolve in the browser). Run from this
# directory; needs npm and network.
set -eu
v=0.186.0
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -sfL "https://registry.npmjs.org/three/-/three-$v.tgz" | tar -xz -C "$tmp"
p="$tmp/package"
j="$p/examples/jsm"
cat > "$tmp/entry.js" <<JS
export * from '$p/build/three.module.js';
export { GLTFLoader } from '$j/loaders/GLTFLoader.js';
export { EffectComposer } from '$j/postprocessing/EffectComposer.js';
export { RenderPass } from '$j/postprocessing/RenderPass.js';
export { UnrealBloomPass } from '$j/postprocessing/UnrealBloomPass.js';
export { OutputPass } from '$j/postprocessing/OutputPass.js';
JS
npx -y esbuild@0.25.12 "$tmp/entry.js" --bundle --minify --format=esm --legal-comments=none \
  --alias:three="$p/build/three.module.js" --outfile=three.js
cp "$p/LICENSE" three.LICENSE.txt
