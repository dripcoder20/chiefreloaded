import { render, fireEvent, cleanup } from "@testing-library/svelte";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

/**
 * US-004 — the pull-request control.
 *
 * The control is absent, not disabled, wherever publishing cannot work. These
 * tests assert absence, which is the part that is easy to regress into a
 * disabled button nobody can explain.
 */

const store = vi.hoisted(() => {
  const app = {
    canPublish: false,
    canPublishStack: false,
    publishOffer: null as unknown,
    publishing: false,
    published: null as unknown,
    publishedStack: null as unknown,
    now: 1_700_000_000_000,
  };
  return { app, publishPullRequest: vi.fn(), publishStack: vi.fn() };
});

vi.mock("../stores/app.svelte", () => store);

import PublishMenu from "./PublishMenu.svelte";

beforeEach(() => {
  vi.clearAllMocks();
  store.app.canPublish = false;
  store.app.canPublishStack = false;
  store.app.publishOffer = null;
  store.app.publishing = false;
  store.app.published = null;
  store.app.publishedStack = null;
});
afterEach(cleanup);

describe("the pull-request control", () => {
  it("is absent when the PRD cannot be published", () => {
    const view = render(PublishMenu);
    expect(view.queryByRole("button", { name: "Pull request" })).toBeNull();
  });

  it("offers both items once the PRD has something to publish", async () => {
    store.app.canPublish = true;
    const view = render(PublishMenu);

    await fireEvent.click(view.getByRole("button", { name: "Pull request" }));

    expect(view.getByRole("menuitem", { name: "Create pull request" })).toBeTruthy();
    expect(view.getByRole("menuitem", { name: "Create draft pull request" })).toBeTruthy();
  });

  it("publishes as a draft only when the draft item is chosen", async () => {
    store.app.canPublish = true;
    const view = render(PublishMenu);

    await fireEvent.click(view.getByRole("button", { name: "Pull request" }));
    await fireEvent.click(view.getByRole("menuitem", { name: "Create pull request" }));
    expect(store.publishPullRequest).toHaveBeenCalledWith(false);

    await fireEvent.click(view.getByRole("button", { name: "Pull request" }));
    await fireEvent.click(view.getByRole("menuitem", { name: "Create draft pull request" }));
    expect(store.publishPullRequest).toHaveBeenLastCalledWith(true);
  });

  // Publishing takes the better part of a minute against a real remote, so the
  // control has to say it is working and refuse a second press meanwhile.
  it("reports progress while publishing and cannot be pressed again", () => {
    store.app.publishing = true;
    const view = render(PublishMenu);

    const button = view.getByRole("button", { name: "Publishing…" }) as HTMLButtonElement;
    expect(button.disabled).toBe(true);
  });

  it("shows the resulting pull request with a link", () => {
    store.app.canPublish = true;
    store.app.published = {
      prd: "checkout",
      branch: "chief/checkout",
      pr: { number: 128, url: "https://github.com/acme/checkout/pull/128", state: "OPEN" },
    };
    const view = render(PublishMenu);

    const link = view.getByRole("link") as HTMLAnchorElement;
    expect(link.href).toBe("https://github.com/acme/checkout/pull/128");
    expect(view.getByText("#128")).toBeTruthy();
  });

  // US-005 — the stacked items, and the sentence that replaces them.
  it("offers the stacked items for a PRD whose run used a branch per story", async () => {
    store.app.canPublish = true;
    store.app.canPublishStack = true;
    const view = render(PublishMenu);

    await fireEvent.click(view.getByRole("button", { name: "Pull request" }));

    expect(view.getByRole("menuitem", { name: "Create stacked pull requests" })).toBeTruthy();
    await fireEvent.click(view.getByRole("menuitem", { name: "Create stacked pull requests" }));
    expect(store.publishStack).toHaveBeenCalledWith(false);
  });

  it("states why a single-branch PRD has no stacked item instead of hiding it silently", async () => {
    store.app.canPublish = true;
    store.app.publishOffer = {
      available: true,
      layout: "one-branch",
      stacked: false,
      stackReason: "this run put every story on one branch, so there is no stack to publish",
    };
    const view = render(PublishMenu);

    await fireEvent.click(view.getByRole("button", { name: "Pull request" }));

    expect(view.queryByRole("menuitem", { name: "Create stacked pull requests" })).toBeNull();
    expect(view.getByText(/no stack to publish/)).toBeTruthy();
  });

  it("shows every story of a published stack with its link", () => {
    store.app.canPublish = true;
    store.app.publishedStack = {
      prd: "checkout",
      stories: [
        {
          storyId: "US-001",
          branch: "loop/checkout/us-001",
          base: "main",
          pr: { number: 128, url: "https://github.com/acme/checkout/pull/128", state: "OPEN" },
        },
        {
          storyId: "US-002",
          branch: "loop/checkout/us-002",
          skipped: "the story produced no commit",
        },
      ],
    };
    const view = render(PublishMenu);

    expect(view.getByText("US-001")).toBeTruthy();
    expect((view.getByRole("link") as HTMLAnchorElement).href).toBe(
      "https://github.com/acme/checkout/pull/128",
    );
    expect(view.getByText("US-002")).toBeTruthy();
    expect(view.getByText(/produced no commit/)).toBeTruthy();
  });
});
