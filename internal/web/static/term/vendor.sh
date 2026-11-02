#!/bin/sh
# Fetches xterm.js and the two addons the dashboard's terminals use (D149),
# checking each file's sha256. Run from this directory; needs network.
# The canvas addon (WebGL as its fallback) is required: xterm's DOM renderer
# adds <style> elements, which the dashboard's CSP (default-src 'self')
# blocks. Canvas draws crisper text than WebGL in Firefox-family browsers.
set -eu
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
get() { # get <package> <version> <file in lib|css> <sha256>
  mkdir -p "$tmp/$1"
  [ -d "$tmp/$1/package" ] || curl -sfL "https://registry.npmjs.org/@xterm/$1/-/$1-$2.tgz" | tar -xz -C "$tmp/$1"
  f="$tmp/$1/package/$3"
  echo "$4  $f" | sha256sum -c --quiet
  grep -v '^//# sourceMappingURL=' "$f" > "$(basename "$3")" || true # no .map files are served
}
get xterm 5.5.0 lib/xterm.js 1f991ac3b4b283ebf96e60ae23a00a52765dd3a2e46fa6fdda9f1aab032f7495
get xterm 5.5.0 css/xterm.css ba8e6985669488981ccf40c0cefe3aba80722cb6c92de7ad628b0bd717faf2b6
get addon-fit 0.10.0 lib/addon-fit.js bdaefa370b1bfc42ee88d46fe6072400902a4d4b2d45cd93438dda9b23c97089
get addon-webgl 0.18.0 lib/addon-webgl.js 9ffa9ac3ff6d47d4e6216ed1972ca8e0b5336cef744f50ebdc3f67b0ed727cdb
get addon-canvas 0.7.0 lib/addon-canvas.js 7b3e904d5bec98b54d26674994cf994396c4af0971010ffd0d4983229b65d933
cp "$tmp/xterm/package/LICENSE" xterm.LICENSE.txt
