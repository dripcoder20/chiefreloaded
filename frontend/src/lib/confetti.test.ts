import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Options } from "canvas-confetti";

/**
 * The celebration is decoration, so the properties worth pinning down are the
 * ones that would make it a bug rather than a flourish: a canvas that eats
 * clicks, an animation that outstays two seconds or loops, and — above all — a
 * failure that surfaces where a pull request link should be.
 */

const HARD_LIMIT_MS = 2000;

/**
 * Lets the real library run under jsdom, which has no 2d context to give it.
 * Every context method is a no-op, which is enough for canvas-confetti to build
 * and mount its canvas — the part these tests are about.
 */
function stubCanvasContext(): void {
  const context = new Proxy({}, { get: () => () => {} });
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(
    context as unknown as CanvasRenderingContext2D,
  );
}

function canvases(): HTMLCanvasElement[] {
  return Array.from(document.querySelectorAll("canvas"));
}

beforeEach(() => {
  vi.resetModules();
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
  document.body.innerHTML = "";
});

describe("fireConfetti with the real library", () => {
  it("mounts a full-window canvas that takes no pointer events, then removes it", async () => {
    stubCanvasContext();
    const { fireConfetti } = await import("./confetti");

    fireConfetti();
    await vi.waitUntil(() => canvases().length > 0);

    const [canvas] = canvases();
    expect(canvas.style.position).toBe("fixed");
    expect(canvas.style.top).toBe("0px");
    expect(canvas.style.left).toBe("0px");
    // FR-8: every control underneath stays clickable while the burst plays.
    expect(canvas.style.pointerEvents).toBe("none");
    expect(canvas.width).toBe(document.documentElement.clientWidth);
    expect(canvas.height).toBe(document.documentElement.clientHeight);

    // FR-9: the burst ends, and leaves nothing of itself behind.
    await vi.waitUntil(() => canvases().length === 0, { timeout: HARD_LIMIT_MS });
  });
});

describe("fireConfetti options", () => {
  const confetti = vi.hoisted(() => {
    const spy = vi.fn((_options?: Options) => Promise.resolve(undefined));
    return Object.assign(spy, { reset: vi.fn() });
  });

  beforeEach(() => {
    vi.doMock("canvas-confetti", () => ({ default: confetti }));
    confetti.mockClear();
    confetti.reset.mockClear();
  });

  it("fires exactly one burst per call", async () => {
    const { fireConfetti } = await import("./confetti");

    fireConfetti();
    await vi.waitUntil(() => confetti.mock.calls.length > 0);
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(confetti).toHaveBeenCalledTimes(1);
  });

  it("asks for a finite burst that cannot outlast two seconds of frames", async () => {
    const { fireConfetti } = await import("./confetti");

    fireConfetti();
    await vi.waitUntil(() => confetti.mock.calls.length > 0);

    const [options] = confetti.mock.calls[0];
    // 60fps is the ceiling a browser animates at, so ticks below 120 cannot
    // stretch past the two second budget on a machine keeping up.
    expect(options?.ticks).toBeLessThan(120);
    expect(Number.isFinite(options?.ticks)).toBe(true);
  });

  it("stops the animation by the deadline even if frames are slow", async () => {
    const { fireConfetti } = await import("./confetti");
    vi.useFakeTimers();

    fireConfetti();
    await vi.advanceTimersByTimeAsync(HARD_LIMIT_MS);

    expect(confetti.reset).toHaveBeenCalled();
  });
});

describe("fireConfetti when the celebration cannot be drawn", () => {
  it("returns silently when the library throws", async () => {
    const confetti = Object.assign(
      vi.fn(() => {
        throw new Error("no canvas for you");
      }),
      { reset: vi.fn() },
    );
    vi.doMock("canvas-confetti", () => ({ default: confetti }));
    const { fireConfetti } = await import("./confetti");

    expect(() => fireConfetti()).not.toThrow();
    await vi.waitUntil(() => confetti.mock.calls.length > 0);
  });

  it("returns silently when the library will not load", async () => {
    vi.doMock("canvas-confetti", () => {
      throw new Error("chunk load failed");
    });
    const { fireConfetti } = await import("./confetti");

    expect(() => fireConfetti()).not.toThrow();
    // An unhandled rejection from the import would fail the run, so surviving a
    // turn of the microtask queue is the assertion.
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
});
