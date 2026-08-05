import { render, fireEvent, waitFor, cleanup } from "@testing-library/svelte";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import NewPrdDialog from "./NewPrdDialog.svelte";

/**
 * Creating a PRD is one decision with an obvious end, so it is asked once in a
 * dialog rather than in a tab competing with the selected PRD's own screens.
 */

const store = vi.hoisted(() => ({
  app: {
    prds: [{ name: "checkout" }] as Array<{ name: string }>,
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
  },
  createPrd: vi.fn(),
}));

vi.mock("../stores/app.svelte", () => store);

function labelled(text: string): HTMLSelectElement {
  const label = [...document.querySelectorAll("label")].find((l) =>
    l.textContent?.includes(text),
  );
  return label!.querySelector("select")!;
}

// Typed structurally rather than as RenderResult: testing-library's generic
// render result does not narrow to the parameter type its own helpers expect.
function nameField(view: { getByPlaceholderText: (text: string) => HTMLElement }): HTMLInputElement {
  return view.getByPlaceholderText("secondary-email") as HTMLInputElement;
}

beforeEach(() => {
  vi.clearAllMocks();
  store.createPrd.mockResolvedValue("secondary-email");
});

afterEach(cleanup);

describe("the New PRD dialog", () => {
  it("creates the PRD with the brief and workflow chosen alongside it", async () => {
    const view = render(NewPrdDialog, { props: { onclose: () => {} } });
    await waitFor(() => expect(labelled("Implementation agent").value).toBe("codex"));

    await fireEvent.input(nameField(view), { target: { value: "secondary-email" } });
    await fireEvent.input(view.getByPlaceholderText(/Optional/), {
      target: { value: "Let people nominate a second email." },
    });
    await fireEvent.click(view.getByLabelText("Stack a pull request per user story"));
    await fireEvent.change(labelled("Publish issues to"), { target: { value: "github" } });
    await fireEvent.click(view.getByRole("button", { name: "Create PRD" }));

    await waitFor(() => expect(store.createPrd).toHaveBeenCalledTimes(1));
    expect(store.createPrd).toHaveBeenCalledWith({
      name: "secondary-email",
      context: "Let people nominate a second email.",
      workflow: {
        implementationAgent: "codex",
        stackPerStory: true,
        issueDestination: "github",
      },
    });
  });

  it("seeds each agent selector from its own resolved default", async () => {
    render(NewPrdDialog, { props: { onclose: () => {} } });
    await waitFor(() => expect(labelled("Authoring agent").value).toBe("claude"));
    expect(labelled("Implementation agent").value).toBe("codex");
  });

  // The name becomes a folder and a branch, so it is checked before the
  // backend is asked — and an existing PRD would be overwritten.
  it("refuses a name that is already taken", async () => {
    const view = render(NewPrdDialog, { props: { onclose: () => {} } });
    await fireEvent.input(nameField(view), { target: { value: "checkout" } });

    expect(view.getByText(/already exists/)).toBeTruthy();
    expect((view.getByRole("button", { name: "Create PRD" }) as HTMLButtonElement).disabled).toBe(
      true,
    );
  });

  it("refuses a name that is not a plain identifier", async () => {
    const view = render(NewPrdDialog, { props: { onclose: () => {} } });
    await fireEvent.input(nameField(view), { target: { value: "has spaces" } });

    expect(view.getByText(/Letters, digits/)).toBeTruthy();
    expect((view.getByRole("button", { name: "Create PRD" }) as HTMLButtonElement).disabled).toBe(
      true,
    );
  });

  it("creates nothing when cancelled", async () => {
    const onclose = vi.fn();
    const view = render(NewPrdDialog, { props: { onclose } });
    await fireEvent.input(nameField(view), { target: { value: "fresh" } });
    await fireEvent.click(view.getByRole("button", { name: "Cancel" }));

    expect(onclose).toHaveBeenCalled();
    expect(store.createPrd).not.toHaveBeenCalled();
  });

  it("dismisses on Escape", async () => {
    const onclose = vi.fn();
    render(NewPrdDialog, { props: { onclose } });
    await fireEvent.keyDown(document.querySelector("[role='dialog']")!, { key: "Escape" });

    expect(onclose).toHaveBeenCalled();
  });

  it("disables an unconfigured tracker and explains the setup", async () => {
    render(NewPrdDialog, { props: { onclose: () => {} } });
    const options = [...labelled("Publish issues to").options];

    expect(options.find((o) => o.value === "linear")!.disabled).toBe(true);
    expect(options.find((o) => o.value === "github")!.disabled).toBe(false);
    expect(document.body.textContent).toContain("LINEAR_API_KEY");
  });

  it("lists only installed agents", async () => {
    render(NewPrdDialog, { props: { onclose: () => {} } });
    expect([...labelled("Authoring agent").options].map((o) => o.value)).toEqual([
      "claude",
      "codex",
    ]);
  });
});
