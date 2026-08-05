import { beforeEach, describe, expect, it } from "vitest";
import { clear, droppedFor, flushNow, ingest, logFor, storiesFor } from "./logs.svelte";
import { EventKind, type LoopEvent } from "../platform";

/**
 * The log buffer's two contracts: it must hand back a value that actually
 * changes as events arrive (or every reader freezes at its first render), and
 * it must be able to answer "what did this one story do".
 */

let seq = 0;
function ev(over: Partial<LoopEvent> = {}): LoopEvent {
  seq += 1;
  return {
    seq,
    at: Date.now(),
    kind: EventKind.EvAgentText,
    prd: "checkout",
    text: `line ${seq}`,
    ...over,
  } as LoopEvent;
}

beforeEach(() => {
  clear();
  seq = 0;
});

describe("reactivity", () => {
  // Svelte's $derived compares by reference, so a buffer mutated in place would
  // leave the log frozen at whatever it first rendered.
  it("returns a new value once more events arrive", () => {
    ingest([ev()]);
    flushNow();
    const first = logFor("checkout");

    ingest([ev()]);
    flushNow();
    const second = logFor("checkout");

    expect(first.length).toBe(1);
    expect(second.length).toBe(2);
    expect(second).not.toBe(first);
  });

  it("serves repeated reads within one flush from the memo", () => {
    ingest([ev(), ev()]);
    flushNow();
    expect(logFor("checkout")).toBe(logFor("checkout"));
  });

  it("keeps a separate buffer per PRD", () => {
    ingest([ev({ prd: "checkout" }), ev({ prd: "docs-site" })]);
    flushNow();

    expect(logFor("checkout")).toHaveLength(1);
    expect(logFor("docs-site")).toHaveLength(1);
  });
});

describe("per-story scoping", () => {
  beforeEach(() => {
    ingest([
      ev({ storyId: "US-001" }),
      ev({ kind: EventKind.EvGit, text: "pushed" }), // run-level, no story
      ev({ storyId: "US-002" }),
      ev({ storyId: "US-001" }),
    ]);
    flushNow();
  });

  it("narrows to one story", () => {
    const first = logFor("checkout", "US-001");
    expect(first).toHaveLength(2);
    expect(first.every((e) => e.storyId === "US-001")).toBe(true);
  });

  it("shows everything when no story is given, run-level events included", () => {
    const all = logFor("checkout");
    expect(all).toHaveLength(4);
    expect(all.some((e) => !e.storyId)).toBe(true);
  });

  // Run-level chatter belongs to no story, so scoping to one must exclude it
  // rather than attaching it to whichever story happened to be running.
  it("excludes run-level events from a story view", () => {
    expect(logFor("checkout", "US-002").every((e) => e.storyId === "US-002")).toBe(true);
    expect(logFor("checkout", "US-002")).toHaveLength(1);
  });

  it("lists the stories that produced output, in first-seen order", () => {
    expect(storiesFor("checkout")).toEqual(["US-001", "US-002"]);
  });

  it("offers no story that produced nothing", () => {
    expect(storiesFor("checkout")).not.toContain("US-003");
  });

  it("re-filters as new events arrive for the selected story", () => {
    const before = logFor("checkout", "US-002");
    ingest([ev({ storyId: "US-002" })]);
    flushNow();
    const after = logFor("checkout", "US-002");

    expect(after.length).toBe(before.length + 1);
    expect(after).not.toBe(before);
  });
});

describe("dropped events", () => {
  it("reports a gap the backend declared", () => {
    ingest([ev({ dropped: 12 } as Partial<LoopEvent>)]);
    flushNow();
    expect(droppedFor("checkout")).toBe(12);
  });

  it("forgets a cleared PRD rather than serving its old view", () => {
    ingest([ev()]);
    flushNow();
    expect(logFor("checkout")).toHaveLength(1);

    clear("checkout");
    expect(logFor("checkout")).toHaveLength(0);
  });
});
