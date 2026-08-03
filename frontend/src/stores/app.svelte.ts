import {
  api,
  isLogEvent,
  EventKind,
  LoopState,
  onEvents,
  onReady,
  type Settings,
  type LoopEvent,
  type PRDDetail,
  type PRDSummary,
  type Project,
  type Question,
  type RunSnapshot,
} from "../platform";
import { ingest } from "./logs.svelte";

/**
 * Application state.
 *
 * Everything is derived from two sources: a Snapshot taken at startup, and the
 * event stream thereafter. When the stream reports a gap the snapshot is simply
 * retaken — that is the whole recovery story, and it is why the Go side bothers
 * to sequence-number events.
 */

export type View = "stories" | "author" | "settings";

/**
 * A control request that has been sent but not yet resolved.
 *
 * The authoritative session state lives on the Go side; between firing a
 * request and the backend confirming the new state there is a window in which
 * the last-known snapshot is stale. Recording the in-flight transition — keyed
 * by PRD so it survives switching away and back — lets the UI show `Starting`,
 * `Pausing`, etc. and refuse a duplicate of the same action for that PRD.
 */
export type Transition = "starting" | "pausing" | "resuming" | "stopping";

class AppState {
  project = $state<Project | null>(null);
  prds = $state<PRDSummary[]>([]);
  runs = $state<RunSnapshot[]>([]);
  questions = $state<Question[]>([]);
  config = $state<Settings | null>(null);

  /** In-flight control transitions, keyed by PRD name. */
  pending = $state<Record<string, Transition>>({});

  selectedPrd = $state<string | null>(null);
  selectedStory = $state<string | null>(null);
  view = $state<View>("stories");
  detail = $state<PRDDetail | null>(null);

  /** Most recent agent output, for the status bar ticker. */
  activity = $state("");
  error = $state<string | null>(null);
  connected = $state(false);
  /** Ticks once a second so elapsed timers re-render without one timer each. */
  now = $state(Date.now());

  /** The run attached to the selected PRD, if any. */
  get currentRun(): RunSnapshot | null {
    const prd = this.selectedPrd;
    if (!prd) return null;
    return this.runs.find((r) => r.prd === prd) ?? null;
  }

  get currentPrd(): PRDSummary | null {
    return this.prds.find((p) => p.name === this.selectedPrd) ?? null;
  }

  /** The unresolved control transition for the selected PRD, if any. */
  get currentTransition(): Transition | null {
    const prd = this.selectedPrd;
    if (!prd) return null;
    return this.pending[prd] ?? null;
  }

  /**
   * What to show for the selected PRD. An in-flight transition wins over the
   * last-known authoritative state so the UI never displays a stale `idle`
   * while a Start is still resolving.
   */
  get displayState(): string {
    return this.currentTransition ?? this.currentRun?.state ?? LoopState.StateIdle;
  }

  /** Controls are derived from the authoritative state, never from the last
   *  request — a transition in flight disables every control for that PRD. */
  get canStart(): boolean {
    return !this.currentTransition && isStartable(this.currentRun?.state);
  }

  get canResume(): boolean {
    return !this.currentTransition && this.currentRun?.state === LoopState.StatePaused;
  }

  get canPause(): boolean {
    return !this.currentTransition && this.currentRun?.state === LoopState.StateRunning;
  }

  get canStop(): boolean {
    return !this.currentTransition && isActive(this.currentRun?.state);
  }

  /** True while any PRD is running — drives the one animating element. */
  get anyRunning(): boolean {
    return this.runs.some((r) => r.state === "running");
  }

  get runningCount(): number {
    return this.runs.filter((r) => r.state === "running").length;
  }
}

export const app = new AppState();

let unsubscribe: Array<() => void> = [];

/** Wire up the event stream and take the first snapshot. */
export async function connect(): Promise<void> {
  unsubscribe.push(onReady(() => void refresh()));
  unsubscribe.push(
    onEvents((events) => {
      for (const ev of events) apply(ev);
      // Log events go to the ring buffer wholesale; it does its own batching.
      ingest(events.filter((e) => isLogEvent(e.kind)));
    }),
  );

  setInterval(() => (app.now = Date.now()), 1000);
  await refresh();
  app.connected = true;
}

