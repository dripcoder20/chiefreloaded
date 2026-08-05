import { render, fireEvent, cleanup, within } from "@testing-library/svelte";
import { tick } from "svelte";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import PrdRail from "./PrdRail.svelte";

/**
 * US-004 — the PRD rail's ordering, divider, per-row action menus and their
 * dismissal/keyboard behaviour. The store is mocked to plain state plus action
 * spies, so what is under test is the rail's own wiring, not the Go backend.
 */

const store = vi.hoisted(() => ({
  selectPrd: vi.fn(),
  requestNewPRD: vi.fn(),
  editPrd: vi.fn(),
  openPrdFile: vi.fn(),
  confirmDeletePrd: vi.fn(),
  openOnGitHub: vi.fn(),
  openInApp: vi.fn(),
  app: {
    localApps: [
      { app: "vscode", name: "VS Code", available: true },
      { app: "claude", name: "Claude", available: true },
      { app: "cursor", name: "Cursor", available: false },
      { app: "codex", name: "Codex", available: false },
    ],
    prds: [
      { name: "checkout", completed: 1, total: 3 },
      { name: "docs-site", completed: 2, total: 6 },
    ],
    runs: [] as Array<{ prd: string; state: string }>,
    selectedPrd: "checkout" as string | null,
  },
}));

vi.mock("../stores/app.svelte", () => store);

const EXPECTED_ACTIONS = ["Edit PRD", "Open markdown file", "Delete PRD"];

function dotsFor(name: string): HTMLButtonElement {
  return document.querySelector<HTMLButtonElement>(`[aria-label="Actions for ${name}"]`)!;
}

function rowFor(name: string): HTMLElement {
  return dotsFor(name).closest(".row")!;
}

async function openButtonMenu(name: string): Promise<HTMLElement> {
  await fireEvent.click(dotsFor(name));
  await tick();
  return document.querySelector<HTMLElement>("[role='menu']")!;
}

async function openContextMenu(name: string): Promise<HTMLElement> {
  await fireEvent.contextMenu(rowFor(name));
  await tick();
  return document.querySelector<HTMLElement>("[role='menu']")!;
}

function menuLabels(menu: HTMLElement): string[] {
  return within(menu)
    .getAllByRole("menuitem")
    .map((el) => el.textContent?.trim() ?? "");
}

beforeEach(() => {
  vi.clearAllMocks();
  store.app.selectedPrd = "checkout";
  store.app.runs = [];
});

afterEach(cleanup);

