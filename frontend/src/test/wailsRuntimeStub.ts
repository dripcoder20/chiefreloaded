/**
 * A stand-in for @wailsio/runtime under test.
 *
 * The real package runs side effects on import — its drag module arms a timer
 * that touches `window`. Under Vitest that timer can outlive the jsdom
 * environment, and the resulting "window is not defined" is reported as an
 * unhandled error that fails the run while every test still passes. Nothing in
 * the suite exercises the runtime itself: the platform boundary is mocked
 * wherever behaviour matters, and tests that import it want only the generated
 * enums alongside it.
 */

/** Events.On returns an unsubscribe function; nothing here ever emits. */
export const Events = {
  On: (): (() => void) => () => {},
  Emit: (): void => {},
  Off: (): void => {},
};

export const Call = { ByID: async (): Promise<unknown> => undefined };

/**
 * The generated bindings build their decoders at module scope with these, so
 * they must exist merely to import a service — before any test decides whether
 * to mock it. Each returns an identity decoder: nothing here decodes real
 * traffic, since the platform boundary is mocked wherever behaviour matters.
 */
const identity = <T,>(v: T): T => v;

export const Create = {
  Any: identity,
  Map: () => identity,
  Array: () => identity,
  Struct: () => identity,
  Nullable: () => identity,
  ByteSlice: identity,
};

/** Bindings type their return values with this; tests never await a real one. */
export class CancellablePromise<T> extends Promise<T> {
  cancel(): void {}
}
export const Window = {};
export const Browser = { OpenURL: (): void => {}, OpenFile: (): void => {} };
export const Dialogs = {};
export const System = { IsMac: (): boolean => true };
