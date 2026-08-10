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
