#!/bin/sh
# Builds the dashboard's whiteboard editor (D151) into
# internal/web/static/whiteboard/: Excalidraw and React, bundled by esbuild
# from the packages package-lock.json pins (each by its integrity hash),
# with Excalidraw's stylesheet and fonts. Run here; needs node and network.
# Left out: Mermaid import (megabytes, for one dialog: no-mermaid.js) and
# the Xiaolai CJK font (13 MB): such text draws in a system font.
set -eu
out=../../internal/web/static/whiteboard
pkg=node_modules/@excalidraw/excalidraw/dist/prod
npm ci --no-audit --no-fund --ignore-scripts
rm -rf "$out"
mkdir -p "$out"
npx esbuild entry.js --bundle --format=esm --splitting --minify --target=es2022 \
  --outdir="$out" --entry-names=excalidraw --chunk-names=chunk-[hash] \
  --define:process.env.NODE_ENV='"production"' --legal-comments=external \
  --alias:@excalidraw/mermaid-to-excalidraw=./no-mermaid.js
cp "$pkg/index.css" "$out/excalidraw.css"
mkdir -p "$out/fonts"
for d in "$pkg"/fonts/*/; do
  case "$d" in */Xiaolai/) continue ;; esac
  cp -r "$d" "$out/fonts/"
done
# The package ships no LICENSE file; its package.json says MIT.
cat > "$out/excalidraw.LICENSE.txt" <<'LICENSE'
Excalidraw (https://github.com/excalidraw/excalidraw), MIT License

Copyright (c) 2020 Excalidraw

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
LICENSE
