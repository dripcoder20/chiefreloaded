import { render, fireEvent, cleanup } from "@testing-library/svelte";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

/**
 * US-005 — the confetti switch.
 *
 * The switch is the only control on this screen that is not project
 * configuration, and the two ways that can go wrong are both asserted here: it
 * must survive with no project open, and it must never reach saveConfig, which
 * would write a personal preference into a file the whole team shares.
 */

const store = vi.hoisted(() => ({
  app: { config: null as unknown, project: null as unknown, error: null as unknown },
}));

const platform = vi.hoisted(() => ({
  api: {
    project: { saveConfig: vi.fn() },
    author: { getPrompt: vi.fn(), savePrompt: vi.fn(), resetPrompt: vi.fn() },
  },
}));

vi.mock("../stores/app.svelte", () => store);
vi.mock("../platform", () => platform);

import Settings from "./Settings.svelte";
import { celebration, STORAGE_KEY } from "../stores/celebration.svelte";

// Found through its label rather than through render()'s result: testing-library's
// Svelte render result does not narrow to the query helpers its own types expect.
function toggle(): HTMLInputElement {
  const label = [...document.querySelectorAll("label")].find((l) =>
    l.textContent?.includes("Confetti on publish"),
  );
  return label!.querySelector('input[type="checkbox"]')!;
}

beforeEach(() => {
  vi.clearAllMocks();
  platform.api.author.getPrompt.mockResolvedValue({ kind: "new", body: "", path: "" });
  // Reset the singleton first, then clear: the reset itself persists, and a test
  // that asserts what was written needs the key absent before it starts.
  celebration.isCelebrationEnabled = true;
  localStorage.clear();
});
afterEach(cleanup);

describe("the confetti switch", () => {
  it("is offered with no project open", () => {
    render(Settings);
    expect(toggle().checked).toBe(true);
  });

  it("shows the stored preference rather than the default", () => {
    celebration.isCelebrationEnabled = false;
    render(Settings);
    expect(toggle().checked).toBe(false);
  });

  it("turns the preference off when unchecked", async () => {
    render(Settings);
    await fireEvent.click(toggle());

    expect(celebration.isCelebrationEnabled).toBe(false);
    expect(localStorage.getItem(STORAGE_KEY)).toBe("false");
  });

  it("turns it back on when checked again", async () => {
    celebration.isCelebrationEnabled = false;
    render(Settings);
    await fireEvent.click(toggle());

    expect(celebration.isCelebrationEnabled).toBe(true);
    expect(localStorage.getItem(STORAGE_KEY)).toBe("true");
  });

  // The switch is a browser preference. Writing it to .chief/config.yaml would
  // put it in everyone's checkout and drag a config migration behind it.
  it("does not write the project config", async () => {
    render(Settings);
    await fireEvent.click(toggle());
    expect(platform.api.project.saveConfig).not.toHaveBeenCalled();
  });

  it("follows a preference changed elsewhere", async () => {
    render(Settings);
    celebration.isCelebrationEnabled = false;
    await Promise.resolve();
    expect(toggle().checked).toBe(false);
  });
});
