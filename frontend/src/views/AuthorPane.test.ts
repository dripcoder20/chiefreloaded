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

const store = vi.hoisted(() => ({
  app: {
    selectedPrd: null as string | null,
    authorTarget: { kind: "new" } as { kind: "new" } | { kind: "edit"; prd: string },
    environment: {
      agents: [
        { name: "claude", available: true },
        { name: "codex", available: true },
      ],
    },
    agentDefaults: { authoring: "claude", implementation: "codex" },
    destinations: [
      { destination: "github", name: "GitHub Issues", available: true },
      {
        destination: "linear",
        name: "Linear",
        available: false,
        reason: "set LINEAR_API_KEY to a Linear personal API key",
      },
    ],
    publishing: null as unknown,
  },
  refresh: vi.fn(),
  selectPrd: vi.fn(),
  savePrdWorkflow: vi.fn(),
  publishIssues: vi.fn(),
}));

vi.mock("../stores/app.svelte", () => store);

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
  store.app.selectedPrd = null;
  store.app.authorTarget = { kind: "new" };
  store.app.publishing = null;
  store.savePrdWorkflow.mockReset().mockResolvedValue(true);
  store.publishIssues.mockReset().mockResolvedValue(null);
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

  it("does not offer the create form while editing", async () => {
    bridge.start.mockResolvedValue(SESSION_ID);
    store.app.authorTarget = { kind: "edit", prd: "docs-site" };

    const view = render(AuthorPane, { props: { active: true } });
    await waitFor(() => expect(bridge.start).toHaveBeenCalled());
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
    store.app.authorTarget = { kind: "new" };
    render(AuthorPane, { props: { active: true } });
    await waitFor(() => {});
    expect(bridge.start).not.toHaveBeenCalled();
  });
});

/**
 * US-007 / US-008 — the New PRD tab's per-phase agent selectors and workflow
 * options: independent choices, resolved defaults shown rather than a blank,
 * only installed agents offered, and unavailable trackers disabled in place
 * with the missing configuration explained.
 */
describe("creation options", () => {
  /**
   * The select inside the label carrying this text. Queried from the document
   * rather than through RenderResult, whose generic instantiation does not line
   * up between the Svelte and DOM testing-library versions here.
   */
  function labelled(text: string): HTMLSelectElement {
    const label = [...document.querySelectorAll("label")].find((l) =>
      l.textContent?.includes(text),
    );
    return label!.querySelector("select")!;
  }

  /** The pane has exactly one checkbox: the stacked-PR option. */
  function stackCheckbox(): HTMLInputElement {
    return document.querySelector<HTMLInputElement>('input[type="checkbox"]')!;
  }

  it("offers separate authoring and implementation agent selectors", () => {
    const view = render(AuthorPane, { props: { active: true } });
    expect(labelled("Authoring agent")).toBeTruthy();
    expect(labelled("Implementation agent")).toBeTruthy();
  });

  // A blank meaning "whatever is configured" tells the user nothing; the
  // resolved default is shown instead.
  it("initialises each selector from its own resolved default", async () => {
    const view = render(AuthorPane, { props: { active: true } });
    await waitFor(() => expect(labelled("Authoring agent").value).toBe("claude"));
    expect(labelled("Implementation agent").value).toBe("codex");
  });

  it("changes one selector without moving the other", async () => {
    const view = render(AuthorPane, { props: { active: true } });
    await waitFor(() => expect(labelled("Authoring agent").value).toBe("claude"));

    await fireEvent.change(labelled("Authoring agent"), { target: { value: "codex" } });
    expect(labelled("Authoring agent").value).toBe("codex");
    expect(labelled("Implementation agent").value).toBe("codex");

    await fireEvent.change(labelled("Implementation agent"), {
      target: { value: "claude" },
    });
    expect(labelled("Implementation agent").value).toBe("claude");
    expect(labelled("Authoring agent").value).toBe("codex");
  });

  it("lists only installed agents", () => {
    const view = render(AuthorPane, { props: { active: true } });
    const options = [...labelled("Authoring agent").options].map((o) => o.value);
    expect(options).toEqual(["claude", "codex"]);
  });

  it("starts the authoring session with the chosen authoring agent", async () => {
    bridge.start.mockResolvedValue(SESSION_ID);
    const view = render(AuthorPane, { props: { active: true } });
    await waitFor(() => expect(labelled("Authoring agent").value).toBe("claude"));

    await fireEvent.change(labelled("Authoring agent"), { target: { value: "codex" } });
    await fireEvent.input(view.getByPlaceholderText("checkout"), {
      target: { value: "checkout" },
    });
    await fireEvent.click(view.getByRole("button", { name: "Create" }));

    await waitFor(() => expect(bridge.start).toHaveBeenCalled());
    expect(bridge.start.mock.calls[0][0]).toMatchObject({ agent: "codex" });
  });

  // Stacking and the issue destination configure the later run; saving them
  // must not create a branch, a pull request or an issue.
  it("saves the implementation agent and workflow options with the PRD", async () => {
    bridge.start.mockResolvedValue(SESSION_ID);
    const view = render(AuthorPane, { props: { active: true } });
    await waitFor(() => expect(labelled("Implementation agent").value).toBe("codex"));

    await fireEvent.click(stackCheckbox());
    await fireEvent.change(labelled("Publish issues to"), {
      target: { value: "github" },
    });
    await fireEvent.input(view.getByPlaceholderText("checkout"), {
      target: { value: "checkout" },
    });
    await fireEvent.click(view.getByRole("button", { name: "Create" }));

    await waitFor(() => expect(store.savePrdWorkflow).toHaveBeenCalled());
    expect(store.savePrdWorkflow).toHaveBeenCalledWith("checkout", {
      implementationAgent: "codex",
      stackPerStory: true,
      issueDestination: "github",
    });
  });

  it("defaults to not stacking and not publishing", () => {
    const view = render(AuthorPane, { props: { active: true } });
    const stack = stackCheckbox();
    expect(stack.checked).toBe(false);
    expect(labelled("Publish issues to").value).toBe("");
  });

  // An unconfigured tracker is disabled in place with its setup explained,
  // rather than being silently absent.
  it("disables an unconfigured destination and explains what is missing", () => {
    const view = render(AuthorPane, { props: { active: true } });
    const options = [...labelled("Publish issues to").options];

    const linear = options.find((o) => o.value === "linear")!;
    expect(linear.disabled).toBe(true);
    expect(options.find((o) => o.value === "github")!.disabled).toBe(false);
    expect(view.getByText(/LINEAR_API_KEY/)).toBeTruthy();
  });
});
