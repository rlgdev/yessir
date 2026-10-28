#!/bin/sh
# Installs the latest yessir release into ~/.yessir/bin.
#   curl -fsSL https://raw.githubusercontent.com/rlgdev/yessir/main/scripts/install.sh | sh
set -eu
repo="rlgdev/yessir"
bin="$HOME/.yessir/bin"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  darwin|linux) ;;
  *) echo "yessir: unsupported OS $os (use the PowerShell installer on Windows)" >&2; exit 1 ;;
esac
arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "yessir: unsupported arch $arch" >&2; exit 1 ;;
esac
asset="yessir_${os}_${arch}.tar.gz"

echo "yessir: looking up latest release of $repo ..."
tag=$(curl -fsSL -H 'User-Agent: yessir-installer' "https://api.github.com/repos/$repo/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)
[ -n "$tag" ] || { echo "yessir: could not determine latest release" >&2; exit 1; }
url="https://github.com/$repo/releases/download/$tag/$asset"

mkdir -p "$bin"
tmp=$(mktemp -d)
echo "yessir: downloading $tag ($asset) ..."
curl -fsSL -H 'User-Agent: yessir-installer' -o "$tmp/$asset" "$url"
tar -xzf "$tmp/$asset" -C "$tmp"
mv "$tmp/yessir" "$bin/yessir"
chmod +x "$bin/yessir"
rm -rf "$tmp"

"$bin/yessir" version
case ":$PATH:" in
  *":$bin:"*) ;;
  *) echo ""; echo "add to your shell profile:  export PATH=\"\$HOME/.yessir/bin:\$PATH\"" ;;
esac
echo ""
echo "Next:"
echo "  yessir doctor          # can it work on this machine?"
echo "  yessir on --global     # or: cd <repo>; yessir on"
echo "  then start a new claude session"
