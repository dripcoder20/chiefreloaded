package session

import (
	"testing"

	chiefloop "github.com/dripcoder/loop/internal/chief/loop"
	"github.com/dripcoder/loop/internal/fakeagent"
)

// oneStoryPRD keeps the run to a single story so an attempt-level assertion is
// unambiguous.
const oneStoryPRD = `# Demo Project

## User Stories

### US-001: Only story
**Status:** todo
**Priority:** 1
**Description:** Do the thing.
- [ ] It works
`

func usageRecord(key, runID, storyID string, attempt int, in, out int64, cost float64) UsageRecord {
	return UsageRecord{
		Key:          key,
		RunID:        runID,
		StoryID:      storyID,
		Attempt:      attempt,
		InputTokens:  ptr(in),
		OutputTokens: ptr(out),
		Cost:         ptr(cost),
		Currency:     "USD",
	}
}

func approxEqual(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}

// A story total, a run (session) total and the project grand total are three
// distinct scopes; usage must roll up into each independently.
func TestUsageTotalsSplitByStoryRunAndProject(t *testing.T) {
	l := newUsageLedger()
	l.add(usageRecord("k1", "run_1", "US-001", 1, 100, 10, 0.01))
	l.add(usageRecord("k2", "run_1", "US-002", 1, 200, 20, 0.02))
	l.add(usageRecord("k3", "run_2", "US-001", 1, 50, 5, 0.005))

	rep := l.report()

	if rep.Project.InputTokens != 350 || rep.Project.OutputTokens != 35 {
		t.Errorf("project totals = %+v, want input 350 / output 35", rep.Project)
	}
	if !approxEqual(rep.Project.Cost, 0.035) {
		t.Errorf("project cost = %v, want 0.035", rep.Project.Cost)
	}

	if got := rep.Runs["run_1"]; got.InputTokens != 300 || got.OutputTokens != 30 {
		t.Errorf("run_1 session total = %+v, want input 300 / output 30", got)
	}
	if got := rep.Runs["run_2"]; got.InputTokens != 50 {
		t.Errorf("run_2 session total = %+v, want input 50 (distinct from the project total)", got)
	}

	story := rep.Stories[storyScopeKey("run_1", "US-001")]
	if story.InputTokens != 100 || story.TotalTokens != 110 {
		t.Errorf("story total = %+v, want input 100 / derived total 110", story)
	}
}

// Retried and failed attempts still consumed usage and must remain in the story
// and session totals, each attempt attributable on its own.
func TestUsageIncludesRetriesAndFailedAttempts(t *testing.T) {
	l := newUsageLedger()
	l.add(usageRecord("a1", "run_1", "US-001", 1, 100, 10, 0.01)) // failed attempt
	l.add(usageRecord("a2", "run_1", "US-001", 2, 100, 10, 0.01)) // failed retry
	l.add(usageRecord("a3", "run_1", "US-001", 3, 100, 10, 0.01)) // success

	rep := l.report()

	story := rep.Stories[storyScopeKey("run_1", "US-001")]
	if story.Records != 3 || story.InputTokens != 300 {
		t.Errorf("story total = %+v, want 3 records / input 300 across every attempt", story)
	}
	if got := rep.Attempts[attemptScopeKey("run_1", "US-001", 2)]; got.InputTokens != 100 {
		t.Errorf("attempt 2 total = %+v, want input 100 (a failed retry is still attributable)", got)
	}
	if got := rep.Runs["run_1"]; got.InputTokens != 300 {
		t.Errorf("session total = %+v, want input 300 including retries and failures", got)
	}
}

// Pause and resume keep the same run ID, so usage after a resume must keep
// accumulating into the same session rather than starting a new one.
func TestUsagePauseResumeAccumulatesSameSession(t *testing.T) {
	l := newUsageLedger()
	l.add(usageRecord("p1", "run_1", "US-001", 1, 100, 10, 0.01))
	before := l.report().Runs["run_1"]

	// After resume: same run, next story.
	l.add(usageRecord("p2", "run_1", "US-002", 2, 100, 10, 0.01))
	after := l.report().Runs["run_1"]

	if after.InputTokens <= before.InputTokens {
		t.Errorf("resume did not keep accumulating: before %d, after %d",
			before.InputTokens, after.InputTokens)
	}
	if after.InputTokens != 200 || after.Records != 2 {
		t.Errorf("resumed session total = %+v, want input 200 / 2 records", after)
	}
}

