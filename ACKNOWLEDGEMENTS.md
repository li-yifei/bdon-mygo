# Acknowledgements

bdon-mygo is a small native shell. Nearly everything useful in it comes from the projects below. Thank you.

## Moenotes / bdon.moe (StarMoe)

The deck calculator UI, its card and chart data, and the calculation data files that bdon-mygo works with all belong to **Moenotes**, the fan-made data site at <https://bdon.moe> built by the **StarMoe** team (<https://github.com/StarMoe-org/moenotes>). The site's front-end source is published under **AGPL-3.0**. Its commit history lists the main contributors as Exmeaning (東雪) and nichinichisou. The site credits 東雪 as editor-in-chief. Its `package.json` declares no `author` or `license` field. The license comes from the repository `LICENSE` file and the README.

bdon-mygo is an **unofficial client**. It loads the live page from `https://bdon.moe/tools/deck` in a system WebView, exactly like a browser would. It does **not** copy, bundle, mirror or redistribute any Moenotes source code, web UI, or data. The web UI and the calculation data remain the work and property of that project, under that project's terms (<https://bdon.moe/terms>, <https://bdon.moe/licenses>). Please support them, report issues about the calculator itself to them, and do not put extra load on their servers.

bdon-mygo is not made, endorsed or supported by StarMoe / Moenotes.

## ournotes-deck (calculation engine)

The native multithreaded engine is built from **ournotes-deck**, a Rust deck-recommendation engine:
- upstream: <https://github.com/empty-sekai/ournotes-deck>
- fork used here (branch `native-desktop`, C ABI / dylib build): <https://github.com/li-yifei/ournotes-deck>
- License: dual **MIT OR Apache-2.0**, "Copyright (c) 2026 The ournotes-deck contributors"

## MyGo

The desktop shell (Go + WKWebView) is built on **MyGo** by EGOIST and contributors, <https://github.com/egoist/mygo>, **MIT**.

## ADB and USB

Device communication uses **go-adb** by Patrick Gaskin (MIT), **gousb** by Google (Apache-2.0) and **libusb** (LGPL-2.1, dynamically linked). Optional Android SDK Platform-Tools are downloaded from Google on demand. See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) for the full list.

## The game

*BanG Dream! Our Notes*, its characters, music, artwork, data, names and logos are trademarks and/or copyrighted works of **Bushiroad** and the respective rights holders (Moenotes credits game assets to Bushiroad / Craft Egg / Ishimori). bdon-mygo is an independent fan-made tool and is **not affiliated with, endorsed, sponsored or approved by** Bushiroad, Craft Egg, or any rights holder. The game name is used only to describe compatibility (nominative use).

## Disclaimer

- **Unofficial tool.** It has no connection to the game's developer, publisher, or the Moenotes site.
- **Use at your own risk.** The software is provided "as is" without warranty (see LICENSE). The author is not responsible for account actions, data loss, or any other consequence of using it. Using third-party tools may conflict with a game's terms of service. Read them and decide for yourself.
- **No game assets are distributed.** The repository and release binaries contain no game artwork, audio, chart data, master data, or decryption keys. All game-related content you see is loaded live from bdon.moe or read from your own device.
- **Your save stays local.** Save files imported over ADB are processed on your Mac and written only into the WebView's local storage for the bdon.moe page. bdon-mygo does not upload them anywhere. See the privacy section in the README for what the bdon.moe page itself does.
