import "@testing-library/jest-dom/vitest";

// jsdom has no ResizeObserver, which some views wire up to drive terminal
// sizing. A no-op stub is enough: the tests drive visibility explicitly.
class ResizeObserverStub {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}
globalThis.ResizeObserver = ResizeObserverStub as unknown as typeof ResizeObserver;

/**
 * jsdom implements no pointer-capture API. The stub records which pointers are
 * captured, so a test can assert a drag releases what it took — a capture that
 * is never released routes every subsequent pointer event to the captured
 * element, which in a real window looks like a stuck cursor.
 */
const captured = new WeakMap<Element, Set<number>>();

Element.prototype.setPointerCapture = function (pointerId: number): void {
  const set = captured.get(this) ?? new Set<number>();
  set.add(pointerId);
  captured.set(this, set);
};

Element.prototype.releasePointerCapture = function (pointerId: number): void {
  captured.get(this)?.delete(pointerId);
};

Element.prototype.hasPointerCapture = function (pointerId: number): boolean {
  return captured.get(this)?.has(pointerId) ?? false;
};
