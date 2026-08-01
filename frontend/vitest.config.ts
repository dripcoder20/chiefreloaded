import { defineConfig } from "vitest/config";

/**
 * A standalone Vitest config, deliberately not the app's vite.config: that one
 * loads the Wails runtime plugin and resolves the generated bindings, neither of
 * which exists in a plain checkout. The unit tests import only binding-free
 * modules (src/stores/usage.ts), so a minimal Node environment is all they need.
 */
export default defineConfig({
  test: {
    include: ["src/**/*.test.ts"],
    environment: "node",
  },
});
