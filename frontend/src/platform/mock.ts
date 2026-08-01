/**
 * An in-browser stand-in for the Go backend.
 *
 * Two things it buys, both of which pay for it many times over:
 *
 *  - The whole UI runs in an ordinary browser with hot reload. Rebuilding an
 *    8 MB Go binary to adjust a border radius is intolerable, and it is exactly
 *    the friction that makes people stop refining an interface.
 *  - Behaviour that is awkward to produce on demand — a thousand log lines a
 *    second, a dropped-event gap, a failed push — becomes a scripted fixture
 *    rather than something you wait for and hope to catch.
 *
 * It is only ever loaded when `window._wails` is absent, so it cannot reach a
 * shipped build.
 */
import { EventKind, LoopState } from "./index";
import type {
  Environment,
  LoopEvent,
  PRDDetail,
  PRDSummary,
  Project,
  Question,
  RunSnapshot,
  Snapshot,
  StorySnap,
} from "./index";

let seq = 0;
const listeners: Array<(events: LoopEvent[]) => void> = [];
let queue: LoopEvent[] = [];

function emit(partial: Partial<LoopEvent> & { kind: EventKind }): void {
  queue.push({ seq: ++seq, at: Date.now(), ...partial } as LoopEvent);
}

// Batched on the same cadence as the Go bridge, so the frontend's ingest path
// sees the shape it will see in production.
setInterval(() => {
  if (queue.length === 0) return;
  const batch = queue;
  queue = [];
  for (const fn of listeners) fn(batch);
}, 50);

const project: Project = {
  root: "/Users/you/Code/checkout",
  name: "checkout",
  isGitRepo: true,
  branch: "main",
  defaultBase: "main",
  hasChiefDir: true,
  chiefIgnored: true,
} as Project;

const stories: StorySnap[] = [
  {
    id: "US-001",
    title: "Add Stripe webhook",
    description: "Receive and verify Stripe events at /webhooks/stripe.",
    criteria: ["Signature is verified", "Replays are rejected"],
    priority: 1,
    status: "done",
    criteriaAreAuthoritative: false,
    branch: "loop/checkout/us-001-add-stripe-webhook",
    pr: {
      number: 128,
      url: "https://github.com/acme/checkout/pull/128",
      state: "OPEN",
      draft: true,
      base: "main",
    },
    durationMs: 312_000,
  },
  {
    id: "US-002",
    title: "Persist orders",
    description: "Write completed orders to Postgres so they survive a restart.",
    criteria: ["Orders survive a restart", "Duplicate writes are idempotent"],
    priority: 2,
    status: "in-progress",
    criteriaAreAuthoritative: true,
    branch: "loop/checkout/us-002-persist-orders",
  },
  {
    id: "US-003",
    title: "Send receipts",
    description: "Email a receipt once payment succeeds.",
    criteria: ["Receipt email is sent"],
    priority: 3,
    status: "todo",
    criteriaAreAuthoritative: true,
  },
] as StorySnap[];

const summary: PRDSummary = {
  name: "checkout",
  path: "/Users/you/Code/checkout/.chief/prds/checkout/prd.md",
  title: "Checkout Rework",
  total: 3,
  completed: 1,
  inProgress: 1,
  legacy: false,
  branch: "loop/checkout/us-002-persist-orders",
  state: LoopState.StateRunning,
} as PRDSummary;

const docs: PRDSummary = {
  name: "docs-site",
  path: "/Users/you/Code/checkout/.chief/prds/docs-site/prd.md",
  title: "Documentation Site",
  total: 6,
  completed: 2,
  inProgress: 0,
  legacy: false,
  state: LoopState.StateIdle,
} as PRDSummary;

let run: RunSnapshot = {
  id: "run_1",
  prd: "checkout",
  state: LoopState.StateRunning,
  storyId: "US-002",
  attempt: 4,
  attemptBudget: 8,
  startedAt: Date.now() - 252_000,
  provider: "claude",
  pendingGitErrors: 0,
} as RunSnapshot;

const environment: Environment = {
  git: { name: "git", available: true, version: "2.50.1" },
  gh: { name: "gh", available: true, version: "2.68.0" },
  ghAuth: true,
  ghStack: { name: "gh-stack", available: true },
  agents: [{ name: "claude", available: true }],
  chiefRef: "v0.8.0",
  platform: "darwin",
  stackMode: "gh-stack",
} as Environment;

