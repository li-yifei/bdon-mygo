#!/usr/bin/env bash
# Obtain libournotes_ffi (the Rust deck engine loaded through FFI) and place it
# in the app root, where dev/build scripts and the app itself look for it.
#
#   bash scripts/fetch-native-engine.sh            # build if possible, else download
#   bash scripts/fetch-native-engine.sh build      # build from an ournotes-deck checkout
#   bash scripts/fetch-native-engine.sh download   # download a GitHub release asset
#   bash scripts/fetch-native-engine.sh release [os-arch ...]
#                                                   # per-platform release engines
#
# Build mode uses OURNOTES_DECK_SOURCE, else ../ournotes-deck next to this
# repository, else clones OURNOTES_DECK_GIT into .native-src/ and checks out the
# revision pinned in docs/native-search-revision.txt. An existing checkout must
# contain that revision; its current HEAD (including local changes) is built.
# Requires git and a Rust toolchain (cargo).
#
# Download mode fetches
#   https://github.com/$OURNOTES_DECK_REPO/releases/download/$OURNOTES_DECK_RELEASE_TAG/$OURNOTES_DECK_ASSET
# or OURNOTES_DECK_RELEASE_URL when set. Set OURNOTES_DECK_SHA256 to verify the
# file. The default tag below is the published release; override
# OURNOTES_DECK_RELEASE_TAG (or the URL) to use another build.
#
# OURNOTES_FFI_OUT overrides the output path.
#
# Release mode downloads the engines that the multi-platform release ships,
# one per platform, from the release named in docs/native-engine-release.txt
# (override with OURNOTES_ENGINE_RELEASE_TAG): the assets
# libournotes_ffi-<os>-<arch>.<dylib|so|dll>, each checked against the
# release's SHA256SUMS, installed as
#   $OURNOTES_ENGINE_DIR/<os>-<arch>/libournotes_ffi.<ext>
# (default .native-engines/ in the app root). Platforms default to the
# release targets: darwin-arm64 windows-amd64 windows-arm64.
set -euo pipefail

engine_ext() {
  case "$1" in
    darwin-*) echo dylib ;;
    linux-*) echo so ;;
    windows-*) echo dll ;;
    *) echo "Unknown platform: $1" >&2; return 1 ;;
  esac
}

sha256_of() {
  if command -v sha256sum >/dev/null; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi
}

