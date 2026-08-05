import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * Settings is global project configuration, not a per-PRD working context, so
 * it is a dialog rather than a third tab alongside Stories and New PRD.
 */

vi.mock("../platform", async () => {
  const actual = await vi.importActual<typeof import("../platform")>("../platform");
  return { ...actual, api: {} };
});

import { app, closeSettings, toggleSettings } from "./app.svelte";

beforeEach(() => {
  app.settingsOpen = false;
  app.questions = [];
  app.view = "stories";
});

describe("the settings dialog", () => {
  it("toggles open and shut", () => {
    toggleSettings();
    expect(app.settingsOpen).toBe(true);
    toggleSettings();
    expect(app.settingsOpen).toBe(false);
  });

  it("closes explicitly", () => {
    toggleSettings();
    closeSettings();
    expect(app.settingsOpen).toBe(false);
  });

  // Opening settings must not take the user off whatever they were watching.
  it("leaves the current view alone", () => {
    app.view = "author";
    toggleSettings();
    expect(app.view).toBe("author");
  });

  // A native accelerator fires regardless of what the webview is showing, and a
  // question blocks a run — stacking a dialog over it helps nobody.
  it("is ignored while a question is waiting", () => {
    app.questions = [{ id: "q1", title: "Where should this run commit?" }] as never;
    toggleSettings();
    expect(app.settingsOpen).toBe(false);
  });
});
