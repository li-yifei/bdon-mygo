import { existsSync, readFileSync } from "node:fs";
import { defineConfig } from "mygo-cli";

// product.json is the single source of the product identity.
const product = JSON.parse(readFileSync(new URL("./product.json", import.meta.url), "utf8"));
// The version lives in package.json, so a release tag can be checked against it.
const pkg = JSON.parse(readFileSync(new URL("./package.json", import.meta.url), "utf8"));
const pairing = !!process.env.OURNOTES_PAIRING_TEST_ROOT;

export default defineConfig({
  name: pairing ? `${product.name} Pairing` : product.name,
  identifier: pairing ? `${product.identifier}.pairing` : product.identifier,
  version: pkg.version,
  bindings: ".mygo/client.ts",
  // OURNOTES_ICON overrides the app icon for a local build.
  icon: [process.env.OURNOTES_ICON, "resources/icon.png"].find((p) => p && existsSync(p)),
  frontendDist: "frontend",
  // Release builds ship the engine of each platform from
  // resources/<os>-<arch>/, which scripts/build-release.sh stages and removes.
  // In development the source dylib is a resource, so its updates also
  // trigger a rebuild/relaunch.
  resources: process.env.OURNOTES_DEV_MODE === "1"
    ? ["libournotes_ffi.dylib", "libournotes_ffi_experimental.dylib"].filter(existsSync)
    : [],
  out: "build",
  linux: {
    comment: "Unofficial desktop shell for the bdon.moe deck tool",
    categories: ["Game", "Utility"],
  },
});
