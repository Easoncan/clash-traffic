import { defineConfig } from "mygo-cli";

export default defineConfig({
  name: "Clash Traffic",
  identifier: "dev.easoncan.clashtraffic",
  version: "0.2.0",
  // `mygo dev` runs devCommand and loads devUrl; `mygo build` runs
  // buildCommand and embeds frontendDist into the app.
  devUrl: "http://localhost:5173",
  devCommand: "bun run dev:web",
  buildCommand: "bun run build:web",
  frontendDist: "dist",
  bindings: "src/mygo.ts",
  out: "build",
  // Apps update themselves from the GitHub releases, signed with the key
  // whose public half is here. Release builds need the secret half in
  // MYGO_UPDATER_PRIVATE_KEY; see CHANGELOG.md for the release notes.
  updates: {
    publicKey: "YcCJqEIWT1NBoqAZ3S3mXdnzdB/9C+olFzNlDwfixTc=",
    github: "Easoncan/clash-traffic",
  },
  macos: {
    // Go 1.27 links for macOS 13 and later, so the app cannot run on 12.
    minimumSystemVersion: "13.0",
  },
});
