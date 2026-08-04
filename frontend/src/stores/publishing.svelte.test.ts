import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * US-008 / US-009 — the store side of workflow settings and issue publishing:
 * saving settings starts nothing, and a partial publish keeps what it created
 * while naming only the stories still to retry.
 */

const backend = vi.hoisted(() => ({
  saveWorkflow: vi.fn(),
  publish: vi.fn(),
  agentDefaults: vi.fn(),
  issueDestinations: vi.fn(),
  start: vi.fn(),
}));

vi.mock("../platform", async () => {
  const actual = await vi.importActual<typeof import("../platform")>("../platform");
  return {
    ...actual,
    api: {
      prd: {
        saveWorkflow: (...a: unknown[]) => backend.saveWorkflow(...a),
        publish: (...a: unknown[]) => backend.publish(...a),
      },
      project: {
        agentDefaults: (...a: unknown[]) => backend.agentDefaults(...a),
        issueDestinations: (...a: unknown[]) => backend.issueDestinations(...a),
      },
      run: { start: (...a: unknown[]) => backend.start(...a) },
    },
  };
});

import { app, publishIssues, reloadCreationOptions, savePrdWorkflow } from "./app.svelte";

const PARTIAL = {
  prd: "checkout",
  results: [
    {
      storyId: "US-001",
      ref: { destination: "github", identifier: "#41", url: "https://example/41" },
      skipped: false,
    },
    { storyId: "US-002", error: "rate limited", skipped: false },
  ],
  failed: ["US-002"],
};

beforeEach(() => {
  vi.clearAllMocks();
  app.error = null;
  app.publishing = null;
  app.agentDefaults = null;
  app.destinations = [];
  backend.saveWorkflow.mockResolvedValue(undefined);
  backend.agentDefaults.mockResolvedValue({ authoring: "claude", implementation: "codex" });
  backend.issueDestinations.mockResolvedValue([]);
});

describe("savePrdWorkflow", () => {
  it("stores the settings and starts no run", async () => {
    const ok = await savePrdWorkflow("checkout", {
      implementationAgent: "codex",
      stackPerStory: true,
      issueDestination: "github",
    } as never);

    expect(ok).toBe(true);
    expect(backend.saveWorkflow).toHaveBeenCalledWith("checkout", {
      implementationAgent: "codex",
      stackPerStory: true,
      issueDestination: "github",
    });
    expect(backend.start).not.toHaveBeenCalled();
    expect(backend.publish).not.toHaveBeenCalled();
  });

  it("reports a failure to save without adopting it", async () => {
    backend.saveWorkflow.mockRejectedValue(new Error("permission denied"));
    const ok = await savePrdWorkflow("checkout", {} as never);
    expect(ok).toBe(false);
    expect(app.error).toContain("permission denied");
  });
});

describe("publishIssues", () => {
  it("records a fully successful publish with no error", async () => {
    backend.publish.mockResolvedValue({ prd: "checkout", results: PARTIAL.results, failed: [] });
    const report = await publishIssues("checkout");

    expect(report).not.toBeNull();
    expect(app.publishing?.results).toHaveLength(2);
    expect(app.error).toBeNull();
  });

  // A partial failure must keep the references it did create and identify only
  // the stories left to retry.
  it("keeps successful references and names only the failures", async () => {
    backend.publish.mockResolvedValue(PARTIAL);
    await publishIssues("checkout");

    const succeeded = app.publishing!.results.find((r) => r.storyId === "US-001");
    expect(succeeded?.ref?.identifier).toBe("#41");
    expect(app.publishing!.failed).toEqual(["US-002"]);
    expect(app.error).toContain("1 of 2");
  });

  it("surfaces a publishing call that failed outright", async () => {
    backend.publish.mockRejectedValue(new Error("not configured to publish issues"));
    const report = await publishIssues("checkout");

    expect(report).toBeNull();
    expect(app.error).toContain("not configured to publish issues");
  });
});

describe("reloadCreationOptions", () => {
  it("adopts the resolved per-phase defaults", async () => {
    await reloadCreationOptions();
    expect(app.agentDefaults).toEqual({ authoring: "claude", implementation: "codex" });
  });

  it("adopts destination availability with its reason", async () => {
    backend.issueDestinations.mockResolvedValue([
      { destination: "linear", name: "Linear", available: false, reason: "set LINEAR_API_KEY" },
    ]);
    await reloadCreationOptions();
    expect(app.destinations[0].reason).toContain("LINEAR_API_KEY");
  });
});
