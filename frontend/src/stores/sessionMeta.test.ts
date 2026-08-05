import { describe, expect, it } from "vitest";
import {
  isImplementing,
  metaText,
  metaValue,
  storyMeta,
  RESOLVING_LABEL,
  UNAVAILABLE_LABEL,
  type SessionMetaRun,
} from "./sessionMeta";

/**
 * US-013 — the agent and model shown against an in-progress story come from the
 * session that is running it, and the three display states (resolved,
 * Resolving…, Unavailable) are never collapsed into one another.
 */

function run(over: Partial<SessionMetaRun> = {}): SessionMetaRun {
  return {
    id: "run_1",
    prd: "checkout",
    state: "running",
    storyId: "US-002",
    provider: "Claude",
    model: "claude-opus-4-8",
    ...over,
  };
}

describe("storyMeta", () => {
  it("reports the agent and model the session is using", () => {
    const meta = storyMeta(run(), "US-002");
    expect(meta).toEqual({
      agent: { kind: "value", text: "Claude" },
      model: { kind: "value", text: "claude-opus-4-8" },
    });
  });

  it("stays visible through starting, pausing, paused and stopping", () => {
    for (const state of ["idle", "running", "paused", "awaiting-answer"]) {
      expect(storyMeta(run({ state }), "US-002"), state).not.toBeNull();
    }
  });

  // The indicator is live state, not a record of which agent ran a finished
  // story — this story deliberately adds no execution history.
  it("is removed once the story has no active session", () => {
    for (const state of ["complete", "stopped", "error"]) {
      expect(storyMeta(run({ state }), "US-002"), state).toBeNull();
    }
  });

  it("shows nothing against a story the session is not implementing", () => {
    expect(storyMeta(run(), "US-001")).toBeNull();
    expect(storyMeta(run({ storyId: undefined }), "US-002")).toBeNull();
    expect(storyMeta(null, "US-002")).toBeNull();
  });

  // A model that has not arrived yet must not be shown as a default that could
  // be wrong, nor as an absence the provider never intended.
  it("shows Resolving… while the model is unknown and not declared missing", () => {
    const meta = storyMeta(run({ model: undefined }), "US-002");
    expect(meta!.model).toEqual({ kind: "resolving" });
    expect(metaText(meta!.model)).toBe(RESOLVING_LABEL);
  });

  it("shows Unavailable once the session reports no model, keeping the agent", () => {
    const meta = storyMeta(run({ model: undefined, modelUnavailable: true }), "US-002");
    expect(meta!.model).toEqual({ kind: "unavailable" });
    expect(metaText(meta!.model)).toBe(UNAVAILABLE_LABEL);
    // One field being unavailable must never hide the other.
    expect(meta!.agent).toEqual({ kind: "value", text: "Claude" });
  });

  it("shows Resolving… for an agent that has not resolved, keeping the model", () => {
    const meta = storyMeta(run({ provider: undefined }), "US-002");
    expect(meta!.agent).toEqual({ kind: "resolving" });
    expect(meta!.model).toEqual({ kind: "value", text: "claude-opus-4-8" });
  });

  // A retry or restart is a new run object with its own metadata, so a value
  // left over from the superseded session cannot be shown.
  it("follows the current session across a restart rather than the old one", () => {
    const stale = run({ id: "run_1", model: "old-model", state: "error" });
    expect(storyMeta(stale, "US-002")).toBeNull();

    const fresh = run({ id: "run_2", model: "new-model" });
    expect(storyMeta(fresh, "US-002")!.model).toEqual({ kind: "value", text: "new-model" });
  });

  // Remounting reads the same run, so the values come back unchanged without
  // anything being started, paused or resumed.
  it("is reconstructed identically from the same run", () => {
    const r = run();
    expect(storyMeta(r, "US-002")).toEqual(storyMeta(r, "US-002"));
  });
});

describe("metaValue and metaText", () => {
  it("prefers a reported value over either fallback", () => {
    expect(metaValue("gpt-5", true)).toEqual({ kind: "value", text: "gpt-5" });
  });

  it("distinguishes unavailable from unresolved", () => {
    expect(metaValue(undefined, true)).toEqual({ kind: "unavailable" });
    expect(metaValue(undefined, false)).toEqual({ kind: "resolving" });
  });

  // Values must be readable text, not conveyed by icon or colour alone.
  it("renders every state as text", () => {
    expect(metaText({ kind: "value", text: "gpt-5" })).toBe("gpt-5");
    expect(metaText({ kind: "resolving" })).toBe(RESOLVING_LABEL);
    expect(metaText({ kind: "unavailable" })).toBe(UNAVAILABLE_LABEL);
  });
});

describe("isImplementing", () => {
  it("matches only the story the live session is working on", () => {
    expect(isImplementing(run(), "US-002")).toBe(true);
    expect(isImplementing(run(), "US-003")).toBe(false);
    expect(isImplementing(run({ state: "complete" }), "US-002")).toBe(false);
  });
});
