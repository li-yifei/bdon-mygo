# Third-party notices

bdon-mygo is MIT-licensed (see LICENSE). It links or ships the third-party components below, each under its own license. License texts are copied into `Contents/Resources/licenses/` inside the app bundle at build time. Versions are those pinned in `go.mod` at the time of writing. Regenerate this file on every release (see release-checklist.md).

## Not distributed (loaded at runtime from the network)

| Component | Owner | License / terms | Notes |
|---|---|---|---|
| Moenotes web app at https://bdon.moe/tools/deck, including the deck calculator UI and calculation data | StarMoe team (https://github.com/StarMoe-org/moenotes) | Front-end code: AGPL-3.0. Game content: rights holders. Self-made assets: CC BY-NC 4.0 (per https://bdon.moe/licenses) | Loaded live into WKWebView. Not bundled, mirrored, or modified on disk. |
| Android SDK Platform-Tools (`adb`) | Google | Android SDK License Agreement / Platform-Tools terms: https://developer.android.com/studio/terms | **Optional**, downloaded on demand from `https://dl.google.com/android/repository/` into the user's Application Support folder, checksum-verified, never redistributed by this project. By choosing the download the user accepts Google's terms. |

## Native libraries shipped in the app bundle

| Component | Version | License | Source |
|---|---|---|---|
| ournotes-deck engine (`libournotes_ffi.dylib` on macOS, `libournotes_ffi.dll` on Windows) | fork `li-yifei/ournotes-deck@native-desktop` | MIT OR Apache-2.0, Copyright (c) 2026 The ournotes-deck contributors | https://github.com/li-yifei/ournotes-deck (upstream https://github.com/empty-sekai/ournotes-deck) |
| Rust crates compiled into the engine | see engine `Cargo.lock` | various permissive (MIT / Apache-2.0 / BSD-like). Generate the exact list with `cargo about` or `cargo license` | crates.io |
| libusb (`Contents/Frameworks/libusb-1.0.0.dylib`, macOS app only) | 1.0.x (1.0.30 at time of writing) | **LGPL-2.1-or-later** | https://github.com/libusb/libusb |

### libusb (LGPL-2.1) notice

libusb is **dynamically linked** and shipped unmodified as a separate, replaceable shared library at `Contents/Frameworks/libusb-1.0.0.dylib`. The executable loads it via `@executable_path/../Frameworks/libusb-1.0.0.dylib`. You may replace it with your own build of a compatible libusb version. After replacing it, re-sign the app ad hoc (`codesign --force --deep --sign - "bdon-mygo.app"`), because macOS rejects modified signed bundles. The full LGPL-2.1 text is shipped as `Contents/Resources/licenses/libusb-LGPL-2.1.txt`. The corresponding source for the shipped version is available at https://github.com/libusb/libusb/releases (tag matching the version in `libusb-source.txt`). On request we will also provide it for at least three years from the release date.

## Go modules compiled into the executable

Verified from the module cache (`$GOPATH/pkg/mod`, LICENSE file of each pinned version) and `go list -deps`:

| Module | Version | License |
|---|---|---|
| github.com/egoist/mygo | v0.3.7 | MIT, Copyright (c) 2026 MyGo contributors |
| github.com/pgaskin/go-adb | v0.0.0-20260909040028-2bc9b939919d | MIT, Copyright (c) 2025 Patrick Gaskin |
| github.com/google/gousb | v1.1.3 | Apache-2.0 |
| github.com/ebitengine/purego | v0.11.1 | Apache-2.0 |
| github.com/skip2/go-qrcode | v0.0.0-20200617195104-da1b6568686e | MIT, Copyright (c) 2014 Tom Harwood |
| github.com/hashicorp/mdns | v1.0.5 | **MIT**, Copyright (c) 2014 Armon Dadgar *(not MPL-2.0. The v1.0.5 LICENSE file is MIT)* |
| github.com/miekg/dns | v1.1.41 | BSD-3-Clause |
| github.com/andybalholm/brotli | v1.1.1 | MIT, Copyright (c) the Brotli Authors |
| github.com/klauspost/compress | v1.18.0 | BSD-3-Clause (root package. Some sub-packages carry their own BSD/MIT/Apache notices) |
| github.com/pierrec/lz4/v4 | v4.1.22 | BSD-3-Clause, Copyright (c) 2015 Pierre Curto |
| github.com/go-text/typesetting | v0.3.5 | Unlicense OR BSD-3-Clause |
| golang.org/x/image | v0.46.0 | BSD-3-Clause, The Go Authors |
| golang.org/x/net | v0.59.0 | BSD-3-Clause, The Go Authors |
| golang.org/x/sys | v0.48.0 | BSD-3-Clause, The Go Authors |
| google.golang.org/protobuf | v1.36.6 | BSD-3-Clause, The Go Authors |
| Go standard library / runtime | go1.27.x | BSD-3-Clause, The Go Authors |

Apache-2.0 components (gousb, purego) have no NOTICE file in the pinned versions. If a future version adds one, include it.

## Trademarks

"BanG Dream!" and "Our Notes" are trademarks of Bushiroad. All game assets and trademarks belong to Bushiroad and the respective rights holders. bdon-mygo is unaffiliated with them, and with StarMoe / Moenotes. Names are used only to identify compatibility.
