import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { EventKind, type LoopEvent } from "../platform";

/**
 * The engine writes a story's in-progress status into prd.md as it builds the
 * prompt, and nothing watches that file. The story list therefore only reflects
 * what is running if the story-started event pulls the document again.
 */

const backend = vi.hoisted(() => ({
  list: vi.fn(),
  get: vi.fn(),
}));

// connect() subscribes to onEvents; capturing the handler is how a test feeds
// the store real events without exporting apply() purely for testing.
let deliver: ((events: LoopEvent[]) => void) | null = null;

vi.mock("../platform", async () => {
  const actual = await vi.importActual<typeof import("../platform")>("../platform");
  return {
    ...actual,
    api: {
      prd: {
        list: (...a: unknown[]) => backend.list(...a),
        get: (...a: unknown[]) => backend.get(...a),
      },
      project: { getConfig: async () => ({}) },
      run: { snapshot: async () => ({ runs: [], prds: [], questions: [] }) },
    },
    onReady: () => () => {},
    onMenuNewPRD: () => () => {},
    onEvents: (handler: (events: LoopEvent[]) => void) => {
      deliver = handler;
      return () => {};
    },
  };
});

import { app, connect, disconnect } from "./app.svelte";

const TODO = { name: "checkout", stories: [{ id: "US-001", status: "todo" }] };
const RUNNING = { name: "checkout", stories: [{ id: "US-001", status: "in-progress" }] };

function ev(kind: EventKind, over: Partial<LoopEvent> = {}): LoopEvent {
  return { seq: 1, at: Date.now(), kind, prd: "checkout", ...over } as LoopEvent;
}

beforeEach(async () => {
  vi.clearAllMocks();
  backend.list.mockResolvedValue([{ name: "checkout" }]);
  backend.get.mockResolvedValue(RUNNING);
  await connect();
  vi.clearAllMocks();
  app.selectedPrd = "checkout";
  app.detail = TODO as never;
  backend.list.mockResolvedValue([{ name: "checkout" }]);
  backend.get.mockResolvedValue(RUNNING);
});

afterEach(() => disconnect());

describe("story progress", () => {
  it("re-reads the PRD when a story starts", async () => {
    deliver!([ev(EventKind.EvStoryStarted, { storyId: "US-001", text: "Persist orders" })]);
    await vi.waitFor(() => expect(backend.get).toHaveBeenCalledWith("checkout"));

    expect(app.detail?.stories?.[0].status).toBe("in-progress");
  });

  it("still reports the story in the activity ticker", () => {
    deliver!([ev(EventKind.EvStoryStarted, { storyId: "US-001", text: "Persist orders" })]);
    expect(app.activity).toContain("US-001");
  });

  it("re-reads it again when the story finishes", async () => {
    deliver!([ev(EventKind.EvStoryDone, { storyId: "US-001" })]);
    await vi.waitFor(() => expect(backend.get).toHaveBeenCalled());
  });
});
