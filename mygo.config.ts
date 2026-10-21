import { defineConfig } from "mygo-cli";

export default defineConfig({
  name: "Clash Traffic",
  identifier: "dev.lufficc.clashtraffic",
  version: "0.1.4",
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
    publicKey: "XvFzwWpm0jjixED5INf3tnNIaCOWt7qKW3ux7cPmiN0=",
    github: "lufficc/clash-traffic",
  },
});