release_engines() {
  local app_root repo tag out stage sums platform ext asset expected actual
  app_root="$(cd "$(dirname "$0")/.." && pwd)"
  repo="${OURNOTES_DECK_REPO:-li-yifei/ournotes-deck}"
  tag="${OURNOTES_ENGINE_RELEASE_TAG:-$(tr -d '[:space:]' < "$app_root/docs/native-engine-release.txt")}"
  out="${OURNOTES_ENGINE_DIR:-$app_root/.native-engines}"
  local platforms=("$@")
  [[ ${#platforms[@]} -gt 0 ]] || platforms=(darwin-arm64 windows-amd64 windows-arm64)
  stage="$(mktemp -d "${TMPDIR:-/tmp}/ournotes-engines.XXXXXX")"
  trap 'rm -rf "$stage"' RETURN
  local base="https://github.com/$repo/releases/download/$tag"
  echo "Downloading $base/SHA256SUMS"
  curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 -o "$stage/SHA256SUMS" "$base/SHA256SUMS"
  for platform in "${platforms[@]}"; do
    ext="$(engine_ext "$platform")"
    asset="libournotes_ffi-$platform.$ext"
    # SHA256SUMS lines: "<sha256>  <name>" (a leading * marks binary mode).
    expected="$(awk -v name="$asset" '{n=$2; sub(/^\*/, "", n)} n == name {print tolower($1)}' "$stage/SHA256SUMS")"
    [[ ${#expected} -eq 64 ]] || { echo "SHA256SUMS of $tag has no entry for $asset" >&2; return 1; }
    echo "Downloading $base/$asset"
    curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 -o "$stage/$asset" "$base/$asset"
    actual="$(sha256_of "$stage/$asset")"
    [[ "$actual" == "$expected" ]] || { echo "Checksum mismatch for $asset: $actual, expected $expected" >&2; return 1; }
    mkdir -p "$out/$platform"
    cp "$stage/$asset" "$out/$platform/.libournotes_ffi.$ext.tmp"
    chmod 755 "$out/$platform/.libournotes_ffi.$ext.tmp"
    mv -f "$out/$platform/.libournotes_ffi.$ext.tmp" "$out/$platform/libournotes_ffi.$ext"
    # Records the release, so a build can tell a cached engine is outdated.
    echo "$tag" > "$out/$platform/release.txt"
    echo "Installed $out/$platform/libournotes_ffi.$ext ($expected)"
  done
}

if [[ "${1:-}" == release ]]; then
  shift
  release_engines "$@"
  exit 0
fi

app_root="$(cd "$(dirname "$0")/.." && pwd)"
case "$(uname -s)" in
  Darwin) suffix=dylib ;;
  Linux) suffix=so ;;
  *) echo "Unsupported platform: $(uname -s)" >&2; exit 1 ;;
esac
library_name="libournotes_ffi.$suffix"
output="${OURNOTES_FFI_OUT:-$app_root/$library_name}"
revision="$(tr -d '[:space:]' < "$app_root/docs/native-search-revision.txt")"
repo="${OURNOTES_DECK_REPO:-li-yifei/ournotes-deck}"
git_url="${OURNOTES_DECK_GIT:-https://github.com/$repo.git}"
# Default release of the engine matching docs/native-search-revision.txt.
release_tag="${OURNOTES_DECK_RELEASE_TAG:-native-desktop-2026.10.10}"
default_sha256="c98b94037a0e25a819a71786a03e73cc0b0369cb909d4e549d07fa2edfd5b68f"
asset="${OURNOTES_DECK_ASSET:-$library_name}"

install_library() {
  local source="$1" temporary
  temporary="$(mktemp "$(dirname "$output")/.native-engine.XXXXXX")"
  cp "$source" "$temporary"
  chmod 755 "$temporary"
  mv -f "$temporary" "$output"
  echo "Installed $output"
  shasum -a 256 "$output"
}

build_engine() {
  command -v cargo >/dev/null || { echo 'Build mode needs cargo (https://rustup.rs).' >&2; return 1; }
  local source="${OURNOTES_DECK_SOURCE:-}"
  if [[ -z "$source" && -f "$app_root/../ournotes-deck/Cargo.toml" ]]; then
    source="$app_root/../ournotes-deck"
  fi
  if [[ -n "$source" ]]; then
    git -C "$source" merge-base --is-ancestor "$revision" HEAD || {
      echo "$source must contain ournotes-deck revision $revision" >&2
      return 1
    }
  else
    source="$app_root/.native-src/ournotes-deck"
    if [[ ! -d "$source/.git" ]]; then
      mkdir -p "$(dirname "$source")"
      git clone --filter=blob:none "$git_url" "$source"
    fi
    git -C "$source" fetch --quiet origin "$revision" 2>/dev/null || git -C "$source" fetch --quiet origin
    git -C "$source" checkout --quiet --detach "$revision"
  fi
  cargo build --manifest-path "$source/Cargo.toml" --locked --release -p ournotes-ffi
  local target_root
  target_root="$(cargo metadata --manifest-path "$source/Cargo.toml" --no-deps --format-version 1 \
    | python3 -c 'import json,sys; print(json.load(sys.stdin)["target_directory"])')"
  install_library "$target_root/release/$library_name"
}

download_engine() {
  local url="${OURNOTES_DECK_RELEASE_URL:-}"
  if [[ -z "$url" ]]; then
    [[ -n "$release_tag" ]] || {
      echo "Set OURNOTES_DECK_RELEASE_TAG (release of $repo) or OURNOTES_DECK_RELEASE_URL to download $asset." >&2
      return 1
    }
    url="https://github.com/$repo/releases/download/$release_tag/$asset"
  fi
  local stage
  stage="$(mktemp -d "${TMPDIR:-/tmp}/ournotes-engine.XXXXXX")"
  trap 'rm -rf "$stage"' RETURN
  echo "Downloading $url"
  curl --fail --location --proto '=https' --tlsv1.2 -o "$stage/$asset" "$url"
  local expected="${OURNOTES_DECK_SHA256:-}"
  if [[ -z "$expected" && "$release_tag" == "native-desktop-2026.10.10" && "$asset" == "libournotes_ffi.dylib" ]]; then
    expected="$default_sha256"
  fi
  if [[ -n "$expected" ]]; then
    echo "$expected  $stage/$asset" | shasum -a 256 -c -
  else
    echo 'Warning: OURNOTES_DECK_SHA256 not set; downloaded file is unverified.' >&2
  fi
  install_library "$stage/$asset"
}

case "${1:-auto}" in
  build) build_engine ;;
  download) download_engine ;;
  auto)
    if command -v cargo >/dev/null; then build_engine; else download_engine; fi ;;
  *) echo "Usage: $0 [build|download|release [os-arch ...]]" >&2; exit 2 ;;
esac
