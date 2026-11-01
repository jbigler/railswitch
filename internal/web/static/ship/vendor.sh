#!/bin/sh
# Rebuilds three.js: three and its GLTFLoader as one minified ES module
# (the page's CSP allows no import map, so bare "three" imports can't
# resolve in the browser). Run from this directory; needs npm and network.
set -eu
v=0.186.0
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -sfL "https://registry.npmjs.org/three/-/three-$v.tgz" | tar -xz -C "$tmp"
p="$tmp/package"
printf "export * from '%s/build/three.module.js';\nexport { GLTFLoader } from '%s/examples/jsm/loaders/GLTFLoader.js';\n" "$p" "$p" > "$tmp/entry.js"
npx -y esbuild@0.25.12 "$tmp/entry.js" --bundle --minify --format=esm --legal-comments=none \
  --alias:three="$p/build/three.module.js" --outfile=three.js
cp "$p/LICENSE" three.LICENSE.txt
