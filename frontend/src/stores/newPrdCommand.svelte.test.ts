import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Question } from "../platform";

/**
 * Pins US-003's New PRD command: the File ▸ New PRD menu item and its ⌘N /
 * Ctrl+N shortcut both route through `requestNewPRD`, which opens and focuses
 * the New PRD tab. The command must be idempotent — a single event delivered
 * more than once must not spawn a second authoring session — and must be
 * ignored while a modal question is awaiting an answer.
 */

// Spy on the session-starting bindings so the test can prove the command never
// begins a session itself.
const backend = vi.hoisted(() => ({
  authorStart: vi.fn(),
  runStart: vi.fn(),
}));

vi.mock("../platform", async () => {
  const actual = await vi.importActual<typeof import("../platform")>("../platform");
  return {
    ...actual,
    api: {
      author: { start: (...a: unknown[]) => backend.authorStart(...a) },
      run: { start: (...a: unknown[]) => backend.runStart(...a) },
    },
  };
});

import { app, requestNewPRD } from "./app.svelte";

beforeEach(() => {
  backend.authorStart.mockReset();
  backend.runStart.mockReset();
  app.questions = [];
  app.view = "stories";
});

describe("New PRD command (File ▸ New PRD / ⌘N)", () => {
  it("opens and focuses the New PRD tab", () => {
    requestNewPRD();
    expect(app.view).toBe("author");
  });

  it("is idempotent — repeat delivery keeps the tab and starts no session", () => {
    requestNewPRD();
    requestNewPRD();
    requestNewPRD();
    expect(app.view).toBe("author");
    expect(backend.authorStart).not.toHaveBeenCalled();
    expect(backend.runStart).not.toHaveBeenCalled();
  });

  it("is ignored while a modal question is awaiting a response", () => {
    app.questions = [{ id: "q1", title: "Pick one", options: [] } as unknown as Question];
    requestNewPRD();
    expect(app.view).toBe("stories");
  });
});
