/**
 * What happened while a PRD was implemented, assembled from what survives.
 *
 * Nothing here is a new record. The log's event ring drops old entries and is
 * emptied by a restart, so a summary built from it would quietly become a
 * summary of the recent past. Everything below comes from sources that outlive
 * the process:
 *
 *   - prd.md — each story's outcome, through PRDDetail
 *   - .chief/usage.json — attempts, token spend, cost, model, and the period a
 *     story spent tokens in
 *   - .chief/prds/<name>/loop.json — the branch each story used and its pull
 *     request
 *
 * Live run state is folded in on top where it exists, which is what makes the
 * failure of the run you are watching visible before it has been recorded.
 */

import type { PRDDetail, PRRef, RunSnapshot, StorySnap } from "../platform";
import type { SessionUsage, StoryUsage, UsageGroup, UsageReport, UsageTotals } from "./usage";
import { hasCost } from "./usage";

/** One story's line in the summary. */
export type StoryRow = {
  id: string;
  title: string;
  status: StorySnap["status"];
  branch?: string;
  pr?: PRRef;
  /** Distinct attempts that spent tokens. 0 means the story has never run. */
  attempts: number;
  /** Token span in milliseconds; 0 when the story reported usage once or never. */
  spanMs: number;
  totalTokens: number;
  cost?: number;
  provider?: string;
  model?: string;
};

/** The whole picture for one PRD. */
export type PRDSummaryReport = {
  stories: StoryRow[];
  counts: { total: number; done: number; inProgress: number; blocked: number; todo: number };
  /** Runs of this PRD, newest first. */
  sessions: SessionUsage[];
  totals?: UsageTotals;
  /** Summed token spans across every story. Not wall clock — see StoryUsage. */
  activeMs: number;
  /** Wall clock from the first run's start to the last one's end, 0 if unknown. */
  elapsedMs: number;
  /** Failures worth reading: a run that ended badly, or is in error now. */
  failures: Failure[];
  /** True once any story has run, which is what separates "nothing yet" from "nothing to report". */
  hasHistory: boolean;
};

export type Failure = {
  runId: string;
  storyId?: string;
  message: string;
  hint?: string;
  /** "live" for a run still in the session; "recorded" for a past outcome. */
  source: "live" | "recorded";
};

/** Session states that mean the run did not finish its work. */
const BAD_STATES = new Set(["failed", "stopped", "interrupted"]);

export function summarise(
  detail: PRDDetail | null,
  usage: UsageReport | null,
  runs: RunSnapshot[],
): PRDSummaryReport {
  const stories = detail?.stories ?? [];
  const sessions = (usage?.sessions ?? []).filter((s) => s.prd === detail?.name);
  const byStory = foldStories(sessions);

  return {
    stories: stories.map((story) => toRow(story, byStory.get(story.id))),
    counts: countStatuses(stories),
    sessions,
    totals: sumSessions(sessions),
    activeMs: [...byStory.values()].reduce((ms, story) => ms + story.spanMs, 0),
    elapsedMs: elapsedOf(sessions),
    failures: failuresOf(sessions, runs, detail?.name),
    hasHistory: byStory.size > 0,
  };
}

/**
 * One entry per story across every run of the PRD.
 *
 * A story retried in a later run has usage in both, and the question the summary
 * answers is "what did this story cost", not "what did it cost that time".
 */
type Folded = { attempts: number; totals: UsageTotals; spanMs: number };

function foldStories(sessions: SessionUsage[]): Map<string, Folded> {
  const merged = new Map<string, Folded>();
  for (const session of sessions) {
    for (const story of session.stories ?? []) {
      const existing = merged.get(story.storyId);
      merged.set(story.storyId, existing ? mergeStory(existing, story) : foldOne(story));
    }
  }
  return merged;
}

function foldOne(story: StoryUsage): Folded {
  return { attempts: story.attempts, totals: story.totals, spanMs: spanOf(story) };
}

/**
 * Spans are added, never re-derived from the earliest start and the latest end.
 *
 * A story attempted in two runs an hour apart was not working for an hour, and
 * taking min-to-max would report exactly that — the gap between the runs counted
 * as time spent. Each run's own span is the only interval anything was happening.
 */
function mergeStory(a: Folded, story: StoryUsage): Folded {
  return {
    attempts: a.attempts + story.attempts,
    totals: mergeTotals(a.totals, story.totals),
    spanMs: a.spanMs + spanOf(story),
  };
}

