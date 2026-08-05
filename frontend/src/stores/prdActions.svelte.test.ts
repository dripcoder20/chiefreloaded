import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * US-006 — deleting a PRD asks first and only removes the sidebar entry once the
 * backend confirms; opening a PRD's markdown file goes to the operating system
 * and must not start an authoring session or change the active tab.
 */

const backend = vi.hoisted(() => ({
  del: vi.fn(),
  openFile: vi.fn(),
  list: vi.fn(),
  get: vi.fn(),
  authorStart: vi.fn(),
}));

vi.mock("../platform", async () => {
  const actual = await vi.importActual<typeof import("../platform")>("../platform");
  return {
    ...actual,
    api: {
      prd: {
        delete: (...a: unknown[]) => backend.del(...a),
        openFile: (...a: unknown[]) => backend.openFile(...a),
        list: (...a: unknown[]) => backend.list(...a),
        get: (...a: unknown[]) => backend.get(...a),
      },
      author: { start: (...a: unknown[]) => backend.authorStart(...a) },
    },
  };
});

import {
  app,
  confirmDeletePrd,
  cancelDeletePrd,
  deletePrd,
  openPrdFile,
} from "./app.svelte";

const PRDS = [
  { name: "checkout", completed: 1, total: 3 },
  { name: "docs-site", completed: 2, total: 6 },
];

beforeEach(() => {
  vi.clearAllMocks();
  app.error = null;
  app.pendingDelete = null;
  app.view = "stories";
  app.selectedPrd = "checkout";
  app.prds = PRDS as never;
  backend.list.mockResolvedValue(PRDS);
  backend.del.mockResolvedValue(undefined);
  backend.openFile.mockResolvedValue(undefined);
});

describe("Delete PRD", () => {
  it("asks before removing anything, naming the target", () => {
    confirmDeletePrd("docs-site");
    expect(app.pendingDelete).toBe("docs-site");
    expect(backend.del).not.toHaveBeenCalled();
  });

  it("leaves the PRD untouched when the dialog is cancelled", () => {
    confirmDeletePrd("docs-site");
    cancelDeletePrd();
    expect(app.pendingDelete).toBeNull();
    expect(backend.del).not.toHaveBeenCalled();
  });

  it("removes the PRD and clears a selection that pointed at it", async () => {
    backend.list.mockResolvedValue([PRDS[1]]);
    confirmDeletePrd("checkout");
    await deletePrd("checkout");

    expect(backend.del).toHaveBeenCalledWith("checkout");
    expect(app.pendingDelete).toBeNull();
    expect(app.selectedPrd).toBeNull();
    expect(app.prds.map((p) => p.name)).toEqual(["docs-site"]);
  });

  it("deletes the PRD the dialog names, not the selected one", async () => {
    app.selectedPrd = "checkout";
    confirmDeletePrd("docs-site");
    await deletePrd("docs-site");

    expect(backend.del).toHaveBeenCalledWith("docs-site");
    expect(app.selectedPrd).toBe("checkout");
  });

  // A refusal — the PRD is running, or has an authoring session open — must
  // explain itself and leave the rail exactly as it was.
  it("keeps the sidebar entry and shows the reason when deletion is refused", async () => {
    backend.del.mockRejectedValue(
      new Error('"checkout" is being implemented. Stop the run before deleting it.'),
    );
    await deletePrd("checkout");

    expect(app.error).toContain("Stop the run before deleting it");
    expect(app.prds.map((p) => p.name)).toEqual(["checkout", "docs-site"]);
    expect(app.selectedPrd).toBe("checkout");
  });

  it("reports a deletion failure and restores the list from disk", async () => {
    backend.del.mockRejectedValue(new Error("permission denied"));
    await deletePrd("docs-site");

    expect(app.error).toContain("permission denied");
    expect(backend.list).toHaveBeenCalled();
    expect(app.prds).toHaveLength(2);
  });
});

describe("Open markdown file", () => {
  it("asks the operating system to open the targeted file", async () => {
    await openPrdFile("docs-site");
    expect(backend.openFile).toHaveBeenCalledWith("docs-site");
  });

  it("starts no authoring session and does not change the active tab", async () => {
    app.view = "stories";
    await openPrdFile("docs-site");

    expect(backend.authorStart).not.toHaveBeenCalled();
    expect(app.view).toBe("stories");
    expect(app.selectedPrd).toBe("checkout");
  });

  it("reports an unreadable file with an error that identifies it", async () => {
    backend.openFile.mockRejectedValue(
      new Error('the markdown file for "docs-site" cannot be read'),
    );
    await openPrdFile("docs-site");

    expect(app.error).toContain("docs-site");
    expect(app.error).toContain("cannot be read");
  });

  it("reports having no application associated with markdown", async () => {
    backend.openFile.mockRejectedValue(new Error("no application is associated with .md files"));
    await openPrdFile("checkout");
    expect(app.error).toContain("no application is associated");
  });
});