/** A scripted agent, so the log has something realistic in it. */
const script: Array<Partial<LoopEvent> & { kind: EventKind }> = [
  { kind: EventKind.EvStoryStarted, prd: "checkout", storyId: "US-002", text: "Persist orders" },
  { kind: EventKind.EvAgentText, prd: "checkout", text: "Looking at the existing order model." },
  {
    kind: EventKind.EvAgentTool,
    prd: "checkout",
    agent: { tool: "Read", toolInput: { file_path: "internal/orders/model.go" } },
  } as never,
  { kind: EventKind.EvAgentToolResult, prd: "checkout", text: "package orders\n\ntype Order struct {" },
  { kind: EventKind.EvAgentText, prd: "checkout", text: "Adding a Postgres-backed repository." },
  {
    kind: EventKind.EvAgentTool,
    prd: "checkout",
    agent: { tool: "Write", toolInput: { file_path: "internal/orders/postgres.go" } },
  } as never,
  {
    kind: EventKind.EvAgentTool,
    prd: "checkout",
    agent: { tool: "Bash", toolInput: { command: "go test ./internal/orders/..." } },
  } as never,
  { kind: EventKind.EvAgentToolResult, prd: "checkout", text: "ok  internal/orders  0.42s" },
];

let tick = 0;
setInterval(() => {
  if (run.state !== "running") return;
  emit(script[tick % script.length]);
  tick++;
}, 900);

export const mockApi = {
  project: {
    open: async (path: string): Promise<Project> => ({ ...project, root: path }),
    pick: async (): Promise<Project | null> => project,
    current: async (): Promise<Project | null> => project,
    environment: async (): Promise<Environment> => environment,
    getConfig: async () =>
      ({
        worktree: { setup: "" },
        onComplete: { push: false, createPR: false },
        agent: { provider: "claude", cliPath: "" },
        git: {
          mode: "per-story",
          stackDriver: "auto",
          baseBranch: "",
          branchTemplate: "loop/{prd}/{story}-{slug}",
          draft: true,
          requireWorktree: true,
          verifyCommit: true,
        },
      }) as never,
    saveConfig: async (): Promise<void> => {},
    rescan: async (): Promise<void> => {},
  },
  prd: {
    list: async (): Promise<PRDSummary[]> => [summary, docs],
    get: async (name: string): Promise<PRDDetail> =>
      ({
        ...(name === "checkout" ? summary : docs),
        description: "Replace the legacy checkout flow with something maintainable.",
        stories: name === "checkout" ? stories : [],
      }) as PRDDetail,
    progress: async () => ({}) as never,
  },
  run: {
    start: async (): Promise<string> => {
      run = { ...run, state: LoopState.StateRunning, startedAt: Date.now() };
      emit({ kind: EventKind.EvRunStarted, prd: "checkout" });
      return run.id;
    },
    pause: async (): Promise<void> => {
      run = { ...run, state: LoopState.StatePaused };
      emit({ kind: EventKind.EvRunPaused, prd: "checkout" });
    },
    resume: async (): Promise<void> => {
      run = { ...run, state: LoopState.StateRunning };
      emit({ kind: EventKind.EvRunResumed, prd: "checkout" });
    },
    stop: async (): Promise<void> => {
      run = { ...run, state: LoopState.StateStopped, finishedAt: Date.now() };
      emit({ kind: EventKind.EvRunStopped, prd: "checkout" });
    },
    setAttemptBudget: async (_id: string, n: number): Promise<void> => {
      run = { ...run, attemptBudget: n };
      emit({ kind: EventKind.EvRunStarted, prd: "checkout" });
    },
    list: async (): Promise<RunSnapshot[]> => [run],
    snapshot: async (): Promise<Snapshot> =>
      ({
        seq,
        project,
        prds: [summary, docs],
        runs: [run],
        questions: [] as Question[],
        environment,
      }) as Snapshot,
    replay: async () => ({ events: [], complete: true }) as never,
    answer: async (): Promise<void> => {},
    questions: async (): Promise<Question[]> => [],
  },
};

export function mockOnEvents(handler: (events: LoopEvent[]) => void): () => void {
  listeners.push(handler);
  return () => {
    const i = listeners.indexOf(handler);
    if (i >= 0) listeners.splice(i, 1);
  };
}

export function mockOnReady(handler: () => void): () => void {
  setTimeout(handler, 0);
  return () => {};
}
