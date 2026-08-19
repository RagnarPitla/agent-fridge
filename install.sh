#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Agent Fridge installer for macOS and Linux.
#
# Downloads one static binary, verifies its checksum, and puts it on your PATH.
# No runtime, no package manager, no post-install script, nothing written into
# your repository. Read it before you pipe it to a shell; it is 90 lines.
#
#   curl -fsSL .../install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- --version v0.2.0 --dir ~/bin

set -eu

REPO="RagnarPitla/agent-fridge"
BIN="fridge"
VERSION="latest"
DIR=""

while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    --dir) DIR="$2"; shift 2 ;;
    -h|--help)
      echo "usage: install.sh [--version <tag>] [--dir <install dir>]"
      exit 0 ;;
    *) echo "install.sh: unknown option $1" >&2; exit 2 ;;
  esac
done

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  darwin|linux) ;;
  *) echo "install.sh: unsupported OS '$os'. Windows users: use install.ps1." >&2; exit 1 ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) echo "install.sh: unsupported architecture '$arch'." >&2
     echo "  Build from source instead: go install github.com/$REPO/cmd/$BIN@latest" >&2
     exit 1 ;;
esac

asset="${BIN}_${os}_${arch}"
if [ "$VERSION" = "latest" ]; then
  base="https://github.com/$REPO/releases/latest/download"
else
  base="https://github.com/$REPO/releases/download/$VERSION"
fi

if [ -z "$DIR" ]; then
  if [ -w "/usr/local/bin" ] 2>/dev/null; then DIR="/usr/local/bin"; else DIR="$HOME/.local/bin"; fi
fi
mkdir -p "$DIR"

tmp=$(mktemp -d "${TMPDIR:-/tmp}/agent-fridge.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT TERM

fetch() {
  if command -v curl >/dev/null 2>&1; then curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then wget -qO "$2" "$1"
  else echo "install.sh: need curl or wget." >&2; exit 1
  fi
}

echo "Agent Fridge: downloading $asset ($VERSION)"
fetch "$base/$asset" "$tmp/$BIN" || {
  echo "install.sh: download failed. Check that a release exists at" >&2
  echo "  https://github.com/$REPO/releases" >&2
  exit 1
}

# Checksum verification is best effort: if the .sha256 is missing we say so
# loudly rather than pretending we verified something.
if fetch "$base/$asset.sha256" "$tmp/$BIN.sha256" 2>/dev/null; then
  want=$(cut -d' ' -f1 < "$tmp/$BIN.sha256" | tr -d '\r\n')
  if command -v shasum >/dev/null 2>&1; then got=$(shasum -a 256 "$tmp/$BIN" | cut -d' ' -f1)
  elif command -v sha256sum >/dev/null 2>&1; then got=$(sha256sum "$tmp/$BIN" | cut -d' ' -f1)
  else got=""; echo "install.sh: no sha256 tool found, skipping verification" >&2
  fi
  if [ -n "$got" ] && [ "$got" != "$want" ]; then
    echo "install.sh: CHECKSUM MISMATCH. Refusing to install." >&2
    echo "  expected $want" >&2
    echo "  got      $got" >&2
    exit 1
  fi
  [ -n "$got" ] && echo "Agent Fridge: checksum verified"
else
  echo "install.sh: no published checksum for $asset, skipping verification" >&2
fi

chmod +x "$tmp/$BIN"
mv "$tmp/$BIN" "$DIR/$BIN"

echo "Agent Fridge: installed to $DIR/$BIN"
"$DIR/$BIN" version || true

case ":$PATH:" in
  *":$DIR:"*) ;;
  *) echo ""
     echo "  $DIR is not on your PATH. Add this to your shell profile:"
     echo "    export PATH=\"$DIR:\$PATH\"" ;;
esac

echo ""
echo "  Next:  cd your-repo && $BIN init"
echo "  Check: $BIN conform"
