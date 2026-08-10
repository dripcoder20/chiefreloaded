import { beforeEach, describe, expect, it, vi } from "vitest";

import { STORAGE_KEY } from "./celebration.svelte";

/**
 * The preference is read once, when the module is first imported, so every test
 * that cares about a *stored* value has to seed localStorage and then load a
 * fresh copy of the module.
 */
async function loadFresh() {
  vi.resetModules();
  return await import("./celebration.svelte");
}

beforeEach(() => localStorage.clear());

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
