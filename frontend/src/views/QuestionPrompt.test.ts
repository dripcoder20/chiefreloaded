import { render, fireEvent, cleanup } from "@testing-library/svelte";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import QuestionPrompt from "./QuestionPrompt.svelte";

/**
 * The branch-safety question carries the branch layout, so both halves are one
 * decision. The dialog's job is to submit them together.
 */

const store = vi.hoisted(() => ({ answerQuestion: vi.fn() }));

vi.mock("../stores/app.svelte", () => store);

function branchSafetyQuestion(overrides: Record<string, unknown> = {}) {
  return {
    id: "q_1",
    kind: "branch-safety",
    title: "Where should this run commit?",
    body: "The agent commits as it goes.",
    defaultOption: "branch",
    options: [
      { id: "branch", label: "Create a branch", recommended: true },
      {
        id: "here",
        label: "Continue on feature/x",
        disabledWhen: { layout: "branch-per-story" },
      },
      { id: "cancel", label: "Cancel" },
    ],
    inputs: [
      { key: "branch", label: "Branch name", value: "chief/checkout" },
      {
        key: "layout",
        label: "Branch layout",
        value: "one-branch",
        choices: [
          { value: "one-branch", label: "One branch for the whole PRD" },
          {
            value: "branch-per-story",
            label: "A branch per story",
            forces: { worktree: "true" },
          },
        ],
      },
      { key: "worktree", label: "In a separate worktree", value: "false", checkbox: true },
    ],
    ...overrides,
  } as never;
}

beforeEach(() => vi.clearAllMocks());
afterEach(cleanup);

describe("the question prompt", () => {
  it("offers the layout choice alongside the branch-safety options", () => {
    const view = render(QuestionPrompt, { props: { question: branchSafetyQuestion() } });

    const one = view.getByLabelText("One branch for the whole PRD") as HTMLInputElement;
    const perStory = view.getByLabelText("A branch per story") as HTMLInputElement;
    expect(one.checked).toBe(true);
    expect(perStory.checked).toBe(false);
    // Still one decision: the options are on the same dialog.
    expect(view.getByRole("button", { name: "Create a branch" })).toBeTruthy();
  });

  it("submits the preselected fields with the chosen option", async () => {
    const view = render(QuestionPrompt, { props: { question: branchSafetyQuestion() } });

    await fireEvent.click(view.getByRole("button", { name: "Create a branch" }));

    expect(store.answerQuestion).toHaveBeenCalledWith("q_1", "branch", {
      branch: "chief/checkout",
      layout: "one-branch",
      worktree: "false",
    });
  });

  it("submits the worktree toggle the user switched on", async () => {
    const view = render(QuestionPrompt, { props: { question: branchSafetyQuestion() } });

    await fireEvent.click(view.getByLabelText("In a separate worktree"));
    await fireEvent.click(view.getByRole("button", { name: "Create a branch" }));

    expect(store.answerQuestion).toHaveBeenCalledWith("q_1", "branch", {
      branch: "chief/checkout",
      layout: "one-branch",
      worktree: "true",
    });
  });

  // Per-story branches switch the checkout between stories, so the engine marks
  // that choice as forcing the worktree toggle. The dialog has to honour the
  // pin — and let go of it — as the selection changes.
  it("pins the worktree toggle while a forcing layout is selected", async () => {
    const view = render(QuestionPrompt, { props: { question: branchSafetyQuestion() } });

    await fireEvent.click(view.getByLabelText("A branch per story"));

    const toggle = view.getByLabelText("In a separate worktree") as HTMLInputElement;
    expect(toggle.checked).toBe(true);
    expect(toggle.disabled).toBe(true);

    await fireEvent.click(view.getByRole("button", { name: "Create a branch" }));
    expect(store.answerQuestion).toHaveBeenCalledWith("q_1", "branch", {
      branch: "chief/checkout",
      layout: "branch-per-story",
      worktree: "true",
    });
  });

  it("releases the pinned toggle when the layout changes back", async () => {
    const view = render(QuestionPrompt, { props: { question: branchSafetyQuestion() } });

    await fireEvent.click(view.getByLabelText("A branch per story"));
    await fireEvent.click(view.getByLabelText("One branch for the whole PRD"));

    const toggle = view.getByLabelText("In a separate worktree") as HTMLInputElement;
    expect(toggle.disabled).toBe(false);
    expect(toggle.checked).toBe(false);
  });

  it("disables an option while its disabledWhen rule matches", async () => {
    const view = render(QuestionPrompt, { props: { question: branchSafetyQuestion() } });

    const here = view.getByRole("button", { name: "Continue on feature/x" }) as HTMLButtonElement;
    expect(here.disabled).toBe(false);

    await fireEvent.click(view.getByLabelText("A branch per story"));
    expect(here.disabled).toBe(true);

    await fireEvent.click(view.getByLabelText("One branch for the whole PRD"));
    expect(here.disabled).toBe(false);
  });

  // A layout with commits behind it is reported, not offered: there is nothing
  // the user can do about it, and a disabled radio pair would suggest otherwise.
  it("reports a settled layout without offering to change it", () => {
    const view = render(QuestionPrompt, {
      props: {
        question: branchSafetyQuestion({
          inputs: [
            { key: "branch", label: "Branch name", value: "chief/checkout" },
            {
              key: "layout",
              label: "Branch layout",
              value: "one-branch",
              readOnly: true,
              hint: "Settled: this PRD already has commits arranged this way.",
            },
          ],
        }),
      },
    });

    expect(view.queryByLabelText("A branch per story")).toBeNull();
    expect(view.getByText(/Settled/)).toBeTruthy();
  });
});
