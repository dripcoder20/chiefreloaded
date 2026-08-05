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
  AuthoringService,
  PRDService,
  ProjectService,
  RunService,
} from "../../bindings/github.com/dripcoder/loop/internal/services";
import type { AuthorEvent, AuthorExit } from "../../bindings/github.com/dripcoder/loop/internal/session/models";
import type { Spec as AuthorSpec } from "../../bindings/github.com/dripcoder/loop/internal/authoring/models";
import type { Prompt } from "../../bindings/github.com/dripcoder/loop/internal/prompts/models";
import { EventKind, LoopState } from "../../bindings/github.com/dripcoder/loop/internal/session/models";
import type {
  AgentDefaults,
  AppStatus,
  DestinationStatus,
  Environment,
  NewPRDRequest,
  PRDWorkflow,
  PublishReport,
  PullRequestSet,
  PRRef,
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
import {
  mockApi,
  mockOnAuthorData,
  mockOnAuthorExit,
  mockOnEvents,
  mockOnMenuNewPRD,
  mockOnReady,
} from "./mock";

export { EventKind, LoopState };

export type {
  AgentDefaults,
  AppStatus,
  DestinationStatus,
  Environment,
  NewPRDRequest,
  PRDWorkflow,
  PublishReport,
  PullRequestSet,
  PRRef,
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
  AuthorEvent,
  AuthorExit,
  AuthorSpec,
  Prompt,
};

/** Event names the Go bridge emits. Kept in sync with main.go. */
export const EVENT_BATCH = "loop:events";
export const EVENT_READY = "loop:ready";
export const EVENT_AUTHOR = "loop:author";
export const EVENT_AUTHOR_EXIT = "loop:author:exit";
export const EVENT_MENU_NEW_PRD = "loop:menu:new-prd";
export const EVENT_MENU_OPEN_PROJECT = "loop:menu:open-project";
export const EVENT_MENU_SETTINGS = "loop:menu:settings";

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

/** Terminal output from an authoring session. */
export function onAuthorData(handler: (ev: AuthorEvent) => void): () => void {
  if (!isDesktop) return mockOnAuthorData(handler);
  return Events.On(EVENT_AUTHOR, (e: { data: unknown }) => handler(unwrap(e) as AuthorEvent));
}

/** Fires once an authoring session has exited, with what it produced. */
export function onAuthorExit(handler: (ev: AuthorExit) => void): () => void {
  if (!isDesktop) return mockOnAuthorExit(handler);
  return Events.On(EVENT_AUTHOR_EXIT, (e: { data: unknown }) => handler(unwrap(e) as AuthorExit));
}

/** Fires when the File ▸ New PRD menu item or its ⌘N / Ctrl+N shortcut is used. */
export function onMenuNewPRD(handler: () => void): () => void {
  if (!isDesktop) return mockOnMenuNewPRD(handler);
  return Events.On(EVENT_MENU_NEW_PRD, () => handler());
}

/** Fires when File ▸ Open Project… or its ⌘O / Ctrl+O shortcut is used. */
export function onMenuOpenProject(handler: () => void): () => void {
  if (!isDesktop) return mockOnMenuNewPRD(() => {});
  return Events.On(EVENT_MENU_OPEN_PROJECT, () => handler());
}

/** Fires when the app menu's Settings… item or its ⌘, shortcut is used. */
export function onMenuSettings(handler: () => void): () => void {
  if (!isDesktop) return mockOnMenuNewPRD(() => {});
  return Events.On(EVENT_MENU_SETTINGS, () => handler());
}

/**
 * Wails wraps single-argument payloads inconsistently across versions: some
 * deliver the value, some a one-element array. Normalise rather than depend on
 * which, since the difference only shows up at runtime.
 */
function unwrap(e: { data: unknown }): unknown {
  const d = e?.data;
  return Array.isArray(d) ? d[0] : d;
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
    localApps: (): Promise<AppStatus[]> => list(ProjectService.LocalApps()),
    agentDefaults: (): Promise<AgentDefaults> => ProjectService.AgentDefaults(),
    issueDestinations: (): Promise<DestinationStatus[]> =>
      list(ProjectService.IssueDestinations()),
    openInApp: (app: string): Promise<void> => ProjectService.OpenInApp(app),
    openOnGitHub: (): Promise<void> => ProjectService.OpenOnGitHub(),
  },
  prd: {
    list: (): Promise<PRDSummary[]> => list(PRDService.List()),
    get: (name: string): Promise<PRDDetail> => PRDService.Get(name),
    progress: (name: string) => PRDService.Progress(name),
    openFile: (name: string): Promise<void> => PRDService.OpenFile(name),
    create: (req: NewPRDRequest): Promise<PRDSummary> => PRDService.Create(req),
    delete: (name: string): Promise<void> => PRDService.Delete(name),
    workflow: (name: string): Promise<PRDWorkflow> => PRDService.Workflow(name),
    saveWorkflow: (name: string, w: PRDWorkflow): Promise<void> =>
      PRDService.SaveWorkflow(name, w),
    issues: (name: string) => PRDService.Issues(name),
    pullRequests: (name: string): Promise<PullRequestSet> => PRDService.PullRequests(name),
    refreshPullRequests: (name: string): Promise<PullRequestSet> =>
      PRDService.RefreshPullRequests(name),
    publish: (name: string): Promise<PublishReport> => PRDService.Publish(name),
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
  author: {
    start: (spec: AuthorSpec): Promise<string> => AuthoringService.Start(spec),
    write: (id: string, base64: string): Promise<void> => AuthoringService.Write(id, base64),
    resize: (id: string, cols: number, rows: number): Promise<void> =>
      AuthoringService.Resize(id, cols, rows),
    interrupt: (id: string): Promise<void> => AuthoringService.Interrupt(id),
    stop: (id: string): Promise<void> => AuthoringService.Stop(id),
    scrollback: (id: string): Promise<string> => AuthoringService.Scrollback(id),
    getPrompt: (kind: string): Promise<Prompt> => AuthoringService.GetPrompt(kind),
    savePrompt: (kind: string, body: string): Promise<void> =>
      AuthoringService.SavePrompt(kind, body),
    resetPrompt: (kind: string): Promise<void> => AuthoringService.ResetPrompt(kind),
    builtinPrompt: (kind: string): Promise<string> => AuthoringService.BuiltinPrompt(kind),
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
