import { beforeEach, describe, expect, it, vi } from "vitest";

import { STORAGE_KEY } from "./celebration.svelte";

const fireConfetti = vi.hoisted(() => vi.fn());
vi.mock("../lib/confetti", () => ({ fireConfetti: () => fireConfetti() }));

/**
 * The preference is read once, when the module is first imported, so every test
 * that cares about a *stored* value has to seed localStorage and then load a
 * fresh copy of the module.
 */
async function loadFresh() {
  vi.resetModules();
  return await import("./celebration.svelte");
}

beforeEach(() => {
  localStorage.clear();
  fireConfetti.mockClear();
});

describe("the celebrate-on-publish preference", () => {
  it("is on when nothing has been stored", async () => {
    const { celebration } = await loadFresh();
    expect(celebration.isCelebrationEnabled).toBe(true);
  });

  it("does not write anything until the user expresses a choice", async () => {
    await loadFresh();
    expect(localStorage.getItem(STORAGE_KEY)).toBeNull();
  });

  it("remembers being turned off across a restart", async () => {
    const first = await loadFresh();
    first.celebration.isCelebrationEnabled = false;

    const restarted = await loadFresh();
    expect(restarted.celebration.isCelebrationEnabled).toBe(false);
  });

  it("remembers being turned back on across a restart", async () => {
    const first = await loadFresh();
    first.celebration.isCelebrationEnabled = false;
    first.celebration.isCelebrationEnabled = true;

    const restarted = await loadFresh();
    expect(restarted.celebration.isCelebrationEnabled).toBe(true);
  });

  it("occupies exactly one key", async () => {
    const { celebration } = await loadFresh();
    celebration.isCelebrationEnabled = false;
    expect(Object.keys(localStorage)).toEqual([STORAGE_KEY]);
  });

  it("toggles", async () => {
    const { celebration, toggleCelebration } = await loadFresh();
    toggleCelebration();
    expect(celebration.isCelebrationEnabled).toBe(false);
    toggleCelebration();
    expect(celebration.isCelebrationEnabled).toBe(true);
  });

  // A value nobody can interpret is not a request to stay quiet.
  it.each(["", "not json", "{", "null", '"false"', "0", "{}", "[true]"])(
    "falls back to on for the stored value %j",
    async (stored) => {
      localStorage.setItem(STORAGE_KEY, stored);
      const { celebration } = await loadFresh();
      expect(celebration.isCelebrationEnabled).toBe(true);
    },
  );

  it("survives a localStorage that throws on read", async () => {
    const getItem = vi
      .spyOn(Storage.prototype, "getItem")
      .mockImplementation(() => {
        throw new Error("access denied");
      });

    const { celebration } = await loadFresh();
    expect(celebration.isCelebrationEnabled).toBe(true);

    getItem.mockRestore();
  });

  it("survives a localStorage that throws on write", async () => {
    const { celebration } = await loadFresh();
    const setItem = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("quota exceeded");
    });

    expect(() => (celebration.isCelebrationEnabled = false)).not.toThrow();
    // The choice still holds for this session even though it could not be saved.
    expect(celebration.isCelebrationEnabled).toBe(false);

    setItem.mockRestore();
  });
});

describe("celebrating a publish", () => {
  it("fires once per call while the preference is on", async () => {
    const { celebratePublish } = await loadFresh();
    celebratePublish();
    expect(fireConfetti).toHaveBeenCalledTimes(1);
  });

  it("stays quiet while the preference is off", async () => {
    const { celebration, celebratePublish } = await loadFresh();
    celebration.isCelebrationEnabled = false;
    celebratePublish();
    expect(fireConfetti).not.toHaveBeenCalled();
  });

  // The preference is read at the moment of firing, not captured when the
  // publish started, so turning it off mid-publish takes effect.
  it("obeys a preference changed since the last celebration", async () => {
    const { celebration, celebratePublish } = await loadFresh();
    celebratePublish();
    celebration.isCelebrationEnabled = false;
    celebratePublish();
    celebration.isCelebrationEnabled = true;
    celebratePublish();

    expect(fireConfetti).toHaveBeenCalledTimes(2);
  });
});

