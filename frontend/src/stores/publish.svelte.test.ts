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
  publishStack: vi.fn(),
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
        publishStack: (...a: unknown[]) => backend.publishStack(...a),
        list: (...a: unknown[]) => backend.list(...a),
        get: (...a: unknown[]) => backend.get(...a),
      },
    },
  };
});

const fireConfetti = vi.hoisted(() => vi.fn());
vi.mock("../lib/confetti", () => ({ fireConfetti: () => fireConfetti() }));

import { app, publishPullRequest, publishStack, reloadPublishOffer } from "./app.svelte";
import { celebration } from "./celebration.svelte";

const REPORT = {
  prd: "checkout",
  branch: "chief/checkout",
  base: "main",
  stories: ["US-001", "US-002"],
  updated: false,
  pr: { number: 128, url: "https://github.com/acme/checkout/pull/128", state: "OPEN" },
};

const STACK_REPORT = {
  prd: "checkout",
  stories: [
    {
      storyId: "US-001",
      branch: "loop/checkout/us-001",
      base: "main",
      pr: { number: 128, url: "https://github.com/acme/checkout/pull/128", state: "OPEN" },
    },
    { storyId: "US-002", branch: "loop/checkout/us-002", skipped: "the story produced no commit" },
  ],
};

beforeEach(() => {
  vi.clearAllMocks();
  app.error = null;
  app.selectedPrd = "checkout";
  app.runs = [];
  app.publishing = false;
  app.published = null;
  app.publishedStack = null;
  app.publishOffer = null;
  backend.list.mockResolvedValue([]);
  backend.get.mockResolvedValue({ name: "checkout", stories: [] });
  backend.publish.mockResolvedValue(REPORT);
  backend.publishStack.mockResolvedValue(STACK_REPORT);
  celebration.isCelebrationEnabled = true;
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

describe("celebrating a published pull request", () => {
  it("fires exactly once when the pull request is opened", async () => {
    await publishPullRequest(false);
    expect(fireConfetti).toHaveBeenCalledTimes(1);
  });

  it("does not fire when publishing is refused", async () => {
    backend.publish.mockRejectedValue(new Error("main is running"));
    await publishPullRequest(false);
    expect(fireConfetti).not.toHaveBeenCalled();
  });

  // A report can come back describing a push that never became a pull request.
  it("does not fire for a report with no pull request in it", async () => {
    backend.publish.mockResolvedValue({ ...REPORT, pr: undefined });
    await publishPullRequest(false);
    expect(fireConfetti).not.toHaveBeenCalled();
  });

  // The result is state: it is read again whenever the view redraws. Only the
  // act of publishing may celebrate, or a window resize would too.
  it("does not fire when the kept result is read again", async () => {
    await publishPullRequest(false);
    fireConfetti.mockClear();

    const kept = app.published;
    app.published = kept;
    expect(app.published?.pr?.number).toBe(128);
    expect(app.published?.pr?.number).toBe(128);

    expect(fireConfetti).not.toHaveBeenCalled();
  });

  it("does not fire while the preference is off", async () => {
    celebration.isCelebrationEnabled = false;
    await publishPullRequest(false);

    expect(app.published?.pr?.number).toBe(128);
    expect(fireConfetti).not.toHaveBeenCalled();
  });

  // The second press updates the existing pull request, which is as much a
  // publish as the first — and still one celebration, not two.
  it("fires once more when the pull request is updated by a second press", async () => {
    await publishPullRequest(false);
    backend.publish.mockResolvedValue({ ...REPORT, updated: true });
    await publishPullRequest(false);

    expect(fireConfetti).toHaveBeenCalledTimes(2);
  });
});

describe("publishing a stack", () => {
  // US-005 — the stacked item exists only where the run produced a stack, and
  // the engine's recorded layout is what decides that.
  it("is offered for a PRD whose run gave each story its own branch", async () => {
    backend.publishOffer.mockResolvedValue({
      available: true,
      layout: "branch-per-story",
      stacked: true,
    });
    await reloadPublishOffer("checkout");
    expect(app.canPublishStack).toBe(true);
  });

  it("is absent under a single-branch layout, with the reason kept", async () => {
    backend.publishOffer.mockResolvedValue({
      available: true,
      layout: "one-branch",
      stacked: false,
      stackReason: "this run put every story on one branch, so there is no stack to publish",
    });
    await reloadPublishOffer("checkout");

    expect(app.canPublish).toBe(true);
    expect(app.canPublishStack).toBe(false);
    expect(app.publishOffer?.stackReason).toContain("one branch");
  });

  it("keeps every story's outcome, including the ones with no pull request", async () => {
    await publishStack(false);

    expect(backend.publishStack).toHaveBeenCalledWith({ prd: "checkout", draft: false });
    expect(app.publishedStack?.stories?.[0]?.pr?.number).toBe(128);
    expect(app.publishedStack?.stories?.[1]?.skipped).toContain("no commit");
    expect(app.publishing).toBe(false);
  });

  it("asks for drafts when the draft item is chosen", async () => {
    await publishStack(true);
    expect(backend.publishStack).toHaveBeenCalledWith({ prd: "checkout", draft: true });
  });

  // A pass that stops half way arrives as a report rather than a rejection, so
  // that both halves survive: the sentence that says it failed, and the per-story
  // list that says which pull requests exist and which do not.
  it("keeps the per-story outcome of a stack that failed half way", async () => {
    backend.publishStack.mockResolvedValue({
      prd: "checkout",
      failed: "US-002: gh pr create: could not reach github.com",
      stories: [
        STACK_REPORT.stories[0],
        {
          storyId: "US-002",
          branch: "loop/checkout/us-002",
          error: "gh pr create: could not reach github.com",
        },
        {
          storyId: "US-003",
          branch: "loop/checkout/us-003",
          skipped: "the branch below it was not published",
        },
      ],
    });

    await publishStack(false);

    expect(app.error).toContain("US-002");
    expect(app.publishedStack?.stories?.[0]?.pr?.number).toBe(128);
    expect(app.publishedStack?.stories?.[1]?.error).toContain("github.com");
    expect(app.publishedStack?.stories?.[2]?.skipped).toContain("below");
  });

  // The retry: nothing was opened, and what exists is reported back.
  it("shows a retry that had nothing left to do as the pull requests it found", async () => {
    backend.publishStack.mockResolvedValue({
      prd: "checkout",
      stories: [{ ...STACK_REPORT.stories[0], alreadyOpen: true }],
    });

    await publishStack(false);

    expect(app.error).toBeNull();
    expect(app.publishedStack?.stories?.[0]?.alreadyOpen).toBe(true);
    expect(app.publishedStack?.stories?.[0]?.pr?.number).toBe(128);
  });

  it("reports a refusal rather than pretending a stack was published", async () => {
    backend.publishStack.mockRejectedValue(
      new Error("this run put every story on one branch, so there is no stack to publish"),
    );
    await publishStack(false);

    expect(app.error).toContain("no stack to publish");
    expect(app.publishedStack).toBeNull();
    expect(app.publishing).toBe(false);
  });
});
