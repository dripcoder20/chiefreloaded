import {
  api,
  isLogEvent,
  EventKind,
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
 * Usage read model, mirroring the Go `session.UsageReport` json shape. Defined
 * locally rather than imported from the generated bindings for the same reason
 * `mock.ts` does: the bindings are absent in a browser dev checkout, and a plain
 * structural type lets both paths share one contract without a build step.
 */
export type UsageTotals = {
  records: number;
  inputTokens: number;
  outputTokens: number;
  reasoningTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  totalTokens: number;
  cost: number;
  currency: string;
};

export type UsageReport = {
  project: UsageTotals;
  runs: Record<string, UsageTotals>;
  stories: Record<string, UsageTotals>;
  attempts: Record<string, UsageTotals>;
};

class AppState {
  project = $state<Project | null>(null);
  prds = $state<PRDSummary[]>([]);
  runs = $state<RunSnapshot[]>([]);
  questions = $state<Question[]>([]);
  config = $state<Settings | null>(null);

  selectedPrd = $state<string | null>(null);
  selectedStory = $state<string | null>(null);
  view = $state<View>("stories");
  detail = $state<PRDDetail | null>(null);

  /**
   * Absolute cumulative usage roll-up, adopted wholesale from the snapshot and
   * replaced by each EvUsage event's report. Never a delta, so a replayed or
   * reconnected event is idempotent.
   */
  usage = $state<UsageReport | null>(null);

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

  /** True while any PRD is running — drives the one animating element. */
  get anyRunning(): boolean {
    return this.runs.some((r) => r.state === "running");
  }

  get runningCount(): number {
    return this.runs.filter((r) => r.state === "running").length;
  }

  /**
   * The session (per-run) and current-story usage slices for the selected PRD's
   * run. Keyed exactly as the Go ledger keys them: run id, and run id + "/" +
   * story id. Undefined slices mean no usage has been attributed there yet.
   */
  get currentUsage(): { session?: UsageTotals; story?: UsageTotals } {
    const run = this.currentRun;
    if (!run || !this.usage) return {};
    const story = run.storyId ? this.usage.stories[`${run.id}/${run.storyId}`] : undefined;
    return { session: this.usage.runs[run.id], story };
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
    app.usage = (snap as { usage?: UsageReport }).usage ?? null;

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

    case EventKind.EvUsage: {
      // The report is an absolute roll-up, so adopt it wholesale; replay and
      // reconnect both re-adopt the same numbers rather than adding.
      const report = (ev as { usageReport?: UsageReport }).usageReport;
      if (report) app.usage = report;
      break;
    }
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
  app.error = null;
  try {
    await api.run.start({ prd } as never);
    await reloadRuns();
  } catch (err) {
    app.error = String(err);
  }
}

export async function pauseRun(): Promise<void> {
  const run = app.currentRun;
  if (run) await guard(() => api.run.pause(run.id));
}

export async function resumeRun(): Promise<void> {
  const run = app.currentRun;
  if (run) await guard(() => api.run.resume(run.id));
}

export async function stopRun(): Promise<void> {
  const run = app.currentRun;
  if (run) await guard(() => api.run.stop(run.id));
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
