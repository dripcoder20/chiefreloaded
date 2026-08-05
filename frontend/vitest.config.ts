import { defineConfig } from "vitest/config";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import { svelteTesting } from "@testing-library/svelte/vite";

// A test-only config: the app's vite.config.ts pulls in the Wails runtime
// plugin, which expects a real webview bridge and has no place under jsdom.
// Vitest bundles its own copy of Vite, so the plugin types don't line up with
// the app's Vite 8 — the cast keeps that noise out of the config.
export default defineConfig({
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  plugins: [svelte() as any, svelteTesting()],
  resolve: {
    alias: {
      // The real runtime arms a timer on import that touches `window`, which
      // can outlive jsdom and surface as an unhandled error that fails the run
      // with every test still passing. See the stub for why replacing it costs
      // no coverage.
      "@wailsio/runtime": new URL("./src/test/wailsRuntimeStub.ts", import.meta.url).pathname,
    },
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
    include: ["src/**/*.test.ts"],
  },
});
