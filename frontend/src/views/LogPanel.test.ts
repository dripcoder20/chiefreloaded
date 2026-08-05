import { render, fireEvent, cleanup } from "@testing-library/svelte";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import LogPanel from "./LogPanel.svelte";

/**
 * The resize handle's drag lifecycle. A drag that fails to clean up is not a
 * cosmetic bug: an unreleased pointer capture routes every later pointer event
 * to the handle, which presents as a stuck cursor and an unusable window.
 */

vi.mock("../stores/app.svelte", () => ({
  app: { selectedPrd: "checkout", currentRun: null },
  toolArgument: () => "",
}));

vi.mock("../stores/logs.svelte", () => ({
  logFor: () => [],
  droppedFor: () => 0,
  storiesFor: () => [],
}));

function handle(): HTMLElement {
  return document.querySelector<HTMLElement>('[aria-label="Resize the log"]')!;
}

function panelHeight(): number {
  const el = document.querySelector<HTMLElement>(".panel")!;
  return parseInt(el.style.height, 10);
}

/** Drag the handle from y1 to y2 without releasing. */
async function dragTo(y1: number, y2: number): Promise<void> {
  await fireEvent.pointerDown(handle(), { clientY: y1, pointerId: 1 });
  await fireEvent.pointerMove(window, { clientY: y2, pointerId: 1 });
}

beforeEach(() => localStorage.clear());
afterEach(cleanup);

describe("resizing the log panel", () => {
  it("grows the panel when dragged upward", async () => {
    render(LogPanel);
    const before = panelHeight();
    await dragTo(500, 400);
    expect(panelHeight()).toBeGreaterThan(before);
  });

  // The bug: releasePointerCapture was read off the pointerdown event's
  // currentTarget, which the DOM resets to null once dispatch ends. The throw
  // skipped both removeEventListener calls and the release itself.
  it("releases the pointer capture it took", async () => {
    render(LogPanel);
    await dragTo(500, 400);
    expect(handle().hasPointerCapture(1)).toBe(true);

    await fireEvent.pointerUp(window, { clientY: 400, pointerId: 1 });
    expect(handle().hasPointerCapture(1)).toBe(false);
  });

  it("stops resizing once the pointer is released", async () => {
    render(LogPanel);
    await dragTo(500, 400);
    await fireEvent.pointerUp(window, { clientY: 400, pointerId: 1 });

    const settled = panelHeight();
    // A leaked pointermove listener would keep resizing after the drag ended.
    await fireEvent.pointerMove(window, { clientY: 100, pointerId: 1 });
    expect(panelHeight()).toBe(settled);
  });

  // A drag can also end by cancellation — a system gesture, or the window
  // losing the pointer — which must clean up exactly as a release does.
  it("cleans up when the drag is cancelled", async () => {
    render(LogPanel);
    await dragTo(500, 400);
    await fireEvent.pointerCancel(window, { clientY: 400, pointerId: 1 });

    const settled = panelHeight();
    await fireEvent.pointerMove(window, { clientY: 100, pointerId: 1 });
    expect(panelHeight()).toBe(settled);
    expect(handle().hasPointerCapture(1)).toBe(false);
  });

  it("survives repeated drags without accumulating listeners", async () => {
    render(LogPanel);
    for (let i = 0; i < 5; i++) {
      await dragTo(500, 450);
      await fireEvent.pointerUp(window, { clientY: 450, pointerId: 1 });
    }
    const settled = panelHeight();
    await fireEvent.pointerMove(window, { clientY: 100, pointerId: 1 });
    expect(panelHeight()).toBe(settled);
  });
});
