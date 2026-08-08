import { beforeEach, describe, expect, it, vi } from "vitest";
import { external } from "./externalLink";

/**
 * Pull request and issue links have to leave the app. In an embedded webview a
 * plain anchor cannot do that — `target="_blank"` is swallowed, so the link
 * looks fine and does nothing — which is the bug this action exists to close.
 */

const openURL = vi.hoisted(() => vi.fn());
vi.mock("../platform", () => ({ openURL }));

function anchor(href = "https://github.com/o/r/pull/7"): HTMLAnchorElement {
  const a = document.createElement("a");
  a.href = href;
  document.body.appendChild(a);
  return a;
}

function click(node: HTMLAnchorElement, init: MouseEventInit = {}): MouseEvent {
  const event = new MouseEvent("click", { bubbles: true, cancelable: true, ...init });
  node.dispatchEvent(event);
  return event;
}

beforeEach(() => {
  document.body.innerHTML = "";
  openURL.mockReset();
});

describe("external link action", () => {
  it("hands the href to the host instead of navigating", () => {
    const a = anchor();
    external(a);

    const event = click(a);

    expect(openURL).toHaveBeenCalledWith("https://github.com/o/r/pull/7");
    expect(event.defaultPrevented).toBe(true);
  });

  // The href is read at click time, so a link whose target changed without the
  // action being re-run still goes to the right place.
  it("follows the current href, not the one it was attached to", () => {
    const a = anchor();
    external(a);
    a.href = "https://github.com/o/r/pull/9";

    click(a);

    expect(openURL).toHaveBeenCalledWith("https://github.com/o/r/pull/9");
  });

  it("leaves modified and non-primary clicks to the browser", () => {
    const a = anchor();
    external(a);

    click(a, { metaKey: true });
    click(a, { ctrlKey: true });
    click(a, { shiftKey: true });
    click(a, { button: 1 });

    expect(openURL).not.toHaveBeenCalled();
  });

  it("stops opening once destroyed", () => {
    const a = anchor();
    external(a).destroy();

    click(a);

    expect(openURL).not.toHaveBeenCalled();
  });
});
