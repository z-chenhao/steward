#!/bin/sh
# Native release packaging; CI runs this on each supported OS/architecture.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
output=${1:?Usage: sh scripts/package.sh /absolute/output-directory}
case "$output" in /*) ;; *) echo 'Output must be absolute.' >&2; exit 1 ;; esac
mkdir -p "$output"
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
trap 'exit 1' HUP INT TERM
# The same installer owns compilation and pinned runtime dependencies.
sh "$root/install.sh" --prefix "$stage/install"
bundle=$stage/steward
mkdir "$bundle"
cp -R "$stage/install/lib/steward/current/." "$bundle/"
cp "$root/install.sh" "$bundle/"
cat > "$bundle/README.md" <<'EOF'
# Steward native package

Install Node.js 24.21.0, then run `sh install.sh` in this directory.
Start Steward with `~/.local/bin/steward`. First launch guides model setup.
Type `/quit` or press Ctrl-C to stop. No daemon is installed.

Your data stays in `~/.local/share/steward`; reinstalling keeps it.
Use `sh install.sh --prefix /absolute/path` for another installation location.

Documentation: https://github.com/z-chenhao/steward/blob/main/docs/README.md
EOF
platform=$(go env GOOS)
arch=$(go env GOARCH)
# AppleDouble metadata is neither executable code nor a declared plugin file.
# Keep it out of the archive rather than weakening package inventory checks.
COPYFILE_DISABLE=1 tar -czf "$output/steward-$platform-$arch.tar.gz" -C "$stage" steward
(
  cd "$output"
  if command -v sha256sum >/dev/null; then
    sha256sum "steward-$platform-$arch.tar.gz" > "steward-$platform-$arch.tar.gz.sha256"
  else
    shasum -a 256 "steward-$platform-$arch.tar.gz" > "steward-$platform-$arch.tar.gz.sha256"
  fi
)
echo "$output/steward-$platform-$arch.tar.gz"
