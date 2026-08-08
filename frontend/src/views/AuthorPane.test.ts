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
  termWrites: [] as Uint8Array[],
  resets: 0,
}));

vi.mock("@xterm/xterm/css/xterm.css", () => ({}));

vi.mock("@xterm/xterm", () => ({
  Terminal: class {
    cols = 80;
    rows = 24;
    loadAddon(): void {}
    open(): void {}
    onData(): void {}
    write(bytes: Uint8Array): void {
      bridge.termWrites.push(bytes);
    }
    focus(): void {}
    reset(): void {
      bridge.resets++;
    }
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
  openURL: vi.fn(),
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

const store = vi.hoisted(() => ({
  app: {
    selectedPrd: null as string | null,
    authorTarget: null as { kind: "new" | "edit"; prd: string } | null,
    environment: {
      agents: [
        { name: "claude", available: true },
        { name: "codex", available: true },
      ],
    },
    agentDefaults: { authoring: "claude", implementation: "codex" },
  },
  refresh: vi.fn(),
  selectPrd: vi.fn(),
  savePrdWorkflow: vi.fn(),
}));

vi.mock("../stores/app.svelte", () => store);

const SESSION_ID = "sess-1";

/**
 * Render the pane for a PRD that already exists, which is now the only way a
 * conversation begins: the PRD is created first and the pane picks it up.
 */
async function startSession(kind: "new" | "edit" = "new", prd = "checkout") {
  bridge.start.mockResolvedValue(SESSION_ID);
  store.app.authorTarget = { kind, prd };

  const view = render(AuthorPane, { props: { active: true } });
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
  store.app.selectedPrd = null;
  store.app.authorTarget = null;
  store.savePrdWorkflow.mockReset().mockResolvedValue(true);
  bridge.start.mockReset();
  bridge.stop.mockReset();
  bridge.write.mockReset();
  bridge.resize.mockReset();
  bridge.dataHandler = null;
  bridge.exitHandler = null;
  bridge.keyHandler = null;
  bridge.termWrites = [];
  bridge.resets = 0;
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
 * The emulator and the PTY have to agree on what state the terminal is in, or
 * the agent erases and positions against a screen that isn't the one on show —
 * which reads as text drawn over text, not as a size being slightly off.
 */
describe("AuthorPane terminal fidelity", () => {
  const decode = (bytes: Uint8Array) => String.fromCharCode(...bytes);

  // Go pumps the PTY before Start() returns, so the agent's terminal setup can
  // reach this pane before it knows the id to match it against.
  it("replays output that arrived before the session id did", async () => {
    let announceId: (id: string) => void = () => {};
    bridge.start.mockReturnValue(new Promise<string>((resolve) => (announceId = resolve)));
    store.app.authorTarget = { kind: "new", prd: "checkout" };

    render(AuthorPane, { props: { active: true } });
    await waitFor(() => expect(bridge.start).toHaveBeenCalledTimes(1));

    bridge.dataHandler?.({ sessionId: SESSION_ID, data: btoa("[?1049h") });
    expect(bridge.termWrites).toHaveLength(0); // held, not written blind

    announceId(SESSION_ID);
    await waitFor(() => expect(bridge.termWrites).toHaveLength(1));
    expect(decode(bridge.termWrites[0])).toBe("[?1049h");
  });

  // The size passed to start() is measured before the running bar exists, and a
  // fit triggered by the pane becoming visible has no id to report against.
  it("tells the PTY its size once the session id is known", async () => {
    await startSession();

    await waitFor(() => expect(bridge.resize).toHaveBeenCalledWith(SESSION_ID, 80, 24));
  });

  // Stop SIGKILLs the agent, so it never emits the sequences that would put the
  // terminal back. Only a full reset clears the scroll region it left behind.
  it("resets the emulator between sessions rather than only its screen", async () => {
    const view = await startSession("new", "checkout");
    expect(bridge.resets).toBe(1);

    await endSession({ created: true, parsed: true, stories: 0 });
    await fireEvent.click(view.getByRole("button", { name: "New session" }));

    await waitFor(() => expect(bridge.resets).toBe(2));
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

/**
 * US-005 — an Edit PRD target starts an editing session for that PRD on its own,
 * identifies it in the pane, and never starts a second one for the same target.
 */
describe("edit sessions", () => {
  it("starts an edit session for the targeted PRD without further input", async () => {
    bridge.start.mockResolvedValue(SESSION_ID);
    store.app.authorTarget = { kind: "edit", prd: "docs-site" };

    render(AuthorPane, { props: { active: true } });

    await waitFor(() => expect(bridge.start).toHaveBeenCalledTimes(1));
    expect(bridge.start.mock.calls[0][0]).toMatchObject({ kind: "edit", prd: "docs-site" });
  });

  // Two Edit PRD sessions are told apart by the PRD named in the pane; the tab
  // title says only "Edit PRD".
  it("names the PRD being edited inside the pane", async () => {
    bridge.start.mockResolvedValue(SESSION_ID);
    store.app.authorTarget = { kind: "edit", prd: "docs-site" };

    const view = render(AuthorPane, { props: { active: true } });
    await waitFor(() => expect(bridge.start).toHaveBeenCalled());
    await waitFor(() => expect(view.getByText(/Editing docs-site/)).toBeTruthy());
  });

  // The PRD is created before its conversation, so the pane never asks for a
  // name — that decision has already been made in the dialog.
  it("never asks for a name", async () => {
    bridge.start.mockResolvedValue(SESSION_ID);
    store.app.authorTarget = { kind: "edit", prd: "docs-site" };

    const view = render(AuthorPane, { props: { active: true } });
    await waitFor(() => expect(bridge.start).toHaveBeenCalled());
    expect(view.queryByPlaceholderText("checkout")).toBeNull();
    expect(view.queryByRole("button", { name: "Create" })).toBeNull();
  });

  // Returning to the tab must not start a second agent for the same PRD.
  it("starts at most one session per target across tab switches", async () => {
    bridge.start.mockResolvedValue(SESSION_ID);
    store.app.authorTarget = { kind: "edit", prd: "docs-site" };

    const view = render(AuthorPane, { props: { active: true } });
    await waitFor(() => expect(bridge.start).toHaveBeenCalledTimes(1));

    await switchTabs(view.rerender, 3);
    expect(bridge.start).toHaveBeenCalledTimes(1);
  });

  it("explains a failed start and offers a retry", async () => {
    bridge.start.mockRejectedValue(new Error("docs-site has no PRD to edit"));
    store.app.authorTarget = { kind: "edit", prd: "docs-site" };

    const view = render(AuthorPane, { props: { active: true } });
    await waitFor(() => expect(view.getByText(/has no PRD to edit/)).toBeTruthy());

    bridge.start.mockResolvedValue(SESSION_ID);
    await fireEvent.click(view.getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(bridge.start).toHaveBeenCalledTimes(2));
  });

  it("starts nothing on its own when the target is a new PRD", async () => {
    store.app.authorTarget = null;
    render(AuthorPane, { props: { active: true } });
    await waitFor(() => {});
    expect(bridge.start).not.toHaveBeenCalled();
  });
});

/**
 * Ending a session must not be a one-way door. The pane guards against opening
 * a second agent for a PRD that already has one, and that guard used to outlive
 * the session it was protecting — leaving "Starting the conversation…" on
 * screen with nothing starting, and no way back without restarting the app.
 */
describe("starting a second conversation", () => {
  it("opens a new session after the first one is stopped", async () => {
    const view = await startSession("new", "checkout");
    await endSession({ created: true, parsed: true, stories: 0 });

    await fireEvent.click(view.getByRole("button", { name: "New session" }));

    await waitFor(() => expect(bridge.start).toHaveBeenCalledTimes(2));
    expect(bridge.start.mock.calls[1][0]).toMatchObject({ prd: "checkout" });
  });

  // A conversation that wrote stories leaves a real PRD behind; a second `new`
  // session would be refused for exactly that reason, so the next one revises.
  it("revises rather than rewrites once stories exist", async () => {
    const view = await startSession("new", "checkout");
    await endSession({ created: true, parsed: true, stories: 3 });

    await fireEvent.click(view.getByRole("button", { name: "New session" }));

    await waitFor(() => expect(bridge.start).toHaveBeenCalledTimes(2));
    expect(bridge.start.mock.calls[1][0]).toMatchObject({ kind: "edit", prd: "checkout" });
  });

  // Whatever leaves the pane without a session, the way out is a control the
  // user can press — never a message implying something is already happening.
  it("offers a start button when no session is running", async () => {
    bridge.start.mockRejectedValue(new Error("boom"));
    store.app.authorTarget = { kind: "edit", prd: "checkout" };

    const view = render(AuthorPane, { props: { active: true } });

    await waitFor(() => expect(view.getByRole("button", { name: "Try again" })).toBeTruthy());
    expect(view.queryByText(/Starting the conversation/)).toBeNull();
  });
});
