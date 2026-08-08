import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * The store side of a PRD's workflow settings: saving them configures the later
 * run and starts nothing, and the New PRD selectors are seeded from the defaults
 * the Go side resolved rather than from a blank.
 */

const backend = vi.hoisted(() => ({
  saveWorkflow: vi.fn(),
  agentDefaults: vi.fn(),
  start: vi.fn(),
}));

vi.mock("../platform", async () => {
  const actual = await vi.importActual<typeof import("../platform")>("../platform");
  return {
    ...actual,
    api: {
      prd: {
        saveWorkflow: (...a: unknown[]) => backend.saveWorkflow(...a),
      },
      project: {
        agentDefaults: (...a: unknown[]) => backend.agentDefaults(...a),
      },
      run: { start: (...a: unknown[]) => backend.start(...a) },
    },
  };
});

import { app, reloadCreationOptions, savePrdWorkflow } from "./app.svelte";

beforeEach(() => {
  vi.clearAllMocks();
  app.error = null;
  app.agentDefaults = null;
  backend.saveWorkflow.mockResolvedValue(undefined);
  backend.agentDefaults.mockResolvedValue({ authoring: "claude", implementation: "codex" });
});

describe("savePrdWorkflow", () => {
  it("stores the settings and starts no run", async () => {
    const ok = await savePrdWorkflow("checkout", {
      implementationAgent: "codex",
      stackPerStory: true,
    } as never);

    expect(ok).toBe(true);
    expect(backend.saveWorkflow).toHaveBeenCalledWith("checkout", {
      implementationAgent: "codex",
      stackPerStory: true,
    });
    expect(backend.start).not.toHaveBeenCalled();
  });

  it("reports a failure to save without adopting it", async () => {
    backend.saveWorkflow.mockRejectedValue(new Error("permission denied"));
    const ok = await savePrdWorkflow("checkout", {} as never);
    expect(ok).toBe(false);
    expect(app.error).toContain("permission denied");
  });
});

describe("reloadCreationOptions", () => {
  it("adopts the resolved per-phase defaults", async () => {
    await reloadCreationOptions();
    expect(app.agentDefaults).toEqual({ authoring: "claude", implementation: "codex" });
  });

  it("leaves the defaults unset when they cannot be read", async () => {
    backend.agentDefaults.mockRejectedValue(new Error("no project open"));
    await reloadCreationOptions();
    expect(app.agentDefaults).toBeNull();
  });
});
