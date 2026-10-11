#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
source scripts/product.sh
for tool in go pkg-config; do
  command -v "$tool" >/dev/null || { echo "Missing development tool: $tool" >&2; exit 1; }
done
pkg-config --exists libusb-1.0 || { echo 'Install development dependencies: brew install pkg-config libusb' >&2; exit 1; }
[[ -f libournotes_ffi.dylib ]] || { echo 'Missing libournotes_ffi.dylib in the app root. Run: bash scripts/fetch-native-engine.sh' >&2; exit 1; }
# Mygo defaults its child builds to CGO_ENABLED=0 unless the environment sets
# it. Enable cgo for this development session so native USB stays available
# during every hot rebuild.
export CGO_ENABLED=1
export OURNOTES_DEV_MODE=1
export OURNOTES_DEV_STORAGE="${OURNOTES_DEV_STORAGE:-$PWD/$product_devStorageDir}"
export OURNOTES_FFI_LIB="$PWD/libournotes_ffi.dylib"
mkdir -p "$OURNOTES_DEV_STORAGE"
# The mygo CLI comes from the mygo-cli npm package pinned in package.json.
mygo=node_modules/.bin/mygo
[[ -x "$mygo" ]] || { echo 'Install the mygo CLI first: bun install' >&2; exit 1; }
exec "$mygo" dev "$@"
