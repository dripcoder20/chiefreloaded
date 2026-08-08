import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * US-004 — the store half of publishing: when the control is offered, and what
 * one press does.
 *
 * Whether a PRD can be published is the engine's answer, not a guess made here:
 * it depends on git mode and on whether any story has committed, neither of
 * which the frontend can see.
 */

const backend = vi.hoisted(() => ({
  publishOffer: vi.fn(),
  publish: vi.fn(),
  list: vi.fn(),
  get: vi.fn(),
}));

vi.mock("../platform", async () => {
  const actual = await vi.importActual<typeof import("../platform")>("../platform");
  return {
    ...actual,
    api: {
      prd: {
        publishOffer: (...a: unknown[]) => backend.publishOffer(...a),
        publish: (...a: unknown[]) => backend.publish(...a),
        list: (...a: unknown[]) => backend.list(...a),
        get: (...a: unknown[]) => backend.get(...a),
      },
    },
  };
});

import { app, publishPullRequest, reloadPublishOffer } from "./app.svelte";

const REPORT = {
  prd: "checkout",
  branch: "chief/checkout",
  base: "main",
  stories: ["US-001", "US-002"],
  updated: false,
  pr: { number: 128, url: "https://github.com/acme/checkout/pull/128", state: "OPEN" },
};

beforeEach(() => {
  vi.clearAllMocks();
  app.error = null;
  app.selectedPrd = "checkout";
  app.runs = [];
  app.publishing = false;
  app.published = null;
  app.publishOffer = null;
  backend.list.mockResolvedValue([]);
  backend.get.mockResolvedValue({ name: "checkout", stories: [] });
  backend.publish.mockResolvedValue(REPORT);
});

describe("whether the control appears", () => {
  it("is offered once the engine says the PRD can be published", async () => {
    backend.publishOffer.mockResolvedValue({ available: true, layout: "one-branch" });
    await reloadPublishOffer("checkout");
    expect(app.canPublish).toBe(true);
  });

  it("is absent when the engine refuses, whatever the reason", async () => {
    backend.publishOffer.mockResolvedValue({
      available: false,
      reason: "this project is not a git repository",
    });
    await reloadPublishOffer("checkout");
    expect(app.canPublish).toBe(false);
  });

  // The engine refuses to publish during a run, so offering the control would
  // make it a way to read an error message.
  it("is absent while a run for the PRD is live", async () => {
    backend.publishOffer.mockResolvedValue({ available: true, layout: "one-branch" });
    await reloadPublishOffer("checkout");
    app.runs = [{ id: "r1", prd: "checkout", state: "running" }] as never;
    expect(app.canPublish).toBe(false);
  });

  // An answer that arrives after the user has moved on describes a different
  // PRD, and adopting it would offer the control for the wrong one.
  it("ignores an answer for a PRD that is no longer selected", async () => {
    backend.publishOffer.mockResolvedValue({ available: true });
    await reloadPublishOffer("docs-site");
    expect(app.publishOffer).toBeNull();
  });

  it("hides the control when the offer cannot be read", async () => {
    app.publishOffer = { available: true } as never;
    backend.publishOffer.mockRejectedValue(new Error("no project is open"));
    await reloadPublishOffer("checkout");
    expect(app.canPublish).toBe(false);
  });
});

describe("publishing", () => {
  it("publishes the selected PRD and keeps the resulting pull request", async () => {
    await publishPullRequest(false);

    expect(backend.publish).toHaveBeenCalledWith({ prd: "checkout", draft: false });
    expect(app.published?.pr?.number).toBe(128);
    expect(app.publishing).toBe(false);
  });

  it("asks for a draft when the draft item is chosen", async () => {
    await publishPullRequest(true);
    expect(backend.publish).toHaveBeenCalledWith({ prd: "checkout", draft: true });
  });

  // One press publishes once: a second while the first is in flight would push
  // and call gh again for the same branch.
  it("refuses a second press while one is in flight", async () => {
    app.publishing = true;
    await publishPullRequest(false);
    expect(backend.publish).not.toHaveBeenCalled();
  });

  it("reports a refusal verbatim rather than rewording it", async () => {
    backend.publish.mockRejectedValue(
      new Error("main is running. Stop or finish the run before publishing"),
    );
    await publishPullRequest(false);

    expect(app.error).toContain("Stop or finish the run before publishing");
    expect(app.publishing).toBe(false);
  });
});