// Replaying the same usage record must not inflate any aggregate.
func TestUsageDuplicateDeliveryDoesNotDoubleCount(t *testing.T) {
	l := newUsageLedger()
	rec := usageRecord("dup", "run_1", "US-001", 1, 100, 10, 0.01)

	if !l.add(rec) {
		t.Fatal("first delivery should be counted")
	}
	if l.add(rec) {
		t.Error("a repeated delivery of the same key must be ignored")
	}

	rep := l.report()
	if rep.Project.Records != 1 || rep.Project.InputTokens != 100 {
		t.Errorf("duplicate inflated the totals: %+v", rep.Project)
	}
}

// Distinct usage payloads within one attempt get distinct keys, so two genuine
// payloads are both counted rather than collapsing.
func TestUsageDistinctPayloadsWithinAnAttemptAreKeptSeparate(t *testing.T) {
	l := newUsageLedger()
	scope := attemptScopeKey("run_1", "US-001", 1)
	k1 := l.nextKey(scope)
	k2 := l.nextKey(scope)
	if k1 == k2 {
		t.Fatalf("nextKey returned the same key twice: %q", k1)
	}
	l.add(usageRecord(k1, "run_1", "US-001", 1, 100, 10, 0.01))
	l.add(usageRecord(k2, "run_1", "US-001", 1, 100, 10, 0.01))

	if got := l.report().Attempts[scope]; got.Records != 2 || got.InputTokens != 200 {
		t.Errorf("attempt total = %+v, want 2 records / input 200", got)
	}
}

// No provider reports a single total token count, so the aggregate must derive
// one from the reported components, while a missing field contributes nothing.
func TestUsageDerivesTotalAndHandlesMissingFields(t *testing.T) {
	l := newUsageLedger()
	l.add(UsageRecord{Key: "k", RunID: "run_1", StoryID: "US-001", Attempt: 1, OutputTokens: ptr(int64(42))})

	rep := l.report()
	if rep.Project.InputTokens != 0 {
		t.Errorf("missing input token count should contribute 0, got %d", rep.Project.InputTokens)
	}
	if rep.Project.TotalTokens != 42 {
		t.Errorf("total should be derived from the reported components, got %d", rep.Project.TotalTokens)
	}

	reported := newUsageLedger()
	reported.add(UsageRecord{Key: "k", RunID: "r", StoryID: "s", Attempt: 1,
		InputTokens: ptr(int64(10)), TotalTokens: ptr(int64(999))})
	if got := reported.report().Project.TotalTokens; got != 999 {
		t.Errorf("a provider-reported total should be preferred, got %d", got)
	}
}

// buildUsageRecord stamps the attribution the criteria require and resolves the
// record's cost from the reported cost, falling back to the estimate.
func TestBuildUsageRecordStampsAttributionAndResolvesCost(t *testing.T) {
	u := &chiefloop.Usage{
		InputTokens:   ptr(int64(5)),
		Model:         "claude-x",
		EstimatedCost: ptr(0.02),
		Currency:      "USD",
	}
	attr := usageAttribution{runID: "run_1", prd: "main", storyID: "US-001", attempt: 2, provider: "fake", at: 123}

	rec := buildUsageRecord("key1", attr, u)
	if rec.Key != "key1" || rec.RunID != "run_1" || rec.PRD != "main" ||
		rec.StoryID != "US-001" || rec.Attempt != 2 || rec.Provider != "fake" || rec.At != 123 {
		t.Errorf("attribution not stamped: %+v", rec)
	}
	if rec.Model != "claude-x" {
		t.Errorf("model = %q, want claude-x", rec.Model)
	}
	if rec.Cost == nil || !approxEqual(*rec.Cost, 0.02) {
		t.Errorf("cost = %v, want the estimated 0.02 when no reported cost is present", rec.Cost)
	}

	u.ReportedCost = ptr(0.05)
	if got := buildUsageRecord("key2", attr, u); got.Cost == nil || !approxEqual(*got.Cost, 0.05) {
		t.Errorf("cost = %v, want the reported 0.05 to win", got.Cost)
	}
}

