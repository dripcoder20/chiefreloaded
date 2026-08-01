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

// ------------------------------------------------------------------- usage --
//
// Browser development needs usage that looks like a real project's history, not
// a single tidy row. Two things have to be visible to exercise the status bar:
// historical usage from a finished run and live usage streaming into the active
// one, and both a fully supported provider (claude — every token class plus
// cost) and a partially supported one (codex — input/cache/output only, no
// reasoning, no cache-write, no cost). A partial provider is not an error state;
// the UI must render "cost unknown" without falling over.
//
// The report is always rebuilt from the record list with buildUsageReport, the
// same absolute-total invariant the Go ledger keeps: a reconnecting consumer
// adopts the numbers wholesale and a replayed event re-adopts them rather than
// adding, so nothing is ever double counted.

type UsageGroup = {
  provider?: string;
  model?: string;
  currency?: string;
  records: number;
  inputTokens: number;
  outputTokens: number;
  reasoningTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  totalTokens: number;
  cost: number;
  hasCost: boolean;
  costKind?: string;
  contextWindow?: number;
  peakContextTokens?: number;
};

type UsageTotals = {
  records: number;
  inputTokens: number;
  outputTokens: number;
  reasoningTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  totalTokens: number;
  cost: number;
  currency: string;
  groups: UsageGroup[];
};

type UsageRecord = {
  key: string;
  runId: string;
  prd?: string;
  storyId?: string;
  attempt: number;
  provider?: string;
  model?: string;
  at: number;
  inputTokens?: number;
  outputTokens?: number;
  reasoningTokens?: number;
  cacheReadTokens?: number;
  cacheWriteTokens?: number;
  totalTokens?: number;
  contextWindow?: number;
  cost?: number;
  estimated?: boolean;
  currency?: string;
};

type UsageReport = {
  project: UsageTotals;
  runs: Record<string, UsageTotals>;
  stories: Record<string, UsageTotals>;
  attempts: Record<string, UsageTotals>;
};

function emptyTotals(): UsageTotals {
  return {
    records: 0,
    inputTokens: 0,
    outputTokens: 0,
    reasoningTokens: 0,
    cacheReadTokens: 0,
    cacheWriteTokens: 0,
    totalTokens: 0,
    cost: 0,
    currency: "",
    groups: [],
  };
}

/** recordTotal derives a record's token count the way the Go ledger does: the
 *  reported total when present, otherwise the sum of the reported components. */
function recordTotal(rec: UsageRecord): number {
  if (rec.totalTokens != null) return rec.totalTokens;
  return (
    (rec.inputTokens ?? 0) +
    (rec.outputTokens ?? 0) +
    (rec.reasoningTokens ?? 0) +
    (rec.cacheReadTokens ?? 0) +
    (rec.cacheWriteTokens ?? 0)
  );
}

function foldRecord(t: UsageTotals, rec: UsageRecord): void {
  t.records += 1;
  t.inputTokens += rec.inputTokens ?? 0;
  t.outputTokens += rec.outputTokens ?? 0;
  t.reasoningTokens += rec.reasoningTokens ?? 0;
  t.cacheReadTokens += rec.cacheReadTokens ?? 0;
  t.cacheWriteTokens += rec.cacheWriteTokens ?? 0;
  t.totalTokens += recordTotal(rec);
  if (rec.cost != null) t.cost += rec.cost;
  if (t.currency === "" && rec.currency) t.currency = rec.currency;
  foldGroup(t, rec);
}

/** foldGroup mirrors the Go ledger's per-(provider, model, currency) grouping so
 *  the mock exercises the detailed panel's grouped rendering faithfully. */
function foldGroup(t: UsageTotals, rec: UsageRecord): void {
  const provider = rec.provider ?? "";
  const model = rec.model ?? "";
  const currency = rec.currency ?? "";
  let g = t.groups.find(
    (x) => x.provider === provider && x.model === model && x.currency === currency,
  );
  if (!g) {
    g = {
      provider,
      model,
      currency,
      records: 0,
      inputTokens: 0,
      outputTokens: 0,
      reasoningTokens: 0,
      cacheReadTokens: 0,
      cacheWriteTokens: 0,
      totalTokens: 0,
      cost: 0,
      hasCost: false,
    };
    t.groups.push(g);
  }
  g.records += 1;
  g.inputTokens += rec.inputTokens ?? 0;
  g.outputTokens += rec.outputTokens ?? 0;
  g.reasoningTokens += rec.reasoningTokens ?? 0;
  g.cacheReadTokens += rec.cacheReadTokens ?? 0;
  g.cacheWriteTokens += rec.cacheWriteTokens ?? 0;
  const total = recordTotal(rec);
  g.totalTokens += total;
  g.peakContextTokens = Math.max(g.peakContextTokens ?? 0, total);
  if (rec.contextWindow != null) {
    g.contextWindow = Math.max(g.contextWindow ?? 0, rec.contextWindow);
  }
  if (rec.cost != null) {
    g.cost += rec.cost;
    g.hasCost = true;
    noteCostKind(g, rec.estimated === true);
  }
}

