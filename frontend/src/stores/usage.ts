/**
 * The usage read model and its pure presentation logic.
 *
 * This module deliberately imports nothing from `../platform` (and therefore
 * nothing from the generated Wails bindings), so it can be unit-tested in an
 * ordinary Node/Vitest process where those bindings do not exist. `app.svelte`
 * re-exports everything here, so components keep importing from one place.
 *
 * The types mirror the Go `session.UsageReport` json shape, camelCase to match
 * the `json:"..."` tags — the same contract `mock.ts` and the Go side share.
 */

/**
 * A per-(provider, model, currency) subtotal within a scope. When a scope has
 * more than one group its usage came from mixed providers, models or currencies
 * and must be shown group by group rather than summed into one figure.
 */
export type UsageGroup = {
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
  /** "reported", "estimated", or "mixed" once hasCost; absent otherwise. */
  costKind?: string;
  /** Model context-window size in tokens, absent/0 when the provider omits it. */
  contextWindow?: number;
  /** Largest single-payload token footprint, for context utilization. */
  peakContextTokens?: number;
};

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
  groups?: UsageGroup[];
};

/** One story's roll-up within a session, for history browsing. */
export type StoryUsage = {
  storyId: string;
  attempts: number;
  totals: UsageTotals;
};

/**
 * One retained session (run) as the history list shows it. State is one of
 * "active" | "interrupted" | "completed" | "stopped" | "failed": the first two
 * are derived from the live runs, the rest are recorded terminal outcomes.
 */
export type SessionUsage = {
  runId: string;
  prd?: string;
  provider?: string;
  model?: string;
  startedAt: number;
  endedAt?: number;
  state?: string;
  totals: UsageTotals;
  stories?: StoryUsage[];
};

export type UsageReport = {
  project: UsageTotals;
  runs: Record<string, UsageTotals>;
  stories: Record<string, UsageTotals>;
  attempts: Record<string, UsageTotals>;
  /** Retained sessions, newest first, for browsing usage by session and story. */
  sessions?: SessionUsage[];
};

/** The usage-warning thresholds, mirroring the Go `session.UsageSettings` shape. */
export type UsageThresholds = {
  contextWarnPercent: number;
  contextCriticalPercent: number;
  /** Optional per-session cost above which the UI warns; absent = no warning. */
  costWarnAmount?: number;
};

/** Warning severity for a usage dimension. "none" means nothing to flag. */
export type WarnLevel = "none" | "warn" | "critical";

/** The context defaults, matching the Go `DefaultContext*Percent` constants. */
export const DEFAULT_THRESHOLDS: UsageThresholds = {
  contextWarnPercent: 80,
  contextCriticalPercent: 95,
};

// ---------------------------------------------------------------- warnings --

/**
 * Context utilization for a group: the peak single-payload footprint over the
 * model's context window. Null when either is unknown — utilization is only
 * meaningful when both the token count and the window size are known (AC1), and
 * an unknown window must never produce a false warning (AC6).
 */
export function contextUtilization(g: UsageGroup): number | null {
  const window = g.contextWindow ?? 0;
  const peak = g.peakContextTokens ?? 0;
  if (window <= 0 || peak <= 0) return null;
  return peak / window;
}

/** The warning level for a utilization ratio against the configured thresholds. */
export function contextLevel(ratio: number | null, t: UsageThresholds): WarnLevel {
  if (ratio == null) return "none";
  const pct = ratio * 100;
  if (pct >= t.contextCriticalPercent) return "critical";
  if (pct >= t.contextWarnPercent) return "warn";
  return "none";
}

/**
 * The worst context-utilization state across a scope's groups: the highest known
 * utilization and its warning level. Groups without a known window are ignored,
 * so a provider that never reports a window contributes no warning.
 */
export function worstContext(
  totals: UsageTotals | undefined,
  t: UsageThresholds,
): { level: WarnLevel; ratio: number | null } {
  const groups = totals?.groups ?? [];
  let ratio: number | null = null;
  for (const g of groups) {
    const u = contextUtilization(g);
    if (u == null) continue;
    if (ratio == null || u > ratio) ratio = u;
  }
  return { level: contextLevel(ratio, t), ratio };
}

/**
 * Cost warning level for a scope. Only a configured amount over a scope with a
 * known cost (non-empty currency) can warn: an unknown cost is unavailable, not a
 * breach (AC6). Cost breaches are informational, so they never escalate past warn.
 */
export function costLevel(totals: UsageTotals | undefined, t: UsageThresholds): WarnLevel {
  if (t.costWarnAmount == null) return "none";
  if (!totals || !totals.currency) return "none";
  return totals.cost >= t.costWarnAmount ? "warn" : "none";
}

// ------------------------------------------------------------------ states --

/**
 * The lifecycle phase of the status-bar usage summary for a run:
 *  - "loading"  — no report has arrived yet (snapshot not applied).
 *  - "no-run"   — no run is attached to the selected PRD; nothing to show.
 *  - "waiting"  — a run exists but has attributed no usage yet.
 *  - "ready"    — usage has been attributed and can be rendered.
 */
export type UsagePhase = "loading" | "no-run" | "waiting" | "ready";

export function usagePhase(
  usage: UsageReport | null,
  hasRun: boolean,
  sessionTotals: UsageTotals | undefined,
): UsagePhase {
  if (usage == null) return "loading";
  if (!hasRun) return "no-run";
  if (!sessionTotals || sessionTotals.records === 0) return "waiting";
  return "ready";
}

/**
 * True when a cost figure is meaningful. The currency is empty until a provider
 * reports a cost, so a partially-supported provider (tokens but no cost) reads as
 * cost-unavailable rather than a misleading $0.00 (US-005 / AC "partial data").
 */
export function hasCost(totals: UsageTotals | UsageGroup | undefined): boolean {
  return !!totals && !!totals.currency;
}

/**
 * True when a scope's usage spans more than one group — mixed providers, models
 * or currencies — and so must be shown group by group rather than summed into a
 * single figure.
 */
export function isMixedProvider(totals: UsageTotals | undefined): boolean {
  return (totals?.groups?.length ?? 0) > 1;
}

/** True when a scope reports some token classes but leaves others unreported. */
export function isPartial(g: UsageGroup): boolean {
  const classes = [
    g.inputTokens,
    g.outputTokens,
    g.reasoningTokens,
    g.cacheReadTokens,
    g.cacheWriteTokens,
  ];
  const some = classes.some((n) => n > 0);
  const missing = classes.some((n) => !n) || !g.hasCost;
  return some && missing;
}

/** True when a project has no retained session history to browse (AC "empty"). */
export function historyIsEmpty(sessions: SessionUsage[] | undefined): boolean {
  return (sessions?.length ?? 0) === 0;
}

/**
 * The message to surface when persisted usage history could not be loaded. The
 * Go side ships an actionable hint on the EvUsageError event; fall back to a
 * generic line so the state is never silent (AC "persistence-error").
 */
export function usageErrorMessage(hint: string | undefined): string {
  const trimmed = (hint ?? "").trim();
  if (trimmed) return trimmed;
  return "Usage history could not be loaded; live usage is unaffected.";
}
