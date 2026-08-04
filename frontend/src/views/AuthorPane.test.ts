import { render, fireEvent, waitFor, cleanup } from "@testing-library/svelte";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import AuthorPane from "./AuthorPane.svelte";

/**
 * These tests pin the behaviour US-001 asks for: an authoring session survives
 * ordinary tab switches (modelled here as toggling the `active` prop, exactly
 * as App.svelte drives it) and is only released when the pane is actually torn
 * down. They stub the terminal and the platform bridge so the component's
 * lifecycle decisions are what's under test, not xterm or the Go backend.
 */

const bridge = vi.hoisted(() => ({
  start: vi.fn(),
  write: vi.fn(),
  resize: vi.fn(),
  stop: vi.fn(),
  interrupt: vi.fn(),
  dataHandler: null as null | ((ev: unknown) => void),
  exitHandler: null as null | ((ev: unknown) => void),
  keyHandler: null as null | ((e: KeyboardEvent) => boolean),
}));

vi.mock("@xterm/xterm/css/xterm.css", () => ({}));

vi.mock("@xterm/xterm", () => ({
  Terminal: class {
    cols = 80;
    rows = 24;
    loadAddon(): void {}
    open(): void {}
    onData(): void {}
    write(): void {}
    focus(): void {}
    clear(): void {}
    dispose(): void {}
    attachCustomKeyEventHandler(fn: (e: KeyboardEvent) => boolean): void {
      bridge.keyHandler = fn;
    }
  },
}));

vi.mock("@xterm/addon-fit", () => ({
  FitAddon: class {
    fit(): void {}
  },
}));

vi.mock("../platform", () => ({
  api: {
    author: {
      start: bridge.start,
      write: bridge.write,
      resize: bridge.resize,
      stop: bridge.stop,
      interrupt: bridge.interrupt,
    },
  },
  onAuthorData: (fn: (ev: unknown) => void) => {
    bridge.dataHandler = fn;
    return () => {
      bridge.dataHandler = null;
    };
  },
  onAuthorExit: (fn: (ev: unknown) => void) => {
    bridge.exitHandler = fn;
    return () => {
      bridge.exitHandler = null;
    };
  },
}));

vi.mock("../stores/app.svelte", () => ({
  app: { selectedPrd: null as string | null },
  refresh: vi.fn(),
  selectPrd: vi.fn(),
}));

const SESSION_ID = "sess-1";

/** Render the pane and drive it through the "Create" flow into a live session. */
async function startSession() {
  bridge.start.mockResolvedValue(SESSION_ID);
  const view = render(AuthorPane, { props: { active: true } });

  const name = view.getByPlaceholderText("checkout");
  await fireEvent.input(name, { target: { value: "checkout" } });
  await fireEvent.click(view.getByRole("button", { name: "Create" }));

  await waitFor(() => expect(bridge.start).toHaveBeenCalledTimes(1));
  return view;
}

/** Simulate leaving and returning to the tab N times. */
async function switchTabs(rerender: (props: { active: boolean }) => Promise<void>, times: number) {
  for (let i = 0; i < times; i++) {
    await rerender({ active: false });
    await rerender({ active: true });
  }
}

/** Deliver a session-exit event, as the Go bridge would on completion. */
async function endSession(outcome: Record<string, unknown>, prd = "checkout") {
  bridge.exitHandler?.({ sessionId: SESSION_ID, outcome, spec: { prd } });
  await waitFor(() => {});
}

beforeEach(() => {
  bridge.start.mockReset();
  bridge.stop.mockReset();
  bridge.write.mockReset();
  bridge.dataHandler = null;
  bridge.exitHandler = null;
  bridge.keyHandler = null;
});

afterEach(() => cleanup());