export function disconnect(): void {
  for (const off of unsubscribe) off();
  unsubscribe = [];
}

/** Adopt a fresh snapshot. The recovery path for any gap in the stream. */
export async function refresh(): Promise<void> {
  try {
    const snap = await api.run.snapshot();
    app.project = snap.project ?? null;
    app.prds = snap.prds ?? [];
    app.runs = snap.runs ?? [];
    app.questions = snap.questions ?? [];

    if (!app.selectedPrd && app.prds.length > 0) {
      await selectPrd(app.prds[0].name);
    }
    if (!app.config) {
      app.config = await api.project.getConfig();
    }
  } catch (err) {
    app.error = String(err);
  }
}

/** Apply one event to the in-memory model. */
function apply(ev: LoopEvent): void {
  // A gap means we cannot trust our derived state, so rebuild it wholesale
  // rather than trying to patch around the hole.
  if (ev.dropped) {
    void refresh();
  }

  switch (ev.kind) {
    case EventKind.EvProjectOpened:
    case EventKind.EvPRDChanged:
    case EventKind.EvProgressChange:
      void reloadPrds();
      break;

    case EventKind.EvConfigChanged:
      void api.project.getConfig().then((c) => (app.config = c));
      break;

    case EventKind.EvRunStarted:
    case EventKind.EvRunPaused:
    case EventKind.EvRunResumed:
    case EventKind.EvRunStopped:
    case EventKind.EvRunComplete:
    case EventKind.EvRunError:
      void reloadRuns();
      if (ev.kind === EventKind.EvRunError) app.error = ev.text ?? "the run failed";
      if (ev.kind === EventKind.EvRunComplete) app.activity = "complete";
      break;

    case EventKind.EvStoryStarted:
      app.activity = ev.text ? `${ev.storyId}: ${ev.text}` : (ev.storyId ?? "");
      break;

    case EventKind.EvStoryDone:
    case EventKind.EvStorySkipped:
    case EventKind.EvStoryFailed:
      void reloadPrds();
      break;

    case EventKind.EvAgentText:
      app.activity = truncate(ev.text ?? "", 120);
      break;

    case EventKind.EvAgentTool:
      app.activity = `${ev.agent?.tool ?? "tool"} ${toolArgument(ev)}`.trim();
      break;

    case EventKind.EvQuestionAsked:
    case EventKind.EvQuestionResolved:
      void api.run.questions().then((q) => (app.questions = q));
      break;
  }
}

async function reloadRuns(): Promise<void> {
  try {
    app.runs = await api.run.list();
  } catch (err) {
    app.error = String(err);
  }
}

async function reloadPrds(): Promise<void> {
  try {
    app.prds = await api.prd.list();
    if (app.selectedPrd) {
      app.detail = await api.prd.get(app.selectedPrd);
    }
  } catch {
    // A PRD being rewritten underneath us is normal — the agent edits progress
    // notes constantly. The next event will bring us back into sync.
  }
}

export async function selectPrd(name: string): Promise<void> {
  app.selectedPrd = name;
  app.selectedStory = null;
  try {
    app.detail = await api.prd.get(name);
    const first = app.detail.stories?.find((s) => s.status !== "done");
    app.selectedStory = first?.id ?? app.detail.stories?.[0]?.id ?? null;
  } catch (err) {
    app.error = String(err);
  }
}

/** Show the native folder chooser. A dismissed dialog is not an error. */
export async function pickProject(): Promise<void> {
  app.error = null;
  try {
    const project = await api.project.pick();
    if (!project) return;
    app.selectedPrd = null;
    await refresh();
  } catch (err) {
    app.error = String(err);
  }
}

export async function openProject(path: string): Promise<void> {
  app.error = null;
  try {
    await api.project.open(path);
    app.selectedPrd = null;
    await refresh();
  } catch (err) {
    app.error = String(err);
  }
}

// ------------------------------------------------------------------- runs --

