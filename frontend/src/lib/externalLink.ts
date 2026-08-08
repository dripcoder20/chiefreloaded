import { openURL } from "../platform";

/**
 * Makes an anchor to somewhere outside the app actually go there.
 *
 * An embedded webview has no window for `target="_blank"` to open and no opener
 * to handle it, so the browser silently swallows the click and the link reads as
 * dead. Every outward link therefore has to route through the host rather than
 * through ordinary navigation. Left as a plain anchor it would also be free to
 * navigate the webview itself away from the app, which has no way back.
 *
 * An action rather than a handler on each link: this applies to every external
 * link there is, and one that forgets it looks exactly like one that works until
 * somebody clicks it.
 */
export function external(node: HTMLAnchorElement) {
  function open(event: MouseEvent): void {
    // Let the browser do its usual thing with a modified click — in a preview
    // build those still mean something, and in a webview they do nothing either
    // way. The href is read from the node so the action stays correct when it
    // changes without the action being re-run.
    if (event.defaultPrevented || event.button !== 0) return;
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    if (!node.href) return;

    event.preventDefault();
    openURL(node.href);
  }

  node.addEventListener("click", open);
  return {
    destroy(): void {
      node.removeEventListener("click", open);
    },
  };
}
