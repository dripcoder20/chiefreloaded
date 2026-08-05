/**
 * What agent and model to show against a story that is being implemented.
 *
 * This module imports nothing from ../platform on purpose: the generated Wails
 * bindings are built at compile time and absent in a plain checkout, so keeping
 * the logic here is what makes it unit-testable. app.svelte.ts re-exports it.
 *
 * The rule the PRD turns on is that a value is never guessed. The three states
 * are distinct and mean different things:
 *
 *   resolved     — the session named it
 *   Resolving…   — a session exists but has not said yet
 *   Unavailable  — the session reported and this provider does not supply it
 *
 * Showing a configured default in place of any of these would be a plausible
 * lie: a session may run an override, or a provider-selected fallback.
 */

/** The run fields this module reads. Structurally matched to the Go json tags. */
export type SessionMetaRun = {
  id: string;
  prd: string;
  state: string;
  storyId?: string;
  provider?: string;
  model?: string;
  modelUnavailable?: boolean;
};

/** A value that is known, still resolving, or not supplied by the provider. */
export type MetaValue =
  | { kind: "value"; text: string }
  | { kind: "resolving" }
  | { kind: "unavailable" };

/** The agent and model to display for a story, or null to display neither. */
export type StoryMeta = { agent: MetaValue; model: MetaValue };

export const RESOLVING_LABEL = "Resolving…";
export const UNAVAILABLE_LABEL = "Unavailable";

/**
 * States in which a story is being implemented and therefore carries metadata.
 *
 * Deliberately every non-terminal state: the PRD asks for the indicator to stay
 * put through pausing and stopping, because a session that is winding down is
 * still the session that is running.
 */
const ACTIVE_STATES = new Set(["idle", "running", "paused", "awaiting-answer"]);

/** True when this run is actively implementing the given story. */
export function isImplementing(run: SessionMetaRun | null | undefined, storyId: string): boolean {
  if (!run || !run.storyId) return false;
  return run.storyId === storyId && ACTIVE_STATES.has(run.state);
}

/**
 * The label for one metadata field.
 *
 * Absent means resolving; explicitly-unavailable means the provider does not
 * report it. The two are never collapsed, and one being unavailable never hides
 * the other.
 */
export function metaValue(value: string | undefined, unavailable: boolean | undefined): MetaValue {
  if (value) return { kind: "value", text: value };
  if (unavailable) return { kind: "unavailable" };
  return { kind: "resolving" };
}

/** Human text for a metadata value, for both display and assistive technology. */
export function metaText(value: MetaValue): string {
  if (value.kind === "value") return value.text;
  if (value.kind === "unavailable") return UNAVAILABLE_LABEL;
  return RESOLVING_LABEL;
}

/**
 * The agent and model for a story, or null when it has no active session.
 *
 * Returning null rather than empty values is what removes the indicator when a
 * story stops being implemented: this story adds no historical record of which
 * agent ran a finished story.
 *
 * The run is matched by story id and only counted while it is live, so a
 * delayed update belonging to a superseded session cannot be shown against a
 * story the newer session owns — the caller passes the run it holds now, and a
 * restarted implementation is a different run object with its own metadata.
 */
export function storyMeta(
  run: SessionMetaRun | null | undefined,
  storyId: string,
): StoryMeta | null {
  if (!isImplementing(run, storyId)) return null;
  return {
    // The agent CLI is chosen before the session starts, so it is known
    // whenever a session exists; it is still routed through metaValue so a
    // provider Loop could not resolve reads as resolving rather than blank.
    agent: metaValue(run?.provider, false),
    model: metaValue(run?.model, run?.modelUnavailable),
  };
}
