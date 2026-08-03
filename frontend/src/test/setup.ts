import "@testing-library/jest-dom/vitest";

// jsdom has no ResizeObserver, which AuthorPane wires up to drive terminal
// sizing. A no-op stub is enough: the tests drive visibility explicitly.
class ResizeObserverStub {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}
globalThis.ResizeObserver = ResizeObserverStub as unknown as typeof ResizeObserver;
