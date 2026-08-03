import { describe, expect, it } from "vitest";
import { resolveTerminalKey, CR, LF } from "./terminalInput";

/**
 * The classifier is the single place that decides what Enter means. These tests
 * pin the three cases US-002 cares about apart: plain Enter submits (xterm's
 * default CR), Shift+Enter inserts a line feed, and an Enter that is completing
 * an IME composition is neither — it must be left to the composition.
 */

function key(init: Partial<KeyboardEvent> & { type?: string }): KeyboardEvent {
  return { type: "keydown", key: "Enter", shiftKey: false, isComposing: false, ...init } as KeyboardEvent;
}

describe("resolveTerminalKey", () => {
  it("lets plain Enter fall through to xterm's default (submit)", () => {
    expect(resolveTerminalKey(key({ shiftKey: false }))).toEqual({ kind: "default" });
  });

  it("inserts a line feed for Shift+Enter without submitting", () => {
    expect(resolveTerminalKey(key({ shiftKey: true }))).toEqual({ kind: "insert", data: LF });
  });

  it("ignores Enter while an IME composition is in progress", () => {
    expect(resolveTerminalKey(key({ isComposing: true }))).toEqual({ kind: "default" });
    expect(resolveTerminalKey(key({ shiftKey: true, isComposing: true }))).toEqual({ kind: "default" });
  });

  it("does not touch non-Enter keys", () => {
    expect(resolveTerminalKey(key({ key: "a", shiftKey: true }))).toEqual({ kind: "default" });
  });

  it("only acts on keydown, not keyup or keypress", () => {
    expect(resolveTerminalKey(key({ type: "keyup", shiftKey: true }))).toEqual({ kind: "default" });
  });

  it("keeps CR and LF distinct", () => {
    expect(CR).toBe("\r");
    expect(LF).toBe("\n");
  });
});
