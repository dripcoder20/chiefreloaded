/**
 * The only place `@wailsio/runtime` and the generated bindings are imported.
 *
 * Everything else in the app talks to this module. Two reasons, both practical:
 * Wails v3 is alpha and its API moves, so a breaking change should touch one
 * file rather than forty; and with the boundary in place the whole UI can run in
 * an ordinary browser against `mock.ts`, which matters because rebuilding a Go
 * binary to adjust a border radius is intolerable.
 */
import { Events } from "@wailsio/runtime";
import {
  PRDService,
  ProjectService,
  RunService,
} from "../../bindings/github.com/dripcoder/loop/internal/services";
import { EventKind, LoopState } from "../../bindings/github.com/dripcoder/loop/internal/session/models";
import type {
  Environment,
  Event as LoopEvent,
  PRDDetail,
  PRDSummary,
  Project,
  RunSnapshot,
  Snapshot,
  StartRequest,
  Question,
  Answer,
  StorySnap,
  Settings,
} from "../../bindings/github.com/dripcoder/loop/internal/session/models";
import { mockApi, mockOnEvents, mockOnReady } from "./mock";

export { EventKind, LoopState };

export type {
  Environment,
  LoopEvent,
  PRDDetail,
  PRDSummary,
  Project,
  RunSnapshot,
  Snapshot,
  StartRequest,
  Question,
  Answer,
  StorySnap,
  Settings,
};

/** Event names the Go bridge emits. Kept in sync with main.go. */
export const EVENT_BATCH = "loop:events";
export const EVENT_READY = "loop:ready";

/**
 * True when running inside a real webview rather than a browser preview.
 *
 * `window._wails` is not the test: the Wails Vite plugin injects that shim into
 * the browser too, so it is present in both. What is only ever present in an
 * actual embedded webview is the platform's own native message bridge —
 * `webkit.messageHandlers` under WKWebView and WebKitGTK, `chrome.webview`
 * under WebView2. Testing for those is also stable against Wails' alpha churn,
 * since they belong to the platform rather than to the framework.
 */
export const isDesktop =
  typeof window !== "undefined" &&
  Boolean(
    (window as { webkit?: { messageHandlers?: unknown } }).webkit?.messageHandlers ||
      (window as { chrome?: { webview?: unknown } }).chrome?.webview,
  );

/**
 * Subscribe to the event firehose. Returns an unsubscribe function.
 *
 * The Go side batches, so each callback receives an array. It is already
 * ordered and sequence-numbered; the caller checks `seq` continuity to detect
 * gaps rather than trusting delivery.
 */
export function onEvents(handler: (events: LoopEvent[]) => void): () => void {
  if (!isDesktop) return mockOnEvents(handler);
  return Events.On(EVENT_BATCH, (e: { data: unknown }) => {
    // Wails wraps the payload, and a single-argument Emit arrives unwrapped in
    // some versions. Normalise rather than depend on which.
    const payload = Array.isArray(e?.data) ? e.data : (e?.data as { 0?: unknown })?.[0];
    if (Array.isArray(payload)) handler(payload as LoopEvent[]);
  });
}

/** Fires once the Go event bridge is live. */
export function onReady(handler: () => void): () => void {
  if (!isDesktop) return mockOnReady(handler);
  return Events.On(EVENT_READY, () => handler());
}

/**
 * Go marshals a nil slice as `null`, not `[]`, so every list-returning binding
 * is typed `T[] | null`. An empty PRD list is entirely normal — a project that
 * has not been set up yet — and letting that null reach a `.find()` would crash
 * the UI on exactly the screen a first-time user sees.
 *
 * Normalising once here beats a `?? []` at every call site, which is the kind of
 * thing that is right in code review and forgotten six months later.
 */
async function list<T>(p: Promise<T[] | null>): Promise<T[]> {
  return (await p) ?? [];
}

const wailsApi = {
  project: {
    open: (path: string): Promise<Project> => ProjectService.Open(path),
    pick: (): Promise<Project | null> => ProjectService.Pick(),
    current: (): Promise<Project | null> => ProjectService.Current(),
    environment: (): Promise<Environment> => ProjectService.Environment(),
    getConfig: (): Promise<Settings> => ProjectService.GetConfig(),
    saveConfig: (v: Settings): Promise<void> => ProjectService.SaveConfig(v),
    rescan: (): Promise<void> => ProjectService.Rescan(),
  },
  prd: {
    list: (): Promise<PRDSummary[]> => list(PRDService.List()),
    get: (name: string): Promise<PRDDetail> => PRDService.Get(name),
    progress: (name: string) => PRDService.Progress(name),
  },
  run: {
    start: (req: StartRequest): Promise<string> => RunService.Start(req),
    pause: (id: string): Promise<void> => RunService.Pause(id),
    resume: (id: string): Promise<void> => RunService.Resume(id),
    stop: (id: string): Promise<void> => RunService.Stop(id),
    setAttemptBudget: (id: string, n: number): Promise<void> =>
      RunService.SetAttemptBudget(id, n),
    list: (): Promise<RunSnapshot[]> => list(RunService.List()),
    snapshot: (): Promise<Snapshot> => RunService.Snapshot(),
    replay: (sinceSeq: number) => RunService.Replay(sinceSeq),
    answer: (id: string, a: Answer): Promise<void> => RunService.Answer(id, a),
    questions: (): Promise<Question[]> => list(RunService.Questions()),
  },
};

/**
 * Outside a real webview every call goes to the mock. That is what lets the
 * entire interface run under `npm run dev` with hot reload; the check is for the
 * platform's native bridge, so a shipped build can never take this path.
 */
export const api = isDesktop ? wailsApi : (mockApi as unknown as typeof wailsApi);

/**
 * Kinds that belong in the log stream rather than driving state.
 *
 * Built from the generated enum rather than a hand-written copy of the strings:
 * a second list of event names would drift from Go the first time one was
 * renamed, and nothing would fail loudly when it did.
 */
const LOG_KINDS = new Set<EventKind>([
  EventKind.EvAgentText,
  EventKind.EvAgentTool,
  EventKind.EvAgentToolResult,
  EventKind.EvAgentRetry,
  EventKind.EvAgentWatchdog,
  EventKind.EvStoryStarted,
  EventKind.EvStoryDone,
  EventKind.EvStoryFailed,
  EventKind.EvStorySkipped,
  EventKind.EvGit,
  EventKind.EvStep,
  EventKind.EvRunError,
  EventKind.EvRunComplete,
]);

export function isLogEvent(kind: EventKind): boolean {
  return LOG_KINDS.has(kind);
}