export async function startRun(): Promise<void> {
  const prd = app.selectedPrd;
  if (!prd) return;
  // At most one session per PRD: refuse while a Start (or any action) for this
  // PRD is still resolving, or while a live session already exists.
  if (app.pending[prd]) return;
  const existing = app.runs.find((r) => r.prd === prd);
  if (existing && isActive(existing.state)) return;

  setPending(prd, "starting");
  // Dismiss any stale start-error dialog for this PRD; a fresh failure re-sets it.
  app.error = null;
  try {
    await api.run.start({ prd } as never);
    await reloadRuns();
  } catch (err) {
    app.error = String(err);
  } finally {
    clearPending(prd);
  }
}

export function pauseRun(): Promise<void> {
  return transition("pausing", (s) => s === LoopState.StateRunning, (id) => api.run.pause(id));
}

export function resumeRun(): Promise<void> {
  return transition("resuming", (s) => s === LoopState.StatePaused, (id) => api.run.resume(id));
}

export function stopRun(): Promise<void> {
  return transition("stopping", isActive, (id) => api.run.stop(id));
}

/**
 * Fire a Pause/Resume/Stop and track it as an in-flight transition.
 *
 * The action only fires when the authoritative state allows it and no other
 * request for the PRD is outstanding, so duplicate requests are impossible.
 * On failure the run list is re-fetched to reconcile with whatever the backend
 * actually did before the error is surfaced, leaving the controls valid for the
 * reconciled state.
 */
async function transition(
  kind: Transition,
  allowed: (state: LoopState) => boolean,
  fn: (id: string) => Promise<unknown>,
): Promise<void> {
  const prd = app.selectedPrd;
  const run = app.currentRun;
  if (!prd || !run) return;
  if (app.pending[prd]) return;
  if (!allowed(run.state)) return;

  setPending(prd, kind);
  app.error = null;
  try {
    await fn(run.id);
    await reloadRuns();
  } catch (err) {
    await reloadRuns();
    app.error = String(err);
  } finally {
    clearPending(prd);
  }
}

function setPending(prd: string, kind: Transition): void {
  app.pending = { ...app.pending, [prd]: kind };
}

function clearPending(prd: string): void {
  const next = { ...app.pending };
  delete next[prd];
  app.pending = next;
}

export async function adjustBudget(delta: number): Promise<void> {
  const run = app.currentRun;
  if (!run) return;
  await guard(() => api.run.setAttemptBudget(run.id, run.attemptBudget + delta));
}

export async function answerQuestion(id: string, optionId: string): Promise<void> {
  await guard(() => api.run.answer(id, { optionId } as never));
}

async function guard(fn: () => Promise<unknown>): Promise<void> {
  try {
    await fn();
    await reloadRuns();
  } catch (err) {
    app.error = String(err);
  }
}

// -------------------------------------------------------------- utilities --

/** States a PRD can be started from: no live session is in progress. */
const STARTABLE = new Set<LoopState>([
  LoopState.StateIdle,
  LoopState.StateStopped,
  LoopState.StateComplete,
  LoopState.StateError,
]);

/** No session, or one in a terminal/never-started state, can be (re)started. */
function isStartable(state: LoopState | undefined): boolean {
  return state === undefined || STARTABLE.has(state);
}

/** A session that Pause/Resume/Stop can act on. Awaiting is running-but-blocked
 *  on a question, so it is still stoppable. */
function isActive(state: LoopState | undefined): boolean {
  return (
    state === LoopState.StateRunning ||
    state === LoopState.StatePaused ||
    state === LoopState.StateAwaiting
  );
}

function truncate(s: string, n: number): string {
  const flat = s.replace(/\s+/g, " ").trim();
  return flat.length > n ? flat.slice(0, n - 1) + "…" : flat;
}

/** The headline argument of a tool call, for the activity ticker. */
export function toolArgument(ev: LoopEvent): string {
  const input = ev.agent?.toolInput as Record<string, unknown> | undefined;
  if (!input) return "";
  for (const key of ["file_path", "path", "command", "pattern", "url", "description"]) {
    const v = input[key];
    if (typeof v === "string" && v) return truncate(v, 80);
  }
  return "";
}