const OPENED = {
  storyId: "US-001",
  branch: "loop/checkout/us-001",
  pr: {
    number: 128,
    url: "https://github.com/acme/checkout/pull/128",
    state: "OPEN",
    draft: false,
  },
};

describe("celebrating a published stack", () => {
  it("fires when every layer of the stack opened", async () => {
    const { celebrateStackPublish } = await loadFresh();
    celebrateStackPublish({
      prd: "checkout",
      stories: [OPENED, { ...OPENED, storyId: "US-002" }],
    });

    expect(fireConfetti).toHaveBeenCalledTimes(1);
  });

  // A story with nothing to publish published everything it had.
  it("fires when a layer contributed no pull request because it committed nothing", async () => {
    const { celebrateStackPublish } = await loadFresh();
    celebrateStackPublish({
      prd: "checkout",
      stories: [OPENED, { storyId: "US-002", skipped: "the story produced no commit" }],
    });

    expect(fireConfetti).toHaveBeenCalledTimes(1);
  });

  it("stays quiet when the pass stopped before it reached the top", async () => {
    const { celebrateStackPublish } = await loadFresh();
    celebrateStackPublish({
      prd: "checkout",
      failed: "US-002: gh pr create: could not reach github.com",
      stories: [
        OPENED,
        { storyId: "US-002", error: "gh pr create: could not reach github.com" },
        { storyId: "US-003", skipped: "the branch below it was not published" },
      ],
    });

    expect(fireConfetti).not.toHaveBeenCalled();
  });

  // Either half of a failure is enough on its own: the report's sentence and the
  // per-story error say the same thing, and neither is a stack to celebrate.
  it("stays quiet for a failed layer even with no failure sentence on the report", async () => {
    const { celebrateStackPublish } = await loadFresh();
    celebrateStackPublish({
      prd: "checkout",
      stories: [OPENED, { storyId: "US-002", error: "gh pr create: could not reach github.com" }],
    });

    expect(fireConfetti).not.toHaveBeenCalled();
  });

  // The stack was already complete before this press, and its confetti fired then.
  it("stays quiet for a retry that found everything already open", async () => {
    const { celebrateStackPublish } = await loadFresh();
    celebrateStackPublish({
      prd: "checkout",
      stories: [{ ...OPENED, alreadyOpen: true }],
    });

    expect(fireConfetti).not.toHaveBeenCalled();
  });

  it("fires for the retry that opens the last missing pull request", async () => {
    const { celebrateStackPublish } = await loadFresh();
    celebrateStackPublish({
      prd: "checkout",
      stories: [{ ...OPENED, alreadyOpen: true }, { ...OPENED, storyId: "US-002" }],
    });

    expect(fireConfetti).toHaveBeenCalledTimes(1);
  });

  it("stays quiet for a stack that produced no pull request at all", async () => {
    const { celebrateStackPublish } = await loadFresh();
    celebrateStackPublish({
      prd: "checkout",
      stories: [{ storyId: "US-001", skipped: "the story produced no commit" }],
    });

    expect(fireConfetti).not.toHaveBeenCalled();
  });

  it("stays quiet for a report with no stories in it", async () => {
    const { celebrateStackPublish } = await loadFresh();
    celebrateStackPublish({ prd: "checkout" });

    expect(fireConfetti).not.toHaveBeenCalled();
  });

  it("stays quiet while the preference is off", async () => {
    const { celebration, celebrateStackPublish } = await loadFresh();
    celebration.isCelebrationEnabled = false;
    celebrateStackPublish({ prd: "checkout", stories: [OPENED] });

    expect(fireConfetti).not.toHaveBeenCalled();
  });
});