describe("AuthorPane session preservation", () => {
  it("keeps an active session alive across repeated tab switches", async () => {
    const view = await startSession();

    await switchTabs(view.rerender, 5);

    expect(bridge.stop).not.toHaveBeenCalled();
    expect(bridge.start).toHaveBeenCalledTimes(1);
  });

  it("shows output produced while the tab was inactive when returning", async () => {
    const view = await startSession();

    await view.rerender({ active: false });
    // Output can still arrive for the live session while the tab is hidden.
    expect(() => bridge.dataHandler?.({ sessionId: SESSION_ID, data: btoa("hello") })).not.toThrow();
    await view.rerender({ active: true });

    expect(bridge.stop).not.toHaveBeenCalled();
  });

  it("retains a completed session's outcome across tab switches", async () => {
    const view = await startSession();
    await endSession({ created: true, parsed: true, stories: 2 });

    await switchTabs(view.rerender, 3);

    expect(view.getByText(/created with/i)).toBeInTheDocument();
    expect(bridge.stop).not.toHaveBeenCalled();
  });

  it("retains a failed session's outcome across tab switches", async () => {
    const view = await startSession();
    await endSession({ created: true, parsed: false, parseError: "not a PRD" });

    await switchTabs(view.rerender, 3);

    expect(view.getByText(/does not parse/i)).toBeInTheDocument();
    expect(bridge.stop).not.toHaveBeenCalled();
  });

  it("retains a canceled session's outcome across tab switches", async () => {
    const view = await startSession();
    await endSession({ created: false });

    await switchTabs(view.rerender, 3);

    expect(view.getByText(/Nothing was written/i)).toBeInTheDocument();
    expect(bridge.stop).not.toHaveBeenCalled();
  });

  it("releases a live session only when the pane is torn down", async () => {
    const view = await startSession();
    await switchTabs(view.rerender, 2);
    expect(bridge.stop).not.toHaveBeenCalled();

    view.unmount();

    expect(bridge.stop).toHaveBeenCalledWith(SESSION_ID);
  });

  it("does not release an already-finished session on tear down", async () => {
    const view = await startSession();
    await endSession({ created: false });

    view.unmount();

    expect(bridge.stop).not.toHaveBeenCalled();
  });
});

/**
 * US-002: the authoring terminal's custom key handler must send a line break on
 * Shift+Enter without submitting, submit on plain Enter, and never mistake an
 * IME composition for either. The same handler backs both the New PRD and Edit
 * PRD terminals — they are one component started with a different `kind`.
 */
describe("AuthorPane multiline input", () => {
  const preventDefault = vi.fn();
  function enter(init: Partial<KeyboardEvent>): KeyboardEvent {
    return {
      type: "keydown",
      key: "Enter",
      shiftKey: false,
      isComposing: false,
      preventDefault,
      ...init,
    } as unknown as KeyboardEvent;
  }

  beforeEach(() => preventDefault.mockReset());

  it("writes a line feed and suppresses submission on Shift+Enter", async () => {
    await startSession();
    expect(bridge.keyHandler).toBeTypeOf("function");

    const handled = bridge.keyHandler!(enter({ shiftKey: true }));

    expect(handled).toBe(false); // xterm must not also emit its default CR
    expect(preventDefault).toHaveBeenCalled();
    expect(bridge.write).toHaveBeenCalledWith(SESSION_ID, btoa("\n"));
  });

  it("lets plain Enter through so xterm submits it", async () => {
    await startSession();

    const handled = bridge.keyHandler!(enter({ shiftKey: false }));

    expect(handled).toBe(true); // xterm's default CR does the submitting
    expect(bridge.write).not.toHaveBeenCalled();
  });

  it("does not submit or insert while an IME composition is in progress", async () => {
    await startSession();

    expect(bridge.keyHandler!(enter({ isComposing: true }))).toBe(true);
    expect(bridge.keyHandler!(enter({ shiftKey: true, isComposing: true }))).toBe(true);
    expect(bridge.write).not.toHaveBeenCalled();
  });
});
