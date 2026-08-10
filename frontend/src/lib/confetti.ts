/**
 * The one place that knows how the celebration is drawn.
 *
 * Confetti is presentation only: it marks a publish that already happened. So
 * every failure mode here — a library that will not load, a canvas the browser
 * refuses to give a 2d context for — is swallowed. A pull request link the user
 * is waiting for must never be replaced by an error from an animation.
 *
 * The library is loaded on demand rather than imported at the top of the app:
 * it is only ever needed at the end of a run, and a dynamic import is also the
 * only shape in which "the library failed to load" is a catchable runtime event
 * instead of a broken module graph.
 */

// Above every panel, dialog and overlay in the app; the highest of those sits
// at 100. The canvas takes no pointer events, so covering them is harmless.
const OVERLAY_Z_INDEX = 1000;

// Ticks are counted in animation frames, so they bound the animation at a
// steady 60fps and no lower. HARD_STOP_MS is the wall-clock guarantee: whatever
// the frame rate, the burst is torn down before it outlives its welcome.
const ANIMATION_TICKS = 90;
const HARD_STOP_MS = 1800;

const PARTICLE_COUNT = 120;
const SPREAD_DEGREES = 70;
const START_VELOCITY = 45;

// Slightly below centre, so the arc peaks in view rather than off the top.
const ORIGIN = { x: 0.5, y: 0.65 };

/**
 * Fires a single confetti burst over the whole window.
 *
 * Returns immediately; the animation runs on its own and never repeats.
 */
export function fireConfetti(): void {
  void burst();
}

async function burst(): Promise<void> {
  try {
    const { default: confetti } = await import("canvas-confetti");
    const finished = confetti({
      particleCount: PARTICLE_COUNT,
      spread: SPREAD_DEGREES,
      startVelocity: START_VELOCITY,
      ticks: ANIMATION_TICKS,
      zIndex: OVERLAY_Z_INDEX,
      origin: ORIGIN,
    });
    stopAfterHardLimit(confetti.reset);
    await finished;
  } catch {
    // See the module comment: a celebration that fails is not news.
  }
}

function stopAfterHardLimit(reset: () => void): void {
  setTimeout(() => {
    // The timer can outlive the window that armed it — a closed webview, a test
    // whose environment has already been torn down — and a throw from here has
    // no caller left to catch it.
    try {
      reset();
    } catch {
      // Nothing to clean up if the canvas is already gone.
    }
  }, HARD_STOP_MS);
}
