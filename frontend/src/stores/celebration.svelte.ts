/**
 * Whether publishing should be celebrated with confetti.
 *
 * The preference lives entirely in the browser. Putting it in the Go config
 * would make an animation a property of the *project*, shared by everyone who
 * checks the repository out, when it is a property of the person watching the
 * screen — and it would drag a config migration behind it for something with no
 * effect on what a run does.
 *
 * Defaulting to on is the deliberate choice: the celebration is the feature, and
 * a preference nobody has expressed is not a preference to suppress it. Every
 * way of failing to read a stored value therefore lands on on, never off.
 *
 * Persistence happens in the setter rather than an `$effect`, because effects
 * need a component or root context and this module is read from plain
 * TypeScript (and from tests) as well as from components.
 */

import { fireConfetti } from "../lib/confetti";
import type { StackReport, StoryPublish } from "../platform";

/** The single key this preference occupies, namespaced as LogPanel's is. */
export const STORAGE_KEY = "loop.celebrateOnPublish";

/** No stored preference means celebrate; see the note above. */
const DEFAULT_ENABLED = true;

/**
 * The stored preference, or the default.
 *
 * A private browsing mode that throws on `localStorage`, a value written by a
 * different version, and hand-edited garbage are all the same situation here:
 * nothing usable was stored, so nothing is assumed.
 */
function restoreEnabled(): boolean {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw === null) return DEFAULT_ENABLED;
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== "boolean") return DEFAULT_ENABLED;
    return parsed;
  } catch {
    return DEFAULT_ENABLED;
  }
}

function persistEnabled(isEnabled: boolean): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(isEnabled));
  } catch {
    // Remembering the choice is a convenience; losing it must not break a publish.
  }
}

class CelebrationPreference {
  #isEnabled = $state(restoreEnabled());

  get isCelebrationEnabled(): boolean {
    return this.#isEnabled;
  }

  set isCelebrationEnabled(isEnabled: boolean) {
    this.#isEnabled = isEnabled;
    persistEnabled(isEnabled);
  }
}

export const celebration = new CelebrationPreference();

/** Flip the preference. The Settings switch binds through this. */
export function toggleCelebration(): void {
  celebration.isCelebrationEnabled = !celebration.isCelebrationEnabled;
}

/**
 * Celebrate a pull request that has just been published, if the user wants it.
 *
 * Call this from the code path that *did* the publishing, never from one that
 * renders its result: a result is state, and state is read again on every
 * re-render, which would make the confetti fire when the window is resized or a
 * neighbouring value changes. Publishing is an event, and happens once.
 *
 * The preference is read here rather than passed in, so a toggle made while a
 * publish is in flight is the one that decides.
 */
export function celebratePublish(): void {
  if (!celebration.isCelebrationEnabled) return;
  fireConfetti();
}

/**
 * Celebrate a stack that has just become fully published.
 *
 * "Fully" is the point: a stack fails in layers, and three pull requests out of
 * four is the case confetti would misrepresent. So a single failure anywhere —
 * the report's own `failed`, or an entry that could not be opened — cancels the
 * celebration even though most of the stack landed.
 *
 * A story that contributes no pull request because it committed nothing is not a
 * failure and does not cancel it. That story published everything it had.
 */
export function celebrateStackPublish(report: StackReport): void {
  if (report.failed) return;
  const stories = report.stories ?? [];
  if (stories.some(hasFailed)) return;
  // Nothing newly opened means this pass changed nothing — the stack was already
  // complete before it, and the confetti for that fired then. This is what makes
  // "once, when the stack first becomes complete" hold across retries: the retry
  // that opens the last missing pull request celebrates, and pressing publish
  // again afterwards has nothing left to celebrate.
  if (!stories.some(wasJustOpened)) return;
  celebratePublish();
}

function hasFailed(story: StoryPublish): boolean {
  return Boolean(story.error);
}

function wasJustOpened(story: StoryPublish): boolean {
  return Boolean(story.pr) && story.alreadyOpen !== true;
}
