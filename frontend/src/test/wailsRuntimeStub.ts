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
export const Window = {};
export const Browser = { OpenURL: (): void => {}, OpenFile: (): void => {} };
export const Dialogs = {};
export const System = { IsMac: (): boolean => true };
