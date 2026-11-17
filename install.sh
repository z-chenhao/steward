#!/bin/sh
# Install from a checkout or an unpacked release. No sudo, shell-profile edits,
# provider credentials, or user data writes. Dependencies are lockfile-pinned.
set -eu

source_root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
prefix=${HOME:?}/.local
if [ "${1:-}" = "--prefix" ] && [ "$#" = 2 ]; then
  prefix=$2
elif [ "$#" != 0 ]; then
  echo 'Usage: sh install.sh [--prefix /absolute/directory]' >&2
  exit 1
fi
case "$prefix" in /*) ;; *) echo 'Installation prefix must be absolute.' >&2; exit 1 ;; esac
install_root=$prefix/lib/steward
launcher=$prefix/bin/steward

if [ "$(node --version 2>/dev/null || true)" != v24.21.0 ]; then
  echo 'Install Node.js 24.21.0, then rerun this installer: https://nodejs.org/en/download' >&2
  exit 1
fi
if [ -e "$install_root" ] && { [ -L "$install_root" ] || [ ! -f "$install_root/.steward-install" ]; }; then
  echo 'Refusing to overwrite an unmanaged installation directory.' >&2
  exit 1
fi
if [ -e "$launcher" ] || [ -L "$launcher" ]; then
  if [ ! -L "$launcher" ] || [ "$(readlink "$launcher")" != "$install_root/current/bin/steward" ]; then
    echo 'Refusing to replace an existing steward executable.' >&2
    exit 1
  fi
fi
if [ -e "$install_root/current" ] && [ ! -L "$install_root/current" ]; then
  echo 'Refusing to replace an unmanaged current directory.' >&2
  exit 1
fi
mkdir -p "$prefix/bin" "$install_root/releases"
chmod 700 "$install_root" "$install_root/releases"
touch "$install_root/.steward-install"
stage=$(mktemp -d "$install_root/releases/.install.XXXXXX")
trap 'rm -rf "$stage"' EXIT
trap 'exit 1' HUP INT TERM
mkdir -p "$stage/bin" "$stage/provider-bridge" "$stage/runtime/javascript"

if [ -f "$source_root/go.mod" ]; then
  command -v go >/dev/null || { echo 'Source installation needs Go (see go.mod), or use a prebuilt release.' >&2; exit 1; }
  command -v npm >/dev/null || { echo 'Source installation needs npm.' >&2; exit 1; }
  command -v python3 >/dev/null || { echo 'Source installation needs Python 3 for dependency notices.' >&2; exit 1; }
  echo 'Building Steward…'
  (cd "$source_root" && go build -o "$stage/bin/steward" ./cmd/steward)
  cp "$source_root"/provider-bridge/*.mjs "$source_root"/provider-bridge/package*.json "$stage/provider-bridge/"
  cp -R "$source_root/runtime/javascript/src" "$stage/runtime/javascript/"
  cp "$source_root"/runtime/javascript/package*.json "$stage/runtime/javascript/"
  (cd "$stage/provider-bridge" && npm ci --omit=dev --ignore-scripts --no-audit --no-fund)
  (cd "$stage/runtime/javascript" && npm ci --omit=dev --ignore-scripts --no-audit --no-fund)
  sh "$source_root/scripts/build-plugins.sh" "$stage/bin/steward" "$stage/bin/plugins" "$stage/runtime/javascript/node_modules"
  python3 "$source_root/scripts/licenses.py" "$stage/licenses"
else
  cp "$source_root/bin/steward" "$stage/bin/steward"
  cp -R "$source_root/bin/plugins" "$stage/bin/"
  cp -R "$source_root/provider-bridge/." "$stage/provider-bridge/"
  cp -R "$source_root/runtime/javascript/." "$stage/runtime/javascript/"
  cp -R "$source_root/licenses" "$stage/"
fi
cp "$source_root/LICENSE" "$source_root/NOTICE" "$source_root/THIRD_PARTY_NOTICES.md" "$stage/"
"$stage/bin/steward" doctor
release=$install_root/releases/$(date -u +%Y%m%dT%H%M%SZ)-$$
mv "$stage" "$release"
ln -sfn "$release" "$install_root/current"
ln -sfn "$install_root/current/bin/steward" "$launcher"
echo "Installed. Start Steward: $launcher"
case ":$PATH:" in
  *":$prefix/bin:"*) echo 'You can also run: steward' ;;
  *) echo "To use the short command, add $prefix/bin to your PATH." ;;
esac
