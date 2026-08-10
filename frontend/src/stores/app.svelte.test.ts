import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { LoopState, type RunSnapshot } from "../platform";

/**
 * These tests pin US-011: implementation controls and status are derived from
 * the authoritative session state, not from the result of the most recent Start
 * request. They drive the store directly against a mocked platform bridge so
 * the state machine — Starting/Pausing/Resuming/Stopping transitions, the
 * at-most-one-session guard, stale-error cleanup, and failure reconciliation —
 * is what's under test, not the Go backend.
 */

// A controllable backend: each method resolves/rejects on command and `list`
// returns whatever runs the test has set, standing in for the authoritative state.
const backend = vi.hoisted(() => ({
  start: vi.fn(),
  pause: vi.fn(),
  resume: vi.fn(),
  stop: vi.fn(),
  runs: [] as RunSnapshot[],
}));

vi.mock("../platform", async () => {
  const actual = await vi.importActual<typeof import("../platform")>("../platform");
  return {
    ...actual,
    api: {
      run: {
        start: (...a: unknown[]) => backend.start(...a),
        pause: (...a: unknown[]) => backend.pause(...a),
        resume: (...a: unknown[]) => backend.resume(...a),
        stop: (...a: unknown[]) => backend.stop(...a),
        list: async () => backend.runs,
        snapshot: async () => ({ runs: backend.runs }),
      },
    },
  };
});

import {
  app,
  startRun,
  pauseRun,
  resumeRun,
  stopRun,
} from "./app.svelte";

/** A deferred promise so a request can be left unresolved mid-test. */
function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function runIn(state: LoopState, prd = "checkout"): RunSnapshot {
  return { id: "run_1", prd, state, attempt: 1, attemptBudget: 8, pendingGitErrors: 0 } as RunSnapshot;
}

beforeEach(() => {
  backend.start.mockReset();
  backend.pause.mockReset();
  backend.resume.mockReset();
  backend.stop.mockReset();
  backend.runs = [];
  app.selectedPrd = "checkout";
  app.runs = [];
  app.pending = {};
  app.error = null;
});

afterEach(() => {
  vi.clearAllTimers();
});

describe("control availability for every session state", () => {
  const cases: Array<[LoopState | "none", boolean, boolean, boolean, boolean]> = [
    // state, canStart, canResume, canPause, canStop
    ["none", true, false, false, false],
    [LoopState.StateIdle, true, false, false, false],
    [LoopState.StateRunning, false, false, true, true],
    [LoopState.StatePaused, false, true, false, true],
    [LoopState.StateStopped, true, false, false, false],
    [LoopState.StateComplete, true, false, false, false],
    [LoopState.StateError, true, false, false, false],
    [LoopState.StateAwaiting, false, false, false, true],
  ];

  for (const [state, canStart, canResume, canPause, canStop] of cases) {
    it(`derives controls for ${state}`, () => {
      app.runs = state === "none" ? [] : [runIn(state)];
      expect(app.canStart).toBe(canStart);
      expect(app.canResume).toBe(canResume);
      expect(app.canPause).toBe(canPause);
      expect(app.canStop).toBe(canStop);
    });
  }
});

describe("starting", () => {
  it("shows a Starting state and blocks a second Start while unresolved", async () => {
    const d = deferred<string>();
    backend.start.mockReturnValue(d.promise);

    const first = startRun();
    expect(app.displayState).toBe("starting");
    expect(app.canStart).toBe(false);
    expect(app.canPause).toBe(false);
    expect(app.canStop).toBe(false);

    // A duplicate Start while the first is in flight must not fire the backend.
    await startRun();
    expect(backend.start).toHaveBeenCalledTimes(1);

    backend.runs = [runIn(LoopState.StateRunning)];
    d.resolve("run_1");
    await first;

    expect(app.currentTransition).toBeNull();
    expect(app.displayState).toBe("running");
  });
});

describe("failed Start followed by a successful retry", () => {
  it("returns to a startable state on failure, then starts exactly one session", async () => {
    backend.start.mockRejectedValueOnce("boom");

    await startRun();
    // No session exists: actionable error, startable again, Pause/Stop disabled.
    expect(app.error).toBe("boom");
    expect(app.canStart).toBe(true);
    expect(app.canPause).toBe(false);
    expect(app.canStop).toBe(false);
    expect(app.currentTransition).toBeNull();

    // Retry succeeds and the backend reports a running session.
    backend.start.mockResolvedValueOnce("run_1");
    backend.runs = [runIn(LoopState.StateRunning)];
    await startRun();

    expect(backend.start).toHaveBeenCalledTimes(2); // at most one per attempt
    expect(app.error).toBeNull(); // stale start-error dialog dismissed
    expect(app.displayState).toBe("running");
    expect(app.canStart).toBe(false);
    expect(app.canPause).toBe(true);
    expect(app.canStop).toBe(true);
  });

  it("does not start a second session once one is running", async () => {
    app.runs = [runIn(LoopState.StateRunning)];
    await startRun();
    expect(backend.start).not.toHaveBeenCalled();
  });
});

