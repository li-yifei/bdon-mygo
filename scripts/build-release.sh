#!/usr/bin/env bash
# Builds the release apps and puts the user-facing files in dist/:
#
#   bdon-mygo-<version>-darwin-arm64.dmg       macOS (Apple silicon): the app in a disk image
#   bdon-mygo-<version>-windows-<arch>.zip     Windows: unzip and run, no installer
#   SHA256SUMS
#
# A Windows archive holds one folder with the executable, the engine, the
# app's files, licenses and a README (scripts/package-archive.py).
# TODO(linux): Linux releases are deferred. The code builds and tests on
# Linux, and package-archive.py can already make the tar.gz; add linux/amd64
# and linux/arm64 to all_targets, the packaging below and the CI workflow.
#
#   bash scripts/build-release.sh                       # every platform
#   bash scripts/build-release.sh darwin/arm64          # some of them
#
# Platforms: darwin/arm64 windows/amd64 windows/arm64. macOS is built
# natively (it needs macOS, cgo and libusb for the direct USB transport);
# Windows is cross-compiled without cgo by one `mygo build -platform ...`,
# from any machine. On a machine other than a Mac,
# the default list leaves macOS out.
#
# Each app ships the native engine of its platform, staged in
# resources/<os>-<arch>/ for the build and removed afterwards. The engines come
# from $OURNOTES_ENGINE_DIR/<os>-<arch>/libournotes_ffi.<dylib|so|dll>
# (default ../engine-artifacts when it exists, else .native-engines); the
# missing ones are downloaded and checked against SHA256SUMS by
# `scripts/fetch-native-engine.sh release` (OURNOTES_ENGINE_FETCH=0 disables
# that). For darwin/arm64 the app root's libournotes_ffi.dylib, the one
# development uses, is the last resort.
#
# Environment: OURNOTES_SIGN_IDENTITY (macOS signing identity, default ad
# hoc), GOFLAGS (kept, e.g. for extra build tags).
set -euo pipefail
cd "$(dirname "$0")/.."
root="$PWD"
source scripts/product.sh

