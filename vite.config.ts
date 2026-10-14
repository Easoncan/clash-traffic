import { defineConfig } from "vite";

export default defineConfig({
  // JSX compiles to Preact.
  oxc: { jsx: { runtime: "automatic", importSource: "preact" } },
  server: {
    // The app loads this address during `mygo dev` (devUrl in mygo.config.ts).
    port: 5173,
    strictPort: true,
    // The development app and the packaged builds are not the page's.
    watch: { ignored: ["**/.mygo/**", "**/build/**"] },
  },
});
