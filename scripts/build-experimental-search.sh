#!/usr/bin/env bash
set -euo pipefail

app_root="$(cd "$(dirname "$0")/.." && pwd)"
source_root="${OURNOTES_DECK_SOURCE:-$app_root/../ournotes-deck}"
revision="$(cat "$app_root/docs/native-search-revision.txt")"
git -C "$source_root" merge-base --is-ancestor "$revision" HEAD || {
  echo "Rust checkout must include native search revision $revision" >&2
  exit 1
}
cargo build --manifest-path "$source_root/Cargo.toml" --locked --release -p ournotes-ffi
target_root="$(cargo metadata --manifest-path "$source_root/Cargo.toml" --no-deps --format-version 1 | python3 -c 'import json,sys; print(json.load(sys.stdin)["target_directory"])')"
library="$app_root/libournotes_ffi_experimental.dylib"
temporary="$(mktemp "$app_root/.experimental-search.XXXXXX")"
trap 'rm -f "$temporary"' EXIT
cp "$target_root/release/libournotes_ffi.dylib" "$temporary"
chmod 755 "$temporary"
mv -f "$temporary" "$library"
shasum -a 256 "$library"