function noteCostKind(g: UsageGroup, estimated: boolean): void {
  const kind = estimated ? "estimated" : "reported";
  if (!g.costKind) {
    g.costKind = kind;
    return;
  }
  if (g.costKind !== kind) g.costKind = "mixed";
}

function accumulate(m: Record<string, UsageTotals>, key: string, rec: UsageRecord): void {
  const totals = m[key] ?? emptyTotals();
  foldRecord(totals, rec);
  m[key] = totals;
}

/** buildUsageReport rebuilds the absolute roll-up from the ordered record set,
 *  mirroring the Go ledger so the mock stays faithful to the real invariant. */
function buildUsageReport(records: UsageRecord[]): UsageReport {
  const report: UsageReport = { project: emptyTotals(), runs: {}, stories: {}, attempts: {} };
  for (const rec of records) {
    foldRecord(report.project, rec);
    accumulate(report.runs, rec.runId, rec);
    if (!rec.storyId) continue;
    accumulate(report.stories, `${rec.runId}/${rec.storyId}`, rec);
    accumulate(report.attempts, `${rec.runId}/${rec.storyId}#${rec.attempt}`, rec);
  }
  return report;
}

const HOUR = 3_600_000;

// A finished run on a different PRD, driven by codex — a partially supported
// provider that reports input, cached-input and output tokens but no reasoning,
// no cache-write and no cost. Its currency stays empty on purpose.
const historicalUsage: UsageRecord[] = [
  {
    key: "run_prev/US-006#1:0",
    runId: "run_prev",
    prd: "docs-site",
    storyId: "US-006",
    attempt: 1,
    provider: "codex",
    model: "gpt-5-codex",
    at: Date.now() - 5 * HOUR,
    inputTokens: 8_200,
    outputTokens: 1_400,
    cacheReadTokens: 16_000,
  },
  {
    key: "run_prev/US-007#1:0",
    runId: "run_prev",
    prd: "docs-site",
    storyId: "US-007",
    attempt: 1,
    provider: "codex",
    model: "gpt-5-codex",
    at: Date.now() - 4 * HOUR,
    inputTokens: 9_100,
    outputTokens: 1_750,
    cacheReadTokens: 21_000,
  },
  // The active run so far, driven by claude — every token class plus a reported
  // cost. US-001 is done; US-002 has already burned three attempts.
  {
    key: "run_1/US-001#1:0",
    runId: "run_1",
    prd: "checkout",
    storyId: "US-001",
    attempt: 1,
    provider: "claude",
    model: "claude-opus-4",
    at: Date.now() - 3 * HOUR,
    inputTokens: 22_000,
    outputTokens: 2_600,
    cacheReadTokens: 65_000,
    cacheWriteTokens: 4_500,
    cost: 0.31,
    currency: "USD",
  },
  {
    key: "run_1/US-002#1:0",
    runId: "run_1",
    prd: "checkout",
    storyId: "US-002",
    attempt: 1,
    provider: "claude",
    model: "claude-opus-4",
    at: Date.now() - 2 * HOUR,
    inputTokens: 18_000,
    outputTokens: 2_100,
    cacheReadTokens: 52_000,
    cacheWriteTokens: 3_800,
    cost: 0.24,
    currency: "USD",
  },
  {
    key: "run_1/US-002#2:0",
    runId: "run_1",
    prd: "checkout",
    storyId: "US-002",
    attempt: 2,
    provider: "claude",
    model: "claude-opus-4",
    at: Date.now() - 90 * 60_000,
    inputTokens: 15_500,
    outputTokens: 1_800,
    cacheReadTokens: 47_000,
    cacheWriteTokens: 3_100,
    cost: 0.2,
    currency: "USD",
  },
  {
    key: "run_1/US-002#3:0",
    runId: "run_1",
    prd: "checkout",
    storyId: "US-002",
    attempt: 3,
    provider: "claude",
    model: "claude-opus-4",
    at: Date.now() - 30 * 60_000,
    inputTokens: 16_800,
    outputTokens: 1_950,
    cacheReadTokens: 49_000,
    cacheWriteTokens: 3_300,
    cost: 0.22,
    currency: "USD",
  },
];