// End to end through the run engine: a usage payload the agent emits is
// attributed to the active story, attempt and run, and reaches the report and
// the snapshot. Re-reading is idempotent.
func TestUsageAttributedThroughTheRunEngine(t *testing.T) {
	s, _, runID := startRun(t, oneStoryPRD, fakeagent.Behaviour{
		Text: "working", WriteFile: "out.txt", FileBody: "x", Commit: true, Done: true,
		Usage: &fakeagent.Usage{InputTokens: 100, OutputTokens: 50, CostUSD: 0.01, Model: "claude"},
	})

	snap := waitFor(t, s, runID)
	if snap.State != StateComplete {
		t.Fatalf("state = %s, want complete (err %+v)", snap.State, snap.Error)
	}
	assertPublished(t, s, EvUsage)

	rep := s.Usage()
	if rep.Project.InputTokens != 100 || rep.Project.OutputTokens != 50 || rep.Project.TotalTokens != 150 {
		t.Errorf("project totals = %+v, want input 100 / output 50 / total 150", rep.Project)
	}
	if got := rep.Runs[runID]; got.InputTokens != 100 {
		t.Errorf("session total = %+v, want input 100", got)
	}
	if got := rep.Stories[storyScopeKey(runID, "US-001")]; got.InputTokens != 100 {
		t.Errorf("story total = %+v, want the usage attributed to the active story", got)
	}
	if got := rep.Attempts[attemptScopeKey(runID, "US-001", 1)]; got.InputTokens != 100 {
		t.Errorf("attempt total = %+v, want the usage attributed to attempt 1", got)
	}

	// A reconnecting consumer adopts the snapshot's absolute totals; replaying the
	// carrying event re-adopts the same numbers rather than adding to them.
	if s.Snapshot().Usage.Project != rep.Project {
		t.Errorf("snapshot totals %+v differ from the report %+v",
			s.Snapshot().Usage.Project, rep.Project)
	}
	if latest := latestUsageReport(t, s); latest.Project != rep.Project {
		t.Errorf("the usage event's report %+v is not the absolute total %+v",
			latest.Project, rep.Project)
	}
}

// A crash forces an internal retry; the failed attempt's usage must still be
// counted alongside the successful retry's.
func TestUsageAcrossRetriesCountsEveryAttempt(t *testing.T) {
	s, _, runID := startRun(t, oneStoryPRD,
		fakeagent.Behaviour{
			Text:  "try 1",
			Usage: &fakeagent.Usage{InputTokens: 100, OutputTokens: 10, CostUSD: 0.01},
			// Crash after reporting usage, to drive the retry path.
			ExitCode: 1,
		},
		fakeagent.Behaviour{
			Text: "try 2", WriteFile: "o.txt", FileBody: "x", Commit: true, Done: true,
			Usage: &fakeagent.Usage{InputTokens: 200, OutputTokens: 20, CostUSD: 0.02},
		},
	)

	snap := waitFor(t, s, runID)
	if snap.State != StateComplete {
		t.Fatalf("state = %s, want complete (err %+v)", snap.State, snap.Error)
	}

	rep := s.Usage()
	if rep.Project.InputTokens != 300 || rep.Project.OutputTokens != 30 {
		t.Errorf("project totals = %+v, want input 300 / output 30 (the failed attempt must still count)", rep.Project)
	}
	if got := rep.Runs[runID]; got.InputTokens != 300 {
		t.Errorf("session total = %+v, want input 300 including the retry", got)
	}
	if got := rep.Stories[storyScopeKey(runID, "US-001")]; got.Records != 2 {
		t.Errorf("story records = %d, want 2 across the crash and its retry", got.Records)
	}
}

// latestUsageReport returns the report carried on the most recent usage event.
func latestUsageReport(t *testing.T, s *Session) UsageReport {
	t.Helper()
	evs, _ := s.Replay(0)
	var latest *UsageReport
	for i := range evs {
		if evs[i].Kind == EvUsage && evs[i].UsageReport != nil {
			latest = evs[i].UsageReport
		}
	}
	if latest == nil {
		t.Fatal("no usage event carried a report")
	}
	return *latest
}
