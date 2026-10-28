#!/usr/bin/env bash
# Builds the npm packages for the current OS/arch, packs them with `npm pack`,
# unpacks the tarballs into a throwaway node_modules exactly as npm would lay
# them out, and runs the launcher. Used by CI and handy locally:
#   scripts/npm-smoke.sh
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

goos=$(go env GOOS); goarch=$(go env GOARCH)
ext=""; [ "$goos" = windows ] && ext=".exe"
mkdir -p "$work/bin"
CGO_ENABLED=0 go build -C "$root" -ldflags "-s -w -X main.version=0.0.0-smoke" -o "$work/bin/yessir-$goos-$goarch$ext" ./cmd/yessir

node "$root/npm/build.mjs" --version 0.0.0-smoke --bin-dir "$work/bin" --out "$work/pkg"

mkdir -p "$work/tgz" "$work/proj/node_modules/@rlgdev"
for d in "$work"/pkg/*/; do
  (cd "$d" && npm pack --silent --pack-destination "$work/tgz" >/dev/null)
done
ls "$work/tgz"
for t in "$work"/tgz/*.tgz; do
  name=$(basename "$t" .tgz)            # rlgdev-yessir-0.0.0-smoke / rlgdev-yessir-linux-x64-0.0.0-smoke
  dir=${name#rlgdev-}; dir=${dir%-0.0.0-smoke}
  mkdir -p "$work/proj/node_modules/@rlgdev/$dir"
  tar -xzf "$t" -C "$work/proj/node_modules/@rlgdev/$dir" --strip-components=1
done

out=$(node "$work/proj/node_modules/@rlgdev/yessir/bin/yessir.js" version)
echo "$out"
echo "$out" | grep -q "yessir 0.0.0-smoke"

# exit codes pass through
set +e
node "$work/proj/node_modules/@rlgdev/yessir/bin/yessir.js" bogus >/dev/null 2>&1
code=$?
set -e
[ "$code" = 2 ] || { echo "expected exit 2 from unknown command, got $code"; exit 1; }
echo "npm smoke: ok"
