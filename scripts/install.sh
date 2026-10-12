#!/usr/bin/env bash
set -euo pipefail
repo="Ristellise/slash-tp"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$os" in
  linux | darwin) ;;
  mingw* | msys* | cygwin*)
    echo "tp: windows detected — download a binary from https://github.com/$repo/releases"
    exit 1
    ;;
  *)
    echo "tp: unsupported OS: $os"
    exit 1
    ;;
esac
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *)
    echo "tp: unsupported architecture: $arch"
    exit 1
    ;;
esac

asset="tp-${os}-${arch}"
base="https://github.com/$repo/releases/latest/download"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL -o "$tmp/tp" "$base/$asset" || {
  echo "tp: no $asset in the latest release of $repo — is a release published? (git tag v*)"
  exit 1
}
sum=$(curl -fsSL "$base/checksums.txt" | grep " ${asset}\$" | awk '{print $1}') || true
if [ -n "$sum" ]; then
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$tmp" && echo "$sum  tp" | sha256sum -c -)
  else
    (cd "$tmp" && echo "$sum  tp" | shasum -a 256 -c -)
  fi
fi

bindir=${BINDIR:-}
if [ -z "$bindir" ]; then
  if [ -w /usr/local/bin ]; then
    bindir=/usr/local/bin
  else
    bindir=$HOME/.local/bin
    mkdir -p "$bindir"
  fi
fi
install -m 755 "$tmp/tp" "$bindir/tp"
echo "installed: $bindir/tp ($("$bindir/tp" version))"
case ":$PATH:" in
  *":$bindir:"*) ;;
  *) echo "note: $bindir is not in your PATH" ;;
esac
