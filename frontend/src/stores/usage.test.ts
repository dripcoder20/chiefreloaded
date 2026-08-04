import { describe, it, expect } from "vitest";
import {
  DEFAULT_THRESHOLDS,
  contextUtilization,
  worstContext,
  costLevel,
  usagePhase,
  hasCost,
  isMixedProvider,
  isPartial,
  historyIsEmpty,
  usageErrorMessage,
  type UsageGroup,
  type UsageTotals,
  type UsageReport,
  type SessionUsage,
} from "./usage";

// A fully-reported group: every token class and a reported cost.
function fullGroup(over: Partial<UsageGroup> = {}): UsageGroup {
  return {
    provider: "claude",
    model: "claude-sonnet-4-6",
    currency: "USD",
    records: 1,
    inputTokens: 1000,
    outputTokens: 400,
    reasoningTokens: 100,
    cacheReadTokens: 200,
    cacheWriteTokens: 50,
    totalTokens: 1750,
    cost: 0.12,
    hasCost: true,
    costKind: "reported",
    contextWindow: 200_000,
    peakContextTokens: 20_000,
    ...over,
  };
}

function totalsFrom(groups: UsageGroup[]): UsageTotals {
  const sum = (pick: (g: UsageGroup) => number) => groups.reduce((n, g) => n + pick(g), 0);
  return {
    records: sum((g) => g.records),
    inputTokens: sum((g) => g.inputTokens),
    outputTokens: sum((g) => g.outputTokens),
    reasoningTokens: sum((g) => g.reasoningTokens),
    cacheReadTokens: sum((g) => g.cacheReadTokens),
    cacheWriteTokens: sum((g) => g.cacheWriteTokens),
    totalTokens: sum((g) => g.totalTokens),
    cost: sum((g) => g.cost),
    currency: groups.find((g) => g.currency)?.currency ?? "",
    groups,
  };
}

const emptyReport: UsageReport = { project: totalsFrom([]), runs: {}, stories: {}, attempts: {} };

describe("usagePhase — loading and waiting", () => {
  it("is loading before any report has arrived", () => {
    expect(usagePhase(null, true, undefined)).toBe("loading");
  });

  it("is no-run when nothing is attached to the selected PRD", () => {
    expect(usagePhase(emptyReport, false, undefined)).toBe("no-run");
  });

  it("is waiting when a run exists but has attributed no usage yet", () => {
    expect(usagePhase(emptyReport, true, undefined)).toBe("waiting");
    const empty = totalsFrom([]);
    expect(usagePhase(emptyReport, true, empty)).toBe("waiting");
  });

  it("is ready once usage has been attributed", () => {
    const totals = totalsFrom([fullGroup()]);
    expect(usagePhase(emptyReport, true, totals)).toBe("ready");
  });
});

describe("partial-data and mixed-provider", () => {
  it("marks a provider that reports only some token classes and no cost as partial", () => {
    // codex: input/cache-read/output only, no reasoning, no cache-write, no cost.
    const codex = fullGroup({
      provider: "codex",
      currency: "",
      reasoningTokens: 0,
      cacheWriteTokens: 0,
      cost: 0,
      hasCost: false,
      costKind: undefined,
    });
    expect(isPartial(codex)).toBe(true);
    expect(hasCost(codex)).toBe(false);
  });

  it("does not mark a fully-reported group as partial", () => {
    expect(isPartial(fullGroup())).toBe(false);
    expect(hasCost(fullGroup())).toBe(true);
  });

  it("cost is unavailable, not $0.00, when a provider never reports one", () => {
    const noCost = totalsFrom([fullGroup({ currency: "", cost: 0, hasCost: false })]);
    expect(hasCost(noCost)).toBe(false);
  });

  it("flags a scope spanning more than one provider as mixed", () => {
    const mixed = totalsFrom([fullGroup(), fullGroup({ provider: "codex", currency: "" })]);
    expect(isMixedProvider(mixed)).toBe(true);
    const single = totalsFrom([fullGroup()]);
    expect(isMixedProvider(single)).toBe(false);
  });
});

describe("warning and critical states", () => {
  it("does not warn while utilization is under the warn threshold", () => {
    const totals = totalsFrom([fullGroup({ peakContextTokens: 20_000 })]); // 10%
    expect(worstContext(totals, DEFAULT_THRESHOLDS).level).toBe("none");
  });

  it("warns at the warn threshold", () => {
    const totals = totalsFrom([fullGroup({ peakContextTokens: 170_000 })]); // 85%
    expect(worstContext(totals, DEFAULT_THRESHOLDS).level).toBe("warn");
  });

  it("escalates to critical at the critical threshold", () => {
    const totals = totalsFrom([fullGroup({ peakContextTokens: 195_000 })]); // 97.5%
    expect(worstContext(totals, DEFAULT_THRESHOLDS).level).toBe("critical");
  });

  it("takes the worst level across a scope's groups", () => {
    const totals = totalsFrom([
      fullGroup({ peakContextTokens: 20_000 }), // 10%
      fullGroup({ provider: "codex", peakContextTokens: 196_000 }), // 98%
    ]);
    expect(worstContext(totals, DEFAULT_THRESHOLDS).level).toBe("critical");
  });

  it("never warns when the context window is unknown (AC6)", () => {
    const totals = totalsFrom([fullGroup({ contextWindow: 0, peakContextTokens: 999_999 })]);
    expect(contextUtilization(totals.groups![0])).toBeNull();
    expect(worstContext(totals, DEFAULT_THRESHOLDS).level).toBe("none");
  });

  it("warns on cost only over a configured amount with a known cost", () => {
    const t = { ...DEFAULT_THRESHOLDS, costWarnAmount: 0.1 };
    expect(costLevel(totalsFrom([fullGroup({ cost: 0.2 })]), t)).toBe("warn");
    expect(costLevel(totalsFrom([fullGroup({ cost: 0.05 })]), t)).toBe("none");
    // Unknown cost never breaches, even above the amount.
    const unknown = totalsFrom([fullGroup({ currency: "", cost: 0.2, hasCost: false })]);
    expect(costLevel(unknown, t)).toBe("none");
    // No configured amount → never warns.
    expect(costLevel(totalsFrom([fullGroup({ cost: 999 })]), DEFAULT_THRESHOLDS)).toBe("none");
  });
});

describe("empty-history state", () => {
  it("reports an empty project as having no history", () => {
    expect(historyIsEmpty(undefined)).toBe(true);
    expect(historyIsEmpty([])).toBe(true);
  });

  it("reports a project with retained sessions as non-empty", () => {
    const sessions: SessionUsage[] = [
      { runId: "run_1", startedAt: 1, totals: totalsFrom([fullGroup()]) },
    ];
    expect(historyIsEmpty(sessions)).toBe(false);
  });
});

describe("persistence-error state", () => {
  it("passes the Go hint straight through when present", () => {
    const msg = "cannot read .chief/usage.json: unexpected end of JSON";
    expect(usageErrorMessage(msg)).toBe(msg);
  });

  it("falls back to a generic message so the failure is never silent", () => {
    expect(usageErrorMessage(undefined)).toMatch(/usage history/i);
    expect(usageErrorMessage("   ")).toMatch(/usage history/i);
  });
});
