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
