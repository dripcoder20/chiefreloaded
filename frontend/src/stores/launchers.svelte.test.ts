import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * US-012 — the store side of the repository launchers: one activation results in
 * at most one launch request, and every failure surfaces as an actionable error
 * rather than being swallowed.
 */

const backend = vi.hoisted(() => ({
  openOnGitHub: vi.fn(),
  openInApp: vi.fn(),
  localApps: vi.fn(),
}));

vi.mock("../platform", async () => {
  const actual = await vi.importActual<typeof import("../platform")>("../platform");
  return {
    ...actual,
    api: {
      project: {
        openOnGitHub: (...a: unknown[]) => backend.openOnGitHub(...a),
        openInApp: (...a: unknown[]) => backend.openInApp(...a),
        localApps: (...a: unknown[]) => backend.localApps(...a),
      },
    },
  };
});

import { app, openOnGitHub, openInApp, reloadLocalApps } from "./app.svelte";

/** A request that stays unresolved until the test releases it. */
function deferred(): { promise: Promise<void>; resolve: () => void } {
  let resolve!: () => void;
  const promise = new Promise<void>((r) => (resolve = r));
  return { promise, resolve };
}

beforeEach(() => {
  vi.clearAllMocks();
  app.error = null;
  app.launching = false;
  app.localApps = [];
  backend.openOnGitHub.mockResolvedValue(undefined);
  backend.openInApp.mockResolvedValue(undefined);
  backend.localApps.mockResolvedValue([]);
});

describe("openOnGitHub", () => {
  it("opens the repository page", async () => {
    await openOnGitHub();
    expect(backend.openOnGitHub).toHaveBeenCalledTimes(1);
    expect(app.error).toBeNull();
  });

  it("shows an actionable error and opens nothing when there is no remote", async () => {
    backend.openOnGitHub.mockRejectedValue(
      new Error("this project has no GitHub repository configured"),
    );
    await openOnGitHub();
    expect(app.error).toContain("no GitHub repository configured");
  });

  // Overlapping UI and native events must not produce two browser tabs.
  it("ignores a second activation while the first is unresolved", async () => {
    const gate = deferred();
    backend.openOnGitHub.mockReturnValue(gate.promise);

    const first = openOnGitHub();
    await openOnGitHub();
    expect(backend.openOnGitHub).toHaveBeenCalledTimes(1);

    gate.resolve();
    await first;
    // Once settled the launcher is usable again.
    await openOnGitHub();
    expect(backend.openOnGitHub).toHaveBeenCalledTimes(2);
  });
});

describe("openInApp", () => {
  it("forwards the requested application", async () => {
    await openInApp("vscode");
    expect(backend.openInApp).toHaveBeenCalledWith("vscode");
  });

  it("surfaces the not-installed message naming the application", async () => {
    backend.openInApp.mockRejectedValue(new Error("Cursor is not installed."));
    await openInApp("cursor");
    expect(app.error).toContain("Cursor is not installed");
  });

  // "Found but would not open" must stay distinguishable from "not installed".
  it("surfaces a launch failure distinctly from a missing application", async () => {
    backend.openInApp.mockRejectedValue(
      new Error("VS Code was found but could not open the repository"),
    );
    await openInApp("vscode");
    expect(app.error).toContain("was found but could not open");
    expect(app.error).not.toContain("not installed");
  });

  it("launches once per activation", async () => {
    const gate = deferred();
    backend.openInApp.mockReturnValue(gate.promise);

    const first = openInApp("vscode");
    await openInApp("vscode");
    expect(backend.openInApp).toHaveBeenCalledTimes(1);

    gate.resolve();
    await first;
    expect(app.launching).toBe(false);
  });
});

describe("reloadLocalApps", () => {
  it("adopts the reported availability", async () => {
    backend.localApps.mockResolvedValue([
      { app: "vscode", name: "VS Code", available: true },
      { app: "cursor", name: "Cursor", available: false },
    ]);
    await reloadLocalApps();
    expect(app.localApps.map((a) => a.app)).toEqual(["vscode", "cursor"]);
  });

  // Detection is advisory: a failure must leave the entries selectable rather
  // than raising an alert nobody asked for.
  it("falls back to no detection without raising an error", async () => {
    backend.localApps.mockRejectedValue(new Error("nope"));
    await reloadLocalApps();
    expect(app.localApps).toEqual([]);
    expect(app.error).toBeNull();
  });
});
