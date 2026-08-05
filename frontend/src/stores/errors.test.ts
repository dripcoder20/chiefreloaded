import { describe, expect, it } from "vitest";
import { errorMessage } from "./errors";

/**
 * Wails rejects a failed binding with a RuntimeError, and String(err) renders
 * name + message — so every message the interface showed carried a class name
 * that means nothing outside this codebase.
 */
describe("errorMessage", () => {
  it("drops the transport's error name", () => {
    const err = new Error("No GitHub repo is set up for this project.");
    err.name = "RuntimeError";

    expect(String(err)).toContain("RuntimeError:");
    expect(errorMessage(err)).toBe("No GitHub repo is set up for this project.");
  });

  it("drops the prefix from an already-stringified value too", () => {
    expect(errorMessage("RuntimeError: no PRD named \"checkout\"")).toBe(
      'no PRD named "checkout"',
    );
  });

  it("keeps a plain Error's message", () => {
    expect(errorMessage(new Error("the run failed"))).toBe("the run failed");
  });

  // Our own messages lead with a provider or PRD name before a colon. Stripping
  // by a general "word before a colon" rule would eat that.
  it("keeps a leading clause that is not an error name", () => {
    expect(errorMessage(new Error("codex: malformed usage payload"))).toBe(
      "codex: malformed usage payload",
    );
  });

  it("falls back to stringifying anything that is not an Error", () => {
    expect(errorMessage("plain string")).toBe("plain string");
    expect(errorMessage(42)).toBe("42");
  });

  it("trims surrounding whitespace", () => {
    expect(errorMessage(new Error("  spaced  "))).toBe("spaced");
  });
});