// Every usage record the mock has accepted, in delivery order. The scripted run
// appends attempt-4 payloads to it, so both the snapshot and each live event
// report the same absolute roll-up.
const usageRecords: UsageRecord[] = [...historicalUsage];
let usageSeq = 0;

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
  // Every few log lines the active attempt reports usage. It accumulates onto
  // the record set, so each event carries the fresh absolute roll-up.
  if (tick % 3 === 0) emitUsage();
}, 900);

/** emitUsage streams one attempt-4 usage payload for the active claude run and
 *  publishes the absolute roll-up rebuilt from every accepted record. */
function emitUsage(): void {
  const rec: UsageRecord = {
    key: `run_1/US-002#4:${usageSeq++}`,
    runId: "run_1",
    prd: "checkout",
    storyId: "US-002",
    attempt: 4,
    provider: "claude",
    model: "claude-opus-4",
    at: Date.now(),
    inputTokens: 2_400 + usageSeq * 40,
    outputTokens: 260 + usageSeq * 6,
    cacheReadTokens: 7_000 + usageSeq * 30,
    cacheWriteTokens: 500 + usageSeq * 5,
    contextWindow: 200_000,
    cost: 0.03,
    currency: "USD",
  };
  usageRecords.push(rec);
  emit({
    kind: EventKind.EvUsage,
    prd: "checkout",
    runId: "run_1",
    storyId: "US-002",
    attempt: 4,
    usage: rec,
    usageReport: buildUsageReport(usageRecords),
  } as never);
}

const authorListeners: Array<(ev: { sessionId: string; data: string }) => void> = [];

export function mockOnAuthorData(
  handler: (ev: { sessionId: string; data: string }) => void,
): () => void {
  authorListeners.push(handler);
  return () => {
    const i = authorListeners.indexOf(handler);
    if (i >= 0) authorListeners.splice(i, 1);
  };
}

export function mockOnAuthorExit(_handler: (ev: never) => void): () => void {
  // The mock session never exits; browser development only needs the terminal
  // to be visibly wired up.
  return () => {};
}

let mockPrompt = "";

export const mockAuthor = {
  start: async (): Promise<string> => {
    const id = "chat_mock";
    // A few lines so the terminal is visibly wired up in browser development.
    setTimeout(() => {
      const text =
        "\u001b[36m? Chief PRD Generator\u001b[0m\r\n\r\n" +
        "1. What is the primary goal?\r\n   A. Reduce support burden\r\n" +
        "   B. Improve onboarding\r\n\r\n> ";
      for (const fn of authorListeners) fn({ sessionId: id, data: btoa(text) });
    }, 200);
    return id;
  },
  write: async (): Promise<void> => {},
  resize: async (): Promise<void> => {},
  interrupt: async (): Promise<void> => {},
  stop: async (): Promise<void> => {},
  scrollback: async (): Promise<string> => "",
  getPrompt: async (kind: string) => ({
    kind,
    body: mockPrompt || "# Chief PRD Generator\n\nCreate a PRD at {{PRD_DIR}}/prd.md.\n\n{{CONTEXT}}\n",
    custom: mockPrompt !== "",
    path: `.chief/prompts/${kind}.md`,
  }),
  savePrompt: async (_kind: string, body: string): Promise<void> => {
    mockPrompt = body;
  },
  resetPrompt: async (): Promise<void> => {
    mockPrompt = "";
  },
  builtinPrompt: async (): Promise<string> =>
    "# Chief PRD Generator\n\nCreate a PRD at {{PRD_DIR}}/prd.md.\n\n{{CONTEXT}}\n",
};

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
        usage: buildUsageReport(usageRecords),
      }) as Snapshot,
    replay: async () => ({ events: [], complete: true }) as never,
    answer: async (): Promise<void> => {},
    questions: async (): Promise<Question[]> => [],
  },
  author: mockAuthor,
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
