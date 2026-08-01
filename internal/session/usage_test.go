package session

import (
	"context"
	"reflect"
	"strconv"
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
	if !reflect.DeepEqual(s.Snapshot().Usage.Project, rep.Project) {
		t.Errorf("snapshot totals %+v differ from the report %+v",
			s.Snapshot().Usage.Project, rep.Project)
	}
	if latest := latestUsageReport(t, s); !reflect.DeepEqual(latest.Project, rep.Project) {
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

// A fresh snapshot must equal the totals obtained by applying every accepted
// usage event, and replaying those events before live delivery must never double
// count — the two properties that keep a reconnecting status bar correct.
func TestUsageSnapshotEqualsAppliedEventsAndReplayDoesNotDoubleCount(t *testing.T) {
	s, _, runID := startRun(t, oneStoryPRD,
		fakeagent.Behaviour{
			Text:     "try 1",
			Usage:    &fakeagent.Usage{InputTokens: 100, OutputTokens: 10, CostUSD: 0.01},
			ExitCode: 1, // crash after reporting usage, to emit a second event on retry
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

	// Every accepted usage record travels on the ordered event stream. Fresh
	// snapshot totals must equal the totals rebuilt from those events alone.
	records := acceptedUsageRecords(t, s)
	if len(records) != 2 {
		t.Fatalf("accepted usage events = %d, want 2 (the crash and its retry)", len(records))
	}
	applied := buildReport(records)
	fresh := s.Snapshot().Usage
	if !reflect.DeepEqual(applied.Project, fresh.Project) {
		t.Errorf("applying the events gives %+v, snapshot has %+v", applied.Project, fresh.Project)
	}
	if !reflect.DeepEqual(applied.Runs[runID], fresh.Runs[runID]) {
		t.Errorf("session total from events %+v differs from snapshot %+v",
			applied.Runs[runID], fresh.Runs[runID])
	}

	// Replay-then-live: re-delivering the very same events (a reconnect that
	// replays and then keeps receiving) must not inflate any aggregate.
	l := newUsageLedger()
	for _, rec := range records {
		l.add(rec)
	}
	for _, rec := range records { // the "live" re-delivery of already-seen keys
		l.add(rec)
	}
	if got := l.report().Project; !reflect.DeepEqual(got, fresh.Project) {
		t.Errorf("replay + live double counted: %+v, want %+v", got, fresh.Project)
	}
}

// acceptedUsageRecords returns every usage record the session accepted, read back
// from the ordered event stream in delivery order.
func acceptedUsageRecords(t *testing.T, s *Session) []UsageRecord {
	t.Helper()
	evs, complete := s.Replay(0)
	if !complete {
		t.Fatal("replay is incomplete; the retention ring rolled")
	}
	var records []UsageRecord
	for i := range evs {
		if evs[i].Kind == EvUsage && evs[i].Usage != nil {
			records = append(records, *evs[i].Usage)
		}
	}
	return records
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

// findGroup returns the group matching a provider/model/currency triple, or nil.
func findGroup(totals UsageTotals, provider, model, currency string) *UsageGroup {
	for i := range totals.Groups {
		g := &totals.Groups[i]
		if g.Provider == provider && g.Model == model && g.Currency == currency {
			return g
		}
	}
	return nil
}

// Usage from different providers, models or currencies must be grouped, never
// combined into one misleading figure. A scope's flat totals still sum across
// everything; its Groups keep each triple separate.
func TestUsageGroupsSplitMixedProvidersModelsAndCurrencies(t *testing.T) {
	l := newUsageLedger()
	// Two claude records on the same model+currency: one group, summed.
	l.add(UsageRecord{
		Key: "k1", RunID: "run_1", StoryID: "US-001", Attempt: 1,
		Provider: "claude", Model: "claude-opus-4", Currency: "USD",
		InputTokens: ptr[int64](100), Cost: ptr(0.01),
	})
	l.add(UsageRecord{
		Key: "k2", RunID: "run_1", StoryID: "US-001", Attempt: 1,
		Provider: "claude", Model: "claude-opus-4", Currency: "USD",
		InputTokens: ptr[int64](50), Cost: ptr(0.02), Estimated: true,
	})
	// A different model, and a different currency: each its own group.
	l.add(UsageRecord{
		Key: "k3", RunID: "run_1", StoryID: "US-001", Attempt: 1,
		Provider: "codex", Model: "gpt-5-codex", Currency: "USD",
		InputTokens: ptr[int64](200),
	})
	l.add(UsageRecord{
		Key: "k4", RunID: "run_1", StoryID: "US-001", Attempt: 1,
		Provider: "claude", Model: "claude-opus-4", Currency: "EUR",
		InputTokens: ptr[int64](30), Cost: ptr(0.05),
	})

	story := l.report().Stories[storyScopeKey("run_1", "US-001")]
	if len(story.Groups) != 3 {
		t.Fatalf("groups = %d, want 3 distinct (provider,model,currency) triples", len(story.Groups))
	}

	// The two same-triple claude/USD records fold into one group with a mixed cost
	// kind, since one cost was reported and one estimated.
	claude := findGroup(story, "claude", "claude-opus-4", "USD")
	if claude == nil || claude.Records != 2 || claude.InputTokens != 150 {
		t.Fatalf("claude/USD group = %+v, want 2 records / 150 input", claude)
	}
	if claude.CostKind != costKindMixed {
		t.Errorf("claude/USD cost kind = %q, want %q", claude.CostKind, costKindMixed)
	}

	// The codex group reported no cost at all — HasCost stays false so a real 0.00
	// is never inferred.
	codex := findGroup(story, "codex", "gpt-5-codex", "USD")
	if codex == nil || codex.HasCost {
		t.Errorf("codex group = %+v, want a cost-less group", codex)
	}

	// A reported-only group is labeled reported, not estimated or mixed.
	eur := findGroup(story, "claude", "claude-opus-4", "EUR")
	if eur == nil || eur.CostKind != costKindReported {
		t.Errorf("eur group = %+v, want cost kind %q", eur, costKindReported)
	}
}

// A group carries the context-window size and the peak single-payload footprint
// so context utilization can be shown when a provider reports a window.
func TestUsageGroupCarriesContextWindow(t *testing.T) {
	l := newUsageLedger()
	l.add(UsageRecord{
		Key: "k1", RunID: "run_1", StoryID: "US-001", Attempt: 1,
		Provider: "claude", Model: "claude-opus-4",
		InputTokens: ptr[int64](1000), ContextWindow: ptr[int64](200_000),
	})
	l.add(UsageRecord{
		Key: "k2", RunID: "run_1", StoryID: "US-001", Attempt: 1,
		Provider: "claude", Model: "claude-opus-4",
		InputTokens: ptr[int64](3000), ContextWindow: ptr[int64](200_000),
	})

	g := findGroup(l.report().Stories[storyScopeKey("run_1", "US-001")], "claude", "claude-opus-4", "")
	if g == nil || g.ContextWindow != 200_000 {
		t.Fatalf("group = %+v, want context window 200000", g)
	}
	if g.PeakContextTokens != 3000 {
		t.Errorf("peak context tokens = %d, want the largest single payload (3000)", g.PeakContextTokens)
	}
}

// histRecord builds a record with the attribution session history reads: run,
// PRD, story, attempt, provider/model and a timestamp.
func histRecord(runID, prd, storyID string, attempt int, at int64, provider, model string) UsageRecord {
	return UsageRecord{
		Key:   runID + "/" + storyID + "#" + strconv.Itoa(attempt) + ":" + strconv.FormatInt(at, 10),
		RunID: runID, PRD: prd, StoryID: storyID, Attempt: attempt, At: at,
		Provider: provider, Model: model,
		InputTokens: ptr[int64](100), OutputTokens: ptr[int64](10),
		Cost: ptr(0.01), Currency: "USD",
	}
}

// buildReport derives the session history from records: one entry per run, newest
// first, with the run's PRD, dominant provider/model, start/end times and stories.
func TestUsageSessionsListRunsNewestFirstWithStories(t *testing.T) {
	l := newUsageLedger()
	// run_1 ran first (earlier timestamps), across two stories with a retry on the
	// second; run_2 ran later.
	l.add(histRecord("run_1", "alpha", "US-001", 1, 1000, "claude", "claude-opus-4"))
	l.add(histRecord("run_1", "alpha", "US-002", 1, 1500, "claude", "claude-opus-4"))
	l.add(histRecord("run_1", "alpha", "US-002", 2, 1800, "claude", "claude-opus-4"))
	l.add(histRecord("run_2", "beta", "US-001", 1, 3000, "codex", "gpt-5"))

	sessions := l.report().Sessions
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d, want 2", len(sessions))
	}

	// Newest first: run_2 (started at 3000) before run_1 (started at 1000).
	if sessions[0].RunID != "run_2" || sessions[1].RunID != "run_1" {
		t.Fatalf("session order = [%s, %s], want [run_2, run_1]", sessions[0].RunID, sessions[1].RunID)
	}

	run1 := sessions[1]
	if run1.PRD != "alpha" || run1.Provider != "claude" || run1.Model != "claude-opus-4" {
		t.Errorf("run_1 summary = %+v, want prd alpha / claude / claude-opus-4", run1)
	}
	if run1.StartedAt != 1000 || run1.EndedAt != 1800 {
		t.Errorf("run_1 times = start %d end %d, want start 1000 end 1800", run1.StartedAt, run1.EndedAt)
	}
	if run1.Totals.InputTokens != 300 {
		t.Errorf("run_1 total input = %d, want 300 across three records", run1.Totals.InputTokens)
	}
	if len(run1.Stories) != 2 {
		t.Fatalf("run_1 stories = %d, want 2", len(run1.Stories))
	}
	// Stories in first-seen order; US-002 had two distinct attempts.
	if run1.Stories[0].StoryID != "US-001" || run1.Stories[0].Attempts != 1 {
		t.Errorf("first story = %+v, want US-001 with 1 attempt", run1.Stories[0])
	}
	if run1.Stories[1].StoryID != "US-002" || run1.Stories[1].Attempts != 2 {
		t.Errorf("second story = %+v, want US-002 with 2 attempts", run1.Stories[1])
	}
	if run1.Stories[1].Totals.InputTokens != 200 {
		t.Errorf("US-002 total input = %d, want 200 across both attempts", run1.Stories[1].Totals.InputTokens)
	}
}

// An empty project has no sessions to browse.
func TestUsageSessionsEmptyWhenNoUsage(t *testing.T) {
	if got := newUsageLedger().report().Sessions; len(got) != 0 {
		t.Errorf("sessions = %d, want 0 for an empty project", len(got))
	}
}

// A recorded terminal state is stamped on the session and survives a reopen, so
// history shows completed/stopped/failed rather than reading as interrupted.
func TestUsageSessionTerminalStateIsRecordedAndPersisted(t *testing.T) {
	root := t.TempDir()

	first := newUsageLedger()
	first.open(newUsageStore(root), nil, nil, nil)
	first.add(histRecord("run_1", "alpha", "US-001", 1, 1000, "claude", "claude-opus-4"))
	first.noteRunTerminal("run_1", sessionStopped, 2000)
	// A non-terminal state is ignored.
	first.noteRunTerminal("run_1", sessionActive, 3000)

	if got := first.report().Sessions[0]; got.State != sessionStopped || got.EndedAt != 2000 {
		t.Fatalf("session = %+v, want state stopped / endedAt 2000", got)
	}

	// Reopen from disk: the terminal state is restored.
	records, states, err := newUsageStore(root).load()
	if err != nil {
		t.Fatal(err)
	}
	second := newUsageLedger()
	second.open(newUsageStore(root), records, states, nil)
	if got := second.report().Sessions[0]; got.State != sessionStopped {
		t.Errorf("restored session state = %q, want stopped", got.State)
	}
}

// Through the run engine, a completed run is browsable as a completed session
// with its story, and stays completed after a restart.
func TestUsageSessionHistoryThroughRunEngineAndRestart(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "main", oneStoryPRD)

	first := newTestSessionWith(t, fakeagent.New(fakeagent.Behaviour{
		Text: "working", WriteFile: "out.txt", FileBody: "x", Commit: true, Done: true,
		Usage: &fakeagent.Usage{InputTokens: 120, OutputTokens: 30, CostUSD: 0.05, Model: "claude"},
	}))
	if _, err := first.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	runID, err := first.Start(context.Background(), StartRequest{PRD: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if snap := waitFor(t, first, runID); snap.State != StateComplete {
		t.Fatalf("state = %s, want complete", snap.State)
	}

	sessions := first.Usage().Sessions
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	if sessions[0].State != sessionCompleted {
		t.Errorf("session state = %q, want completed", sessions[0].State)
	}
	if len(sessions[0].Stories) == 0 || sessions[0].Stories[0].StoryID != "US-001" {
		t.Errorf("session stories = %+v, want US-001", sessions[0].Stories)
	}

	// A restart reopening the same project still shows the session as completed.
	second := newTestSessionWith(t, fakeagent.New())
	if _, err := second.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	restored := second.Usage().Sessions
	if len(restored) != 1 || restored[0].State != sessionCompleted {
		t.Errorf("restored sessions = %+v, want one completed session", restored)
	}
}
