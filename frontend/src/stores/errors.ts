/**
 * Turning a caught value into something worth showing a person.
 *
 * Wails rejects a failed binding call with a `RuntimeError`, and `String(err)`
 * on any Error renders `${name}: ${message}`. Every message the interface
 * showed was therefore prefixed with a class name that means nothing outside
 * this codebase — "RuntimeError: no PRD named …". The name is an implementation
 * detail of the transport; the message is the part written for the user.
 *
 * This module imports nothing from ../platform so it stays unit-testable
 * without the generated bindings.
 */

/**
 * The human-readable text of a caught value.
 *
 * Anything that is not an Error is stringified as a last resort — a thrown
 * string or object is a bug, but showing it beats showing nothing.
 */
export function errorMessage(err: unknown): string {
  const raw = err instanceof Error ? err.message : String(err);
  return stripErrorName(raw).trim();
}

/**
 * Error names that reached users through String(err).
 *
 * Stripped by name rather than by a general "word before a colon" rule, which
 * would eat the leading clause of a legitimate message — "codex: malformed
 * usage payload" is text we wrote deliberately.
 */
const TRANSPORT_NAMES = ["RuntimeError", "Error", "TypeError"];

/**
 * Remove a leading transport error name.
 *
 * Needed for values that arrive already stringified rather than as an Error, so
 * `err.message` alone would not have dropped the prefix.
 */
function stripErrorName(text: string): string {
  for (const name of TRANSPORT_NAMES) {
    const prefix = `${name}: `;
    if (text.startsWith(prefix)) return text.slice(prefix.length);
  }
  return text;
}
