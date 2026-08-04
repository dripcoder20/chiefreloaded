/**
 * Keyboard handling for the authoring terminal.
 *
 * The terminal is a real PTY: whatever bytes we emit go straight to the agent's
 * line editor. Enter and Shift+Enter arrive as the same key to xterm, which by
 * default turns both into a carriage return — so the agent can't tell "submit"
 * from "insert a line break". We intercept the key event ourselves and choose
 * the bytes deliberately.
 */

/** Enter: submit the line. A PTY's line discipline reads CR as end-of-line. */
export const CR = "\r";
/** Shift+Enter: insert a line break at the cursor without submitting. */
export const LF = "\n";

export type TerminalKeyAction =
  | { kind: "default" } // let xterm emit its usual sequence (plain Enter → CR)
  | { kind: "insert"; data: string }; // we emit data; xterm's default is suppressed

/**
 * Decide what a key event means for terminal input.
 *
 * Returns `insert` with the bytes to write for a manual newline, or `default`
 * to let xterm handle the key as it normally would (so plain Enter still sends
 * CR and submits, and every non-Enter key is untouched).
 */
export function resolveTerminalKey(event: KeyboardEvent): TerminalKeyAction {
  if (event.type !== "keydown") return { kind: "default" };
  if (isComposing(event)) return { kind: "default" };
  if (event.key !== "Enter") return { kind: "default" };
  if (event.shiftKey) return { kind: "insert", data: LF };
  return { kind: "default" };
}

/**
 * An in-progress input-method composition must never be read as Enter. Choosing
 * a CJK candidate with the Enter key fires a keydown with `isComposing` set;
 * treating that as submit would send half-composed input, and as a newline
 * would corrupt the candidate. The terminal runs in a Chromium webview, which
 * reports `isComposing` reliably.
 */
function isComposing(event: KeyboardEvent): boolean {
  return event.isComposing;
}