all_targets=(darwin/arm64 windows/amd64 windows/arm64)
host_os="$(uname -s)"
targets=()
if [[ $# -gt 0 ]]; then
  targets=("$@")
else
  for t in "${all_targets[@]}"; do
    if [[ "$t" == darwin/* && "$host_os" != Darwin ]]; then
      echo "Note: skipping $t, which needs a Mac." >&2
      continue
    fi
    targets+=("$t")
  done
fi
for t in "${targets[@]}"; do
  case " ${all_targets[*]} " in *" $t "*) ;; *) echo "Unsupported platform: $t (supported: ${all_targets[*]})" >&2; exit 2 ;; esac
done

for tool in go python3; do
  command -v "$tool" >/dev/null || { echo "Missing build tool: $tool" >&2; exit 1; }
done
mygo=node_modules/.bin/mygo
[[ -x "$mygo" ]] || { echo 'Install the mygo CLI first: bun install' >&2; exit 1; }
version="$(python3 -c 'import json; print(json.load(open("package.json"))["version"])')"
slug="$product_linuxBinary"
dist="$root/dist"

engine_ext() {
  case "$1" in
    darwin-*) echo dylib ;;
    linux-*) echo so ;;
    windows-*) echo dll ;;
  esac
}
sha256_of() {
  if command -v sha256sum >/dev/null; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi
}
remove_tree() { python3 -c 'import shutil, sys; shutil.rmtree(sys.argv[1], ignore_errors=True)' "$1"; }

# User data lives in the per-user directories, never in the app: refuse to
# build when a storage directory could be copied into an app as a resource.
for dir in "$product_legacyStorageDir" "$product_devStorageDir"; do
  if [[ -e "resources/$dir" ]]; then
    echo "Refusing to build: resources/$dir would ship personal data with the app." >&2
    exit 1
  fi
done

# ---------------------------------------------------------------------------
# Native engines, one per platform, staged in resources/<os>-<arch>/.

engine_dir="${OURNOTES_ENGINE_DIR:-}"
if [[ -z "$engine_dir" ]]; then
  if [[ -d "$root/../engine-artifacts" ]]; then
    engine_dir="$(cd "$root/../engine-artifacts" && pwd)"
  else
    engine_dir="$root/.native-engines"
  fi
fi
# A staged engine directory names the engine commit it holds; refuse one built
# from another revision than the pinned one.
if [[ -f "$engine_dir/SOURCE_COMMIT" ]]; then
  staged_commit="$(tr -d '[:space:]' < "$engine_dir/SOURCE_COMMIT")"
  pinned_commit="$(tr -d '[:space:]' < "$root/docs/native-search-revision.txt")"
  if [[ "$staged_commit" != "$pinned_commit" ]]; then
    echo "$engine_dir holds engines from $staged_commit, but docs/native-search-revision.txt pins $pinned_commit." >&2
    echo "Update or move that directory, or set OURNOTES_ENGINE_DIR." >&2
    exit 1
  fi
fi
pinned_release="${OURNOTES_ENGINE_RELEASE_TAG:-$(tr -d '[:space:]' < "$root/docs/native-engine-release.txt")}"
missing=()
for t in "${targets[@]}"; do
  platform="${t/\//-}"
  if [[ ! -f "$engine_dir/$platform/libournotes_ffi.$(engine_ext "$platform")" ]]; then
    missing+=("$platform")
  elif [[ "${OURNOTES_ENGINE_FETCH:-1}" != 0 && "$engine_dir" == "$root/.native-engines" && "$(cat "$engine_dir/$platform/release.txt" 2>/dev/null)" != "$pinned_release" ]]; then
    # A download cached from another release than the pinned one.
    rm -f "$engine_dir/$platform/libournotes_ffi.$(engine_ext "$platform")"
    missing+=("$platform")
  fi
done
if [[ ${#missing[@]} -gt 0 && "${OURNOTES_ENGINE_FETCH:-1}" != 0 ]]; then
  fetch_dir="$engine_dir"
  # Never write downloads into a directory another workflow fills.
  [[ "$engine_dir" == "$root/.native-engines" ]] || fetch_dir="$root/.native-engines"
  echo "Fetching release engines for: ${missing[*]}"
  OURNOTES_ENGINE_DIR="$fetch_dir" bash scripts/fetch-native-engine.sh release "${missing[@]}" ||
    echo 'Warning: downloading the release engines failed.' >&2
fi

staged=()
embedded_credentials=""
cleanup() {
  for dir in "${staged[@]+"${staged[@]}"}"; do remove_tree "$dir"; done
  [[ -z "$embedded_credentials" ]] || rm -f "$embedded_credentials"
}
trap cleanup EXIT
# Build-time credentials (see scripts/embed-save-credentials.py).
# OURNOTES_EMBED_LOCAL_CREDENTIALS=1 reads the local configuration file.
credentials_go="$root/zz_embedded_save_credentials.go"
if [[ ! -f "$credentials_go" ]]; then
  local_flag=()
  [[ "${OURNOTES_EMBED_LOCAL_CREDENTIALS:-0}" == 1 ]] && local_flag=(--local)
  python3 "$root/scripts/embed-save-credentials.py" "$credentials_go" "${local_flag[@]+"${local_flag[@]}"}"
  [[ -f "$credentials_go" ]] && embedded_credentials="$credentials_go"
else
  echo "save credentials: using the existing $(basename "$credentials_go")" >&2
fi
for t in "${targets[@]}"; do
  platform="${t/\//-}"
  ext="$(engine_ext "$platform")"
  source_lib=""
  for candidate in "$engine_dir/$platform/libournotes_ffi.$ext" "$root/.native-engines/$platform/libournotes_ffi.$ext"; do
    if [[ -f "$candidate" ]]; then source_lib="$candidate"; break; fi
  done
  if [[ -z "$source_lib" && "$platform" == darwin-arm64 && -f libournotes_ffi.dylib ]]; then
    echo "Warning: no release engine for $platform; using the development libournotes_ffi.dylib." >&2
    source_lib="$root/libournotes_ffi.dylib"
  fi
  [[ -n "$source_lib" ]] || { echo "Missing the native engine for $platform: put it in $engine_dir/$platform/libournotes_ffi.$ext or run scripts/fetch-native-engine.sh release $platform" >&2; exit 1; }
  if [[ -e "resources/$platform" ]]; then
    echo "resources/$platform already exists; it is staged by this script. Remove it first." >&2
    exit 1
  fi
  mkdir -p "resources/$platform"
  staged+=("$root/resources/$platform")
  cp "$source_lib" "resources/$platform/libournotes_ffi.$ext"
  chmod 755 "resources/$platform/libournotes_ffi.$ext"
  echo "Engine for $platform: $source_lib ($(sha256_of "$source_lib"))"
done

remove_tree "$dist"
mkdir -p "$dist"

# ---------------------------------------------------------------------------
# macOS: MyGo makes the bundle, then the executable is rebuilt with cgo for
# the direct USB transport (gousb + libusb, bundled in Contents/Frameworks).

build_darwin() {
  local arch=arm64 identity="${OURNOTES_SIGN_IDENTITY:--}"
  [[ "$host_os" == Darwin && "$(uname -m)" == arm64 ]] || { echo 'darwin/arm64 builds on an Apple silicon Mac.' >&2; exit 1; }
  for tool in bun pkg-config install_name_tool codesign hdiutil ditto; do
    command -v "$tool" >/dev/null || { echo "Missing build tool: $tool" >&2; exit 1; }
  done
  pkg-config --exists libusb-1.0 || { echo 'Install libusb development files (brew install libusb).' >&2; exit 1; }
  # The bundle MyGo builds holds a pure Go executable; it is replaced below.
  CGO_ENABLED=0 "$mygo" build -platform "darwin/$arch" -skip-dmg -sign "$identity"
  local app="build/darwin-$arch/$product_name.app"
  for dir in "$product_legacyStorageDir" "$product_devStorageDir"; do
    if [[ -e "$app/Contents/Resources/$dir" ]]; then
      echo "Refusing to package $app: Contents/Resources/$dir must not exist." >&2
      exit 1
    fi
  done
  local engine="$app/Contents/Resources/libournotes_ffi.dylib"
  [[ -f "$engine" ]] || { echo "The engine is missing from $app/Contents/Resources." >&2; exit 1; }
  local plist="$app/Contents/Info.plist"
  plist_value() { /usr/libexec/PlistBuddy -c "Print :$1" "$plist"; }
  local exe name bundle_version identifier
  exe="$(plist_value CFBundleExecutable)"
  name="$(plist_value CFBundleName)"
  bundle_version="$(plist_value CFBundleShortVersionString)"
  identifier="$(plist_value CFBundleIdentifier)"
  # Usage descriptions come from the native catalogs: English in Info.plist
  # and a localized InfoPlist.strings per language.
  python3 scripts/localize-plist.py "$plist" "$app/Contents/Resources"
  /usr/libexec/PlistBuddy -c 'Add :NSBonjourServices array' "$plist"
  /usr/libexec/PlistBuddy -c 'Add :NSBonjourServices:0 string _adb-tls-pairing._tcp' "$plist"
  /usr/libexec/PlistBuddy -c 'Add :NSBonjourServices:1 string _adb-tls-connect._tcp' "$plist"
  local ldflags="-s -w -X github.com/egoist/mygo.production=1 -X 'github.com/egoist/mygo.packageName=$name' -X github.com/egoist/mygo.packageVersion=$bundle_version -X github.com/egoist/mygo.packageIdentifier=$identifier"
  local tags=()
  # Match MyGo's production tags; keep any tags supplied through GOFLAGS.
  if [[ "${MYGO_INSPECTOR:-}" != 1 ]]; then
    local build_tags=mygo_noinspector flag
    for flag in ${GOFLAGS:-}; do
      case "$flag" in -tags=*) build_tags="${flag#-tags=},$build_tags" ;; esac
    done
    tags=(-tags "$build_tags")
  fi
  CGO_ENABLED=1 GOOS=darwin GOARCH="$arch" go build -trimpath "${tags[@]}" -ldflags "$ldflags" -o "$app/Contents/MacOS/$exe" .
  local frameworks="$app/Contents/Frameworks" libdir usb_lib licenses usb_dependency
  mkdir -p "$frameworks"
  libdir="$(pkg-config --variable=libdir libusb-1.0)"
  usb_lib="$libdir/libusb-1.0.0.dylib"
  [[ -f "$usb_lib" ]] || { echo "Missing libusb shared library: $usb_lib" >&2; exit 1; }
  cp -L "$usb_lib" "$frameworks/libusb-1.0.0.dylib"
  licenses="$app/Contents/Resources/licenses"
  mkdir -p "$licenses"
  if [[ -f "$libdir/../COPYING" ]]; then
    cp "$libdir/../COPYING" "$licenses/libusb-LGPL-2.1.txt"
  else
    echo 'libusb COPYING file missing; supply its license with the app.' >&2
    exit 1
  fi
  printf 'libusb %s\nSource: https://github.com/libusb/libusb\nDistributed as a replaceable dynamic library in Contents/Frameworks.\n' "$(pkg-config --modversion libusb-1.0)" > "$licenses/libusb-source.txt"
  chmod u+w "$frameworks/libusb-1.0.0.dylib"
  install_name_tool -id '@rpath/libusb-1.0.0.dylib' "$frameworks/libusb-1.0.0.dylib"
  # The system awk and otool: GNU tools earlier in PATH parse differently.
  usb_dependency="$(/usr/bin/otool -L "$app/Contents/MacOS/$exe" | /usr/bin/awk '$1 ~ /libusb/ {print $1}')"
  [[ -n "$usb_dependency" ]] || { echo 'USB build failed: executable has no libusb dependency.' >&2; exit 1; }
  install_name_tool -change "$usb_dependency" '@executable_path/../Frameworks/libusb-1.0.0.dylib' "$app/Contents/MacOS/$exe"
  # Fail packaging when a native library would require this machine's brew.
  local native unexpected
  for native in "$frameworks/libusb-1.0.0.dylib" "$engine"; do
    unexpected="$(/usr/bin/otool -L "$native" | tail -n +3 | /usr/bin/awk '$1 ~ /^\// && $1 !~ /^\/usr\/lib\// && $1 !~ /^\/System\// {print $1}')"
    [[ -z "$unexpected" ]] || { echo "Unbundled native dependency in $native: $unexpected" >&2; exit 1; }
  done
  local sign_args=(--force --sign "$identity")
  if [[ "$identity" != - ]]; then sign_args+=(--options runtime --timestamp); fi
  codesign "${sign_args[@]}" "$frameworks/libusb-1.0.0.dylib"
  codesign "${sign_args[@]}" "$engine"
  codesign "${sign_args[@]}" --deep "$app"
  codesign --verify --deep --strict "$app"
  /usr/bin/otool -L "$app/Contents/MacOS/$exe"
  local image_stage dmg
  image_stage="$(mktemp -d "${TMPDIR:-/tmp}/$slug-dmg.XXXXXX")"
  # hdiutil reads a staging directory holding a copy of the final app.
  ditto "$app" "$image_stage/$product_name.app"
  ln -s /Applications "$image_stage/Applications"
  dmg="$dist/$slug-$version-darwin-$arch.dmg"
  hdiutil create -ov -format UDZO -volname "$name" -srcfolder "$image_stage" "$dmg"
  remove_tree "$image_stage"
}

# ---------------------------------------------------------------------------
# Windows: cross-compiled without cgo in one MyGo build. The direct USB
# transport needs cgo, so these apps offer the ADB compatibility mode and
# direct TCP.

cross=()
for t in "${targets[@]}"; do
  [[ "$t" == darwin/* ]] || cross+=("$t")
done
if [[ " ${targets[*]} " == *" darwin/arm64 "* ]]; then
  build_darwin
fi
if [[ ${#cross[@]} -gt 0 ]]; then
  platform_list="$(IFS=,; echo "${cross[*]}")"
  CGO_ENABLED=0 "$mygo" build -platform "$platform_list"
fi

# ---------------------------------------------------------------------------
# The user-facing files.

for t in "${cross[@]+"${cross[@]}"}"; do
  os="${t%/*}" arch="${t#*/}"
  out="build/$os-$arch"
  for dir in "$product_legacyStorageDir" "$product_devStorageDir" data portable; do
    [[ ! -e "$out/$dir" ]] || { echo "Refusing to package $out: $dir must not exist." >&2; exit 1; }
  done
  exe="$product_name.exe" engine=libournotes_ffi.dll archive="$dist/$slug-$version-$os-$arch.zip"
  [[ -f "$out/$exe" && -f "$out/$engine" ]] || { echo "$out lacks $exe or $engine" >&2; exit 1; }
  # One folder with what the app runs from: an installer MyGo makes when NSIS
  # happens to be installed is left out.
  python3 scripts/package-archive.py "$out" "$archive" "$slug-$version-$os-$arch" "$os" "$product_name" "$version"
done

sums="$(cd "$dist" && for f in *; do [[ "$f" == SHA256SUMS ]] || printf '%s  %s\n' "$(sha256_of "$f")" "$f"; done)"
printf '%s\n' "$sums" > "$dist/SHA256SUMS"
echo "Release files in $dist:"
ls -l "$dist"