describe("pause, resume, and stop transitions", () => {
  it("shows the transition and blocks duplicates while pausing", async () => {
    app.runs = [runIn(LoopState.StateRunning)];
    const d = deferred<void>();
    backend.pause.mockReturnValue(d.promise);

    const first = pauseRun();
    expect(app.displayState).toBe("pausing");
    expect(app.canPause).toBe(false);

    await pauseRun(); // duplicate ignored
    expect(backend.pause).toHaveBeenCalledTimes(1);

    backend.runs = [runIn(LoopState.StatePaused)];
    d.resolve();
    await first;
    expect(app.displayState).toBe("paused");
    expect(app.canResume).toBe(true);
    expect(app.canStop).toBe(true);
  });

  it("stopping ends in a non-running terminal state with Pause/Stop disabled", async () => {
    app.runs = [runIn(LoopState.StateRunning)];
    backend.stop.mockResolvedValue(undefined);
    backend.runs = [runIn(LoopState.StateStopped)];

    await stopRun();
    expect(app.displayState).toBe("stopped");
    expect(app.canPause).toBe(false);
    expect(app.canStop).toBe(false);
    expect(app.canStart).toBe(true); // startable again per restart rules
  });

  it("reconciles with the authoritative state when Pause fails", async () => {
    app.runs = [runIn(LoopState.StateRunning)];
    backend.pause.mockRejectedValue("nope");
    // The backend never actually paused: it is still running.
    backend.runs = [runIn(LoopState.StateRunning)];

    await pauseRun();
    expect(app.error).toBe("nope");
    expect(app.currentTransition).toBeNull();
    expect(app.displayState).toBe("running");
    expect(app.canPause).toBe(true);
    expect(app.canStop).toBe(true);
  });

  it("reconciles with the authoritative state when Resume fails", async () => {
    app.runs = [runIn(LoopState.StatePaused)];
    backend.resume.mockRejectedValue("nope");
    backend.runs = [runIn(LoopState.StatePaused)];

    await resumeRun();
    expect(app.error).toBe("nope");
    expect(app.displayState).toBe("paused");
    expect(app.canResume).toBe(true);
    expect(app.canStop).toBe(true);
  });

  it("reconciles with the authoritative state when Stop fails", async () => {
    app.runs = [runIn(LoopState.StateRunning)];
    backend.stop.mockRejectedValue("nope");
    backend.runs = [runIn(LoopState.StateRunning)];

    await stopRun();
    expect(app.error).toBe("nope");
    expect(app.displayState).toBe("running");
    expect(app.canStop).toBe(true);
  });
});

describe("switching PRDs reconstructs state without side effects", () => {
  it("keeps per-PRD transitions and never fires a control on selection", async () => {
    app.runs = [runIn(LoopState.StateRunning, "checkout"), runIn(LoopState.StatePaused, "docs")];

    app.selectedPrd = "checkout";
    expect(app.displayState).toBe("running");
    expect(app.canPause).toBe(true);

    app.selectedPrd = "docs";
    expect(app.displayState).toBe("paused");
    expect(app.canResume).toBe(true);

    // Merely selecting must not have started/paused/resumed/stopped anything.
    expect(backend.start).not.toHaveBeenCalled();
    expect(backend.pause).not.toHaveBeenCalled();
    expect(backend.resume).not.toHaveBeenCalled();
    expect(backend.stop).not.toHaveBeenCalled();
  });

  it("scopes a Starting transition to its own PRD", async () => {
    const d = deferred<string>();
    backend.start.mockReturnValue(d.promise);
    app.selectedPrd = "checkout";

    const first = startRun();
    expect(app.currentTransition).toBe("starting");

    app.selectedPrd = "docs";
    expect(app.currentTransition).toBeNull(); // docs isn't transitioning
    expect(app.canStart).toBe(true);

    app.selectedPrd = "checkout";
    d.resolve("run_1");
    await first;
    expect(app.currentTransition).toBeNull();
  });
});

/**
 * A PRD accumulates a run per Start. The controls describe the session you are
 * in, so the run they read has to be the latest one — reading any other means
 * Stop is greyed out while an agent is running, which is the one moment it has
 * to work.
 */
describe("a PRD with more than one run", () => {
  function runAt(id: string, state: LoopState, startedAt: number): RunSnapshot {
    return {
      id,
      prd: "checkout",
      state,
      attempt: 1,
      attemptBudget: 8,
      pendingGitErrors: 0,
      startedAt,
    } as RunSnapshot;
  }

  it("follows the newest run, not the first one listed", () => {
    app.runs = [
      runAt("run_1", LoopState.StateStopped, 1_000),
      runAt("run_2", LoopState.StateRunning, 2_000),
    ];

    expect(app.currentRun?.id).toBe("run_2");
    expect(app.canStop).toBe(true);
    expect(app.canStart).toBe(false);
  });

  // The Go side holds runs in a map, so the list arrives in no particular
  // order. Picking by position would make the controls depend on iteration
  // order — right sometimes, wrong the rest of the time.
  it("does not depend on the order the runs arrive in", () => {
    app.runs = [
      runAt("run_2", LoopState.StateRunning, 2_000),
      runAt("run_1", LoopState.StateStopped, 1_000),
    ];

    expect(app.currentRun?.id).toBe("run_2");
    expect(app.canStop).toBe(true);
  });

  // The rail's status dot reads run state per PRD. A cancelled run stays in the
  // list, so looking at anything but the newest run left the dot red after the
  // user started again.
  it("reports the newest run's state after a cancelled run is restarted", () => {
    app.runs = [
      runAt("run_1", LoopState.StateError, 1_000),
      runAt("run_2", LoopState.StateRunning, 2_000),
    ];

    expect(app.latestRunFor("checkout")?.state).toBe(LoopState.StateRunning);
    expect(app.latestRunFor("elsewhere")).toBeNull();
  });

  it("still stops the second run after start, stop, start", () => {
    app.runs = [runAt("run_1", LoopState.StateRunning, 1_000)];
    expect(app.canStop).toBe(true);

    app.runs = [runAt("run_1", LoopState.StateStopped, 1_000)];
    expect(app.canStop).toBe(false);
    expect(app.canStart).toBe(true);

    app.runs = [
      runAt("run_1", LoopState.StateStopped, 1_000),
      runAt("run_2", LoopState.StateRunning, 2_000),
    ];
    expect(app.canStop).toBe(true);
  });
});