function mergeTotals(a: UsageTotals, b: UsageTotals): UsageTotals {
  return {
    records: a.records + b.records,
    inputTokens: a.inputTokens + b.inputTokens,
    outputTokens: a.outputTokens + b.outputTokens,
    reasoningTokens: a.reasoningTokens + b.reasoningTokens,
    cacheReadTokens: a.cacheReadTokens + b.cacheReadTokens,
    cacheWriteTokens: a.cacheWriteTokens + b.cacheWriteTokens,
    totalTokens: a.totalTokens + b.totalTokens,
    cost: a.cost + b.cost,
    currency: a.currency || b.currency,
    // Groups carry the provider and model, and concatenating them keeps a story
    // run by two different agents honest about both.
    groups: [...(a.groups ?? []), ...(b.groups ?? [])],
  };
}

function toRow(story: StorySnap, usage: Folded | undefined): StoryRow {
  const dominant = dominantGroup(usage?.totals);
  return {
    id: story.id,
    title: story.title,
    status: story.status,
    branch: story.branch || undefined,
    pr: story.pr ?? undefined,
    attempts: usage?.attempts ?? 0,
    spanMs: usage?.spanMs ?? 0,
    totalTokens: usage?.totals.totalTokens ?? 0,
    cost: hasCost(usage?.totals) ? usage?.totals.cost : undefined,
    provider: dominant?.provider,
    model: dominant?.model,
  };
}

/** The group that did most of the work, which is the one worth naming. */
function dominantGroup(totals: UsageTotals | undefined): UsageGroup | undefined {
  return (totals?.groups ?? []).reduce<UsageGroup | undefined>(
    (best, group) => (!best || group.totalTokens > best.totalTokens ? group : best),
    undefined,
  );
}

function spanOf(usage: StoryUsage | undefined): number {
  if (!usage?.startedAt || !usage.endedAt) return 0;
  return Math.max(0, usage.endedAt - usage.startedAt);
}

function countStatuses(stories: StorySnap[]) {
  const counts = { total: stories.length, done: 0, inProgress: 0, blocked: 0, todo: 0 };
  for (const s of stories) {
    if (s.status === "done") counts.done++;
    else if (s.status === "in-progress") counts.inProgress++;
    else if (s.status === "blocked") counts.blocked++;
    else counts.todo++;
  }
  return counts;
}

function sumSessions(sessions: SessionUsage[]): UsageTotals | undefined {
  if (sessions.length === 0) return undefined;
  return sessions.map((s) => s.totals).reduce(mergeTotals);
}

function elapsedOf(sessions: SessionUsage[]): number {
  if (sessions.length === 0) return 0;
  const starts = sessions.map((s) => s.startedAt).filter((n) => n > 0);
  const ends = sessions.map((s) => s.endedAt ?? 0).filter((n) => n > 0);
  if (starts.length === 0 || ends.length === 0) return 0;
  return Math.max(0, Math.max(...ends) - Math.min(...starts));
}

/**
 * Failures from two sources, deliberately.
 *
 * A live run carries its error before anything is written down, and a past run
 * only leaves the fact that it ended badly. Reporting only the first would make
 * a summary forget every failure on restart; only the second would make the
 * failure you are watching invisible until it was over.
 */
function failuresOf(
  sessions: SessionUsage[],
  runs: RunSnapshot[],
  prd: string | undefined,
): Failure[] {
  const failures: Failure[] = [];
  const live = new Set<string>();

  for (const run of runs) {
    if (run.prd !== prd || !run.error) continue;
    live.add(run.id);
    failures.push({
      runId: run.id,
      storyId: run.storyId || undefined,
      message: run.error.message,
      hint: run.error.hint || undefined,
      source: "live",
    });
  }

  for (const session of sessions) {
    if (live.has(session.runId) || !session.state || !BAD_STATES.has(session.state)) continue;
    failures.push({
      runId: session.runId,
      message: `This run ${session.state === "failed" ? "failed" : session.state}.`,
      source: "recorded",
    });
  }
  return failures;
}

/** A duration as the summary shows it: compact, and never a bare millisecond count. */
export function formatDuration(ms: number): string {
  if (ms <= 0) return "—";
  const seconds = Math.round(ms / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${seconds % 60}s`;
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

/** Tokens, abbreviated once they stop being readable in full. */
export function formatTokens(n: number): string {
  if (n <= 0) return "—";
  if (n < 10_000) return n.toLocaleString();
  if (n < 1_000_000) return `${(n / 1000).toFixed(1)}k`;
  return `${(n / 1_000_000).toFixed(2)}M`;
}