describe("layout", () => {
  it("renders New PRD before the PRD list, separated by a divider", () => {
    const { container } = render(PrdRail);

    const newBtn = container.querySelector(".new")!;
    const divider = container.querySelector("[role='separator']")!;
    const firstRow = container.querySelector(".row")!;

    expect(newBtn.textContent).toContain("New PRD");
    expect(divider).toBeTruthy();
    // Order: New PRD → divider → first PRD row.
    expect(newBtn.compareDocumentPosition(divider) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(divider.compareDocumentPosition(firstRow) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("routes New PRD to requestNewPRD", async () => {
    const { container } = render(PrdRail);
    await fireEvent.click(container.querySelector(".new")!);
    expect(store.requestNewPRD).toHaveBeenCalledOnce();
  });

  it("keeps a three-dot button, named for its PRD, on every row", () => {
    render(PrdRail);

    // One accessible, per-PRD three-dot button per row. The button is always in
    // the DOM (not conditionally rendered) — hover, keyboard-focus and touch
    // visibility are handled by CSS opacity over this always-present element, so
    // it is reachable in every one of those states. The CSS rules that toggle
    // that opacity (.row:hover, .row:focus-within, @media (hover: none)) live in
    // the component's <style>; jsdom does not evaluate them, so their effect is
    // verified visually rather than here.
    for (const name of ["checkout", "docs-site"]) {
      const dots = dotsFor(name);
      expect(dots).toBeTruthy();
      expect(dots.hasAttribute("hidden")).toBe(false);
      expect(dots.getAttribute("aria-label")).toBe(`Actions for ${name}`);
    }
  });
});

describe("action menus", () => {
  it("opens a dropdown from the three-dot button with the three actions in order", async () => {
    render(PrdRail);
    const menu = await openButtonMenu("checkout");
    expect(menuLabels(menu)).toEqual(EXPECTED_ACTIONS);
    expect(dotsFor("checkout").getAttribute("aria-expanded")).toBe("true");
  });

  it("opens a context menu from a right-click with the same actions in the same order", async () => {
    render(PrdRail);
    const menu = await openContextMenu("checkout");
    expect(menuLabels(menu)).toEqual(EXPECTED_ACTIONS);
  });

  it("offers identical actions from both triggers (parity)", async () => {
    render(PrdRail);
    const fromButton = menuLabels(await openButtonMenu("checkout"));
    await fireEvent.keyDown(document.body, { key: "Escape" });
    await tick();
    const fromContext = menuLabels(await openContextMenu("checkout"));
    expect(fromButton).toEqual(fromContext);
    expect(fromButton).toEqual(EXPECTED_ACTIONS);
  });

  it("only ever shows one menu — opening another closes the first", async () => {
    render(PrdRail);
    await openButtonMenu("checkout");
    await openContextMenu("docs-site");
    const menus = document.querySelectorAll("[role='menu']");
    expect(menus).toHaveLength(1);
    expect(menus[0].getAttribute("aria-label")).toBe("Actions for docs-site");
  });
});

describe("target selection", () => {
  it("acts on the row's PRD even when a different PRD is selected (dropdown)", async () => {
    store.app.selectedPrd = "checkout";
    render(PrdRail);

    const menu = await openButtonMenu("docs-site");
    await fireEvent.click(within(menu).getByText("Edit PRD"));
    expect(store.editPrd).toHaveBeenCalledWith("docs-site");
  });

  it("acts on the row's PRD even when a different PRD is selected (context menu)", async () => {
    store.app.selectedPrd = "checkout";
    render(PrdRail);

    const menu = await openContextMenu("docs-site");
    await fireEvent.click(within(menu).getByText("Delete PRD"));
    expect(store.confirmDeletePrd).toHaveBeenCalledWith("docs-site");
  });

  it("wires each action to its store function", async () => {
    render(PrdRail);
    let menu = await openButtonMenu("checkout");
    await fireEvent.click(within(menu).getByText("Open markdown file"));
    expect(store.openPrdFile).toHaveBeenCalledWith("checkout");

    menu = await openButtonMenu("checkout");
    await fireEvent.click(within(menu).getByText("Edit PRD"));
    expect(store.editPrd).toHaveBeenCalledWith("checkout");
  });
});

describe("dismissal", () => {
  it("dismisses on completing an action", async () => {
    render(PrdRail);
    const menu = await openButtonMenu("checkout");
    await fireEvent.click(within(menu).getByText("Edit PRD"));
    await tick();
    expect(document.querySelector("[role='menu']")).toBeNull();
  });

  it("dismisses on outside pointer press", async () => {
    render(PrdRail);
    await openButtonMenu("checkout");
    await fireEvent.pointerDown(document.body);
    await tick();
    expect(document.querySelector("[role='menu']")).toBeNull();
  });

  it("dismisses on Escape", async () => {
    render(PrdRail);
    await openButtonMenu("checkout");
    await fireEvent.keyDown(document.body, { key: "Escape" });
    await tick();
    expect(document.querySelector("[role='menu']")).toBeNull();
  });

  it("dismisses when focus leaves the menu", async () => {
    render(PrdRail);
    const menu = await openButtonMenu("checkout");
    await fireEvent.focusOut(menu, { relatedTarget: document.body });
    await tick();
    expect(document.querySelector("[role='menu']")).toBeNull();
  });
});

describe("keyboard access", () => {
  it("focuses the first item on open and moves focus with the arrow keys", async () => {
    render(PrdRail);
    const menu = await openButtonMenu("checkout");
    const items = within(menu).getAllByRole("menuitem");

    expect(document.activeElement).toBe(items[0]);

    await fireEvent.keyDown(menu, { key: "ArrowDown" });
    expect(document.activeElement).toBe(items[1]);

    await fireEvent.keyDown(menu, { key: "ArrowUp" });
    expect(document.activeElement).toBe(items[0]);

    // Wraps from the first item back to the last.
    await fireEvent.keyDown(menu, { key: "ArrowUp" });
    expect(document.activeElement).toBe(items[items.length - 1]);
  });

  it("exposes the three-dot button as a keyboard-operable button", () => {
    render(PrdRail);
    const dots = dotsFor("checkout");
    expect(dots.tagName).toBe("BUTTON");
    expect(dots.getAttribute("aria-haspopup")).toBe("menu");
  });
});

/**
 * US-012 — the repository launchers: GitHub, VS Code and an AI IDE dropdown that
 * always lists all three editors regardless of what is installed.
 */
describe("repository launchers", () => {
  function launcher(label: string): HTMLButtonElement {
    return document.querySelector<HTMLButtonElement>(`[aria-label="${label}"]`)!;
  }

  it("puts the launcher group above the divider, with New PRD still first", () => {
    render(PrdRail);
    const rail = document.querySelector(".rail")!;
    const order = [...rail.children].map((el) => el.className.split(" ")[0]);
    expect(order.indexOf("new")).toBeLessThan(order.indexOf("launchers"));
    expect(order.indexOf("launchers")).toBeLessThan(order.indexOf("divider"));
  });

  it("gives every launcher an accessible name and a tooltip", () => {
    render(PrdRail);
    for (const label of ["Open GitHub repository", "Open in VS Code", "Open in AI IDE"]) {
      const button = launcher(label);
      expect(button).toBeTruthy();
      expect(button.tagName).toBe("BUTTON");
      expect(button.getAttribute("title")).toBe(label);
    }
  });

  it("opens the GitHub repository once per activation", async () => {
    render(PrdRail);
    await fireEvent.click(launcher("Open GitHub repository"));
    expect(store.openOnGitHub).toHaveBeenCalledTimes(1);
  });

  it("forwards the VS Code launch to the store", async () => {
    render(PrdRail);
    await fireEvent.click(launcher("Open in VS Code"));
    expect(store.openInApp).toHaveBeenCalledWith("vscode");
  });

  it("lists Claude, Cursor and Codex whether or not they are installed", async () => {
    render(PrdRail);
    await fireEvent.click(launcher("Open in AI IDE"));
    await tick();
    const menu = document.querySelector<HTMLElement>(
      "[aria-label='Open the repository in an AI IDE']",
    )!;
    const items = within(menu).getAllByRole("menuitem");
    expect(items.map((i) => i.textContent?.trim().split(/\s+/)[0])).toEqual([
      "Claude",
      "Cursor",
      "Codex",
    ]);
  });

  it("marks unavailable editors as secondary text but keeps them selectable", async () => {
    render(PrdRail);
    await fireEvent.click(launcher("Open in AI IDE"));
    await tick();
    const menu = document.querySelector<HTMLElement>(
      "[aria-label='Open the repository in an AI IDE']",
    )!;
    const [claude, cursor] = within(menu).getAllByRole("menuitem");
    expect(claude.textContent).not.toContain("not installed");
    expect(cursor.textContent).toContain("not installed");
    // Selectable is the point: activating it is what raises the alert naming
    // the application to install.
    expect(cursor.hasAttribute("disabled")).toBe(false);

    await fireEvent.click(cursor);
    expect(store.openInApp).toHaveBeenCalledWith("cursor");
  });

  it("navigates the AI IDE dropdown with arrow keys and dismisses on Escape", async () => {
    render(PrdRail);
    const trigger = launcher("Open in AI IDE");
    await fireEvent.click(trigger);
    await tick();
    const menu = document.querySelector<HTMLElement>(
      "[aria-label='Open the repository in an AI IDE']",
    )!;
    const items = within(menu).getAllByRole("menuitem");
    expect(document.activeElement).toBe(items[0]);

    await fireEvent.keyDown(menu, { key: "ArrowDown" });
    expect(document.activeElement).toBe(items[1]);
    await fireEvent.keyDown(menu, { key: "ArrowUp" });
    expect(document.activeElement).toBe(items[0]);

    await fireEvent.keyDown(menu, { key: "Escape" });
    await tick();
    expect(
      document.querySelector("[aria-label='Open the repository in an AI IDE']"),
    ).toBeNull();
    expect(trigger.getAttribute("aria-expanded")).toBe("false");
  });
});
