import { describe, expect, it } from "vitest";
import { formatDuration, formatTokens, summarise } from "./summary";
import type { SessionUsage, UsageReport, UsageTotals } from "./usage";
import type { PRDDetail, RunSnapshot } from "../platform";

function totals(over: Partial<UsageTotals> = {}): UsageTotals {
  return {
    records: 1,
    inputTokens: 0,
    outputTokens: 0,
    reasoningTokens: 0,
    cacheReadTokens: 0,
    cacheWriteTokens: 0,
    totalTokens: 0,
    cost: 0,
    currency: "USD",
    ...over,
  };
}

function detail(over: Partial<PRDDetail> = {}): PRDDetail {
  return {
    name: "checkout",
    stories: [
      { id: "US-001", title: "Cart", status: "done" },
      { id: "US-002", title: "Pay", status: "todo" },
    ],
    ...over,
  } as PRDDetail;
}

function report(sessions: SessionUsage[]): UsageReport {
  return { project: totals(), runs: {}, stories: {}, attempts: {}, sessions };
}

describe("summarising a PRD", () => {
  it("reports every story, run or not", () => {
    const summary = summarise(detail(), report([]), []);

    expect(summary.stories.map((s) => s.id)).toEqual(["US-001", "US-002"]);
    expect(summary.counts).toMatchObject({ total: 2, done: 1, todo: 1 });
    // Nothing has run, which is different from having run and produced nothing.
    expect(summary.hasHistory).toBe(false);
  });

  it("ignores usage belonging to another PRD", () => {
    const sessions: SessionUsage[] = [
      { runId: "r1", prd: "billing", startedAt: 1, totals: totals({ totalTokens: 900 }) },
    ];

    const summary = summarise(detail(), report(sessions), []);

    expect(summary.sessions).toHaveLength(0);
    expect(summary.totals).toBeUndefined();
  });

  // A story retried in a later run has usage in both, and what the summary is
  // asked is what the story cost — not what it cost that time.
  it("folds a story's usage across every run of the PRD", () => {
    const sessions: SessionUsage[] = [
      {
        runId: "r2",
        prd: "checkout",
        startedAt: 5_000,
        endedAt: 9_000,
        totals: totals({ totalTokens: 300 }),
        stories: [
          {
            storyId: "US-001",
            attempts: 1,
            totals: totals({ totalTokens: 300, cost: 0.2 }),
            startedAt: 5_000,
            endedAt: 9_000,
          },
        ],
      },
      {
        runId: "r1",
        prd: "checkout",
        startedAt: 1_000,
        endedAt: 3_000,
        totals: totals({ totalTokens: 100 }),
        stories: [
          {
            storyId: "US-001",
            attempts: 2,
            totals: totals({ totalTokens: 100, cost: 0.1 }),
            startedAt: 1_000,
            endedAt: 3_000,
          },
        ],
      },
    ];

    const summary = summarise(detail(), report(sessions), []);
    const first = summary.stories[0];

    expect(first.attempts).toBe(3);
    expect(first.totalTokens).toBe(400);
    expect(first.cost).toBeCloseTo(0.3);
    // Each run's own span, summed — not the gap between the two runs.
    expect(summary.activeMs).toBe(4_000 + 2_000);
    // Wall clock spans everything, including the time between runs.
    expect(summary.elapsedMs).toBe(8_000);
    expect(summary.hasHistory).toBe(true);
  });

  it("names the model that did most of the work", () => {
    const sessions: SessionUsage[] = [
      {
        runId: "r1",
        prd: "checkout",
        startedAt: 1,
        totals: totals(),
        stories: [
          {
            storyId: "US-001",
            attempts: 1,
            totals: totals({
              totalTokens: 300,
              groups: [
                { provider: "claude", model: "haiku", totalTokens: 100 },
                { provider: "claude", model: "opus", totalTokens: 200 },
              ] as never,
            }),
          },
        ],
      },
    ];

    expect(summarise(detail(), report(sessions), []).stories[0].model).toBe("opus");
  });

  it("carries the branch and pull request through from the PRD", () => {
    const withGit = detail({
      stories: [
        {
          id: "US-001",
          title: "Cart",
          status: "done",
          branch: "loop/us-001-cart",
          pr: { number: 12, url: "https://example.test/12", state: "OPEN", draft: true },
        },
      ],
    } as Partial<PRDDetail>);

    const row = summarise(withGit, report([]), []).stories[0];

    expect(row.branch).toBe("loop/us-001-cart");
    expect(row.pr?.number).toBe(12);
  });
});

/**
 * Failures come from two places on purpose: a live run carries its error before
 * anything is written down, and a finished one leaves only that it ended badly.
 */
describe("failures", () => {
  it("reports the error of a run still in the session", () => {
    const runs = [
      {
        id: "r1",
        prd: "checkout",
        storyId: "US-002",
        error: { message: "agent exited 1", hint: "check the log" },
      },
    ] as unknown as RunSnapshot[];

    const failures = summarise(detail(), report([]), runs).failures;

    expect(failures).toHaveLength(1);
    expect(failures[0]).toMatchObject({ storyId: "US-002", source: "live" });
  });

  it("reports a recorded bad outcome once the run is gone", () => {
    const sessions: SessionUsage[] = [
      { runId: "r1", prd: "checkout", startedAt: 1, state: "failed", totals: totals() },
    ];

    const failures = summarise(detail(), report(sessions), []).failures;

    expect(failures).toHaveLength(1);
    expect(failures[0].source).toBe("recorded");
  });

  // The live error is the better description, so a run present in both is not
  // reported twice.
  it("does not double-report a run that is both live and recorded", () => {
    const sessions: SessionUsage[] = [
      { runId: "r1", prd: "checkout", startedAt: 1, state: "failed", totals: totals() },
    ];
    const runs = [
      { id: "r1", prd: "checkout", error: { message: "agent exited 1" } },
    ] as unknown as RunSnapshot[];

    const failures = summarise(detail(), report(sessions), runs).failures;

    expect(failures).toHaveLength(1);
    expect(failures[0].source).toBe("live");
  });

  it("treats a completed run as no failure", () => {
    const sessions: SessionUsage[] = [
      { runId: "r1", prd: "checkout", startedAt: 1, state: "completed", totals: totals() },
    ];

    expect(summarise(detail(), report(sessions), []).failures).toHaveLength(0);
  });
});

describe("formatting", () => {
  it("never shows a bare millisecond count", () => {
    expect(formatDuration(0)).toBe("—");
    expect(formatDuration(4_000)).toBe("4s");
    expect(formatDuration(95_000)).toBe("1m 35s");
    expect(formatDuration(3_780_000)).toBe("1h 3m");
  });

  it("abbreviates token counts once they stop being readable", () => {
    expect(formatTokens(0)).toBe("—");
    expect(formatTokens(9_999)).toBe("9,999");
    expect(formatTokens(12_500)).toBe("12.5k");
    expect(formatTokens(2_400_000)).toBe("2.40M");
  });
});
