import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * US-005 — Edit PRD opens a conversational editing session for the PRD its menu
 * targeted, labels the tab `Edit PRD`, and refuses to open a tab at all for a
 * PRD that cannot be read (which would otherwise create an empty replacement).
 */

const backend = vi.hoisted(() => ({
  get: vi.fn(),
  authorStart: vi.fn(),
}));

vi.mock("../platform", async () => {
  const actual = await vi.importActual<typeof import("../platform")>("../platform");
  return {
    ...actual,
    api: {
      prd: { get: (...a: unknown[]) => backend.get(...a) },
      author: { start: (...a: unknown[]) => backend.authorStart(...a) },
    },
  };
});

import { app, editPrd, requestNewPRD } from "./app.svelte";

const DETAIL = { name: "checkout", stories: [{ id: "US-001", status: "todo" }] };

beforeEach(() => {
  vi.clearAllMocks();
  app.error = null;
  app.view = "stories";
  app.questions = [];
  app.selectedPrd = null;
  app.authorTarget = null;
  backend.get.mockResolvedValue(DETAIL);
});

describe("editPrd", () => {
  it("targets the named PRD and opens the authoring tab", async () => {
    await editPrd("checkout");

    expect(app.authorTarget).toEqual({ kind: "edit", prd: "checkout" });
    expect(app.view).toBe("author");
    expect(app.selectedPrd).toBe("checkout");
  });

  it("targets the PRD it was given, not the one already selected", async () => {
    app.selectedPrd = "docs-site";
    await editPrd("checkout");
    expect(app.authorTarget).toEqual({ kind: "edit", prd: "checkout" });
  });

  // Opening a tab for a PRD that cannot be read would let the session go on to
  // write an empty replacement over the top of it.
  it("reports an unreadable PRD and opens no authoring tab", async () => {
    backend.get.mockRejectedValue(new Error("no PRD named \"ghost\""));
    await editPrd("ghost");

    expect(app.error).toContain("ghost");
    expect(app.view).toBe("stories");
    expect(app.authorTarget).toBeNull();
    expect(backend.authorStart).not.toHaveBeenCalled();
  });

  // Merely opening Edit PRD must not touch the file; the session's own explicit
  // save point is the only thing that writes.
  it("starts no session and writes nothing by itself", async () => {
    await editPrd("checkout");
    expect(backend.authorStart).not.toHaveBeenCalled();
  });

  // Requesting a new PRD opens the dialog; it does not re-point the
  // conversation, which still belongs to whatever PRD it was started for.
  it("opens the New PRD dialog without disturbing an open conversation", async () => {
    await editPrd("checkout");
    requestNewPRD();

    expect(app.newPrdOpen).toBe(true);
    expect(app.authorTarget).toEqual({ kind: "edit", prd: "checkout" });
  });
});
