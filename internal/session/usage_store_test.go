package session

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/dripcoder/loop/internal/fakeagent"
)

// usageFilePath is where a project's history lives on disk.
func usageFilePath(root string) string {
	return filepath.Join(root, ".chief", usageFile)
}

// Round-tripping a set of records through the store must return exactly what was
// written, so a reopened project sees the same history.
func TestUsageStoreSaveLoadRoundTrips(t *testing.T) {
	root := t.TempDir()
	store := newUsageStore(root)

	records := []UsageRecord{
		usageRecord("run_1/US-001#1:0", "run_1", "US-001", 1, 100, 10, 0.01),
		usageRecord("run_1/US-002#1:0", "run_1", "US-002", 1, 200, 20, 0.02),
	}
	if err := store.save(records); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := store.load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != len(records) {
		t.Fatalf("loaded %d records, want %d", len(got), len(records))
	}
	for i := range records {
		if got[i].Key != records[i].Key || got[i].RunID != records[i].RunID ||
			valueOrZero(got[i].InputTokens) != valueOrZero(records[i].InputTokens) {
			t.Errorf("record %d = %+v, want %+v", i, got[i], records[i])
		}
	}
}

// A missing history file is the first-run case: an empty history, not an error.
func TestUsageStoreMissingFileIsEmpty(t *testing.T) {
	store := newUsageStore(t.TempDir())
	got, err := store.load()
	if err != nil {
		t.Fatalf("a missing usage file must load as empty, got error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("loaded %d records from a missing file, want 0", len(got))
	}
}

// Garbage on disk must be reported, not silently misread into a bogus total.
func TestUsageStoreInvalidFileErrors(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".chief"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(usageFilePath(root), []byte("{ not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := newUsageStore(root).load(); err == nil {
		t.Error("invalid usage history should load with an error")
	}
}

// A file from an incompatible version must be rejected rather than parsed as if
// its shape still matched.
func TestUsageStoreUnsupportedVersionErrors(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".chief"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(usageFilePath(root), []byte(`{"version":999,"records":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := newUsageStore(root).load(); err == nil {
		t.Error("an unsupported version should load with an error")
	}
}

// save must create .chief/ when it does not exist yet, so the first usage of a
// brand-new project is not lost.
func TestUsageStoreCreatesChiefDir(t *testing.T) {
	root := t.TempDir()
	store := newUsageStore(root)
	if err := store.save([]UsageRecord{usageRecord("k", "run_1", "US-001", 1, 1, 1, 0)}); err != nil {
		t.Fatalf("save into a project without .chief/: %v", err)
	}
	if _, err := os.Stat(usageFilePath(root)); err != nil {
		t.Errorf("history file was not written: %v", err)
	}
}

// The ledger persists every add and restores it on open, keeping key counters so
// a reopened ledger assigns fresh keys past the loaded ones.
func TestUsageLedgerPersistsAndRestores(t *testing.T) {
	root := t.TempDir()

	first := newUsageLedger()
	first.open(newUsageStore(root), nil, nil)
	scope := attemptScopeKey("run_1", "US-001", 1)
	first.add(usageRecord(first.nextKey(scope), "run_1", "US-001", 1, 100, 10, 0.01))

	// A second ledger opened against the same project restores the history.
	second := newUsageLedger()
	records, err := newUsageStore(root).load()
	if err != nil {
		t.Fatal(err)
	}
	second.open(newUsageStore(root), records, nil)

	if got := second.report().Project.InputTokens; got != 100 {
		t.Errorf("restored project total = %d, want 100", got)
	}
	// The restored counter must not hand back a key the loaded record already uses.
	if k := second.nextKey(scope); k == scope+":0" {
		t.Errorf("nextKey reissued the loaded key %q after restore", k)
	}
}

// Restart recovery through the full session: usage recorded by one session must
// be visible to a fresh session that reopens the same project, without a run.
func TestUsageSurvivesRestart(t *testing.T) {
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
	want := first.Usage().Project
	if want.InputTokens != 120 {
		t.Fatalf("first session total = %+v, want input 120", want)
	}

	// A fresh session (a "restart") reopening the same project restores the total
	// without starting a run.
	second := newTestSessionWith(t, fakeagent.New())
	if _, err := second.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if len(second.Runs()) != 0 {
		t.Error("reopening a project must not start a run")
	}
	if got := second.Usage().Project; got != want {
		t.Errorf("restored total = %+v, want the persisted %+v", got, want)
	}
	if got := second.Snapshot().Usage.Project; got != want {
		t.Errorf("snapshot total = %+v, want the persisted %+v", got, want)
	}
}

// Opening a different project replaces the visible general total with that
// project's own history.
func TestUsageProjectSwitchReplacesHistory(t *testing.T) {
	projectA, projectB := t.TempDir(), t.TempDir()
	if err := newUsageStore(projectA).save([]UsageRecord{
		usageRecord("run_1/US-001#1:0", "run_1", "US-001", 1, 100, 10, 0.01),
	}); err != nil {
		t.Fatal(err)
	}
	if err := newUsageStore(projectB).save([]UsageRecord{
		usageRecord("run_1/US-001#1:0", "run_1", "US-001", 1, 500, 50, 0.5),
	}); err != nil {
		t.Fatal(err)
	}

	s := newTestSession(t)
	ctx := context.Background()
	if _, err := s.OpenProject(ctx, projectA); err != nil {
		t.Fatal(err)
	}
	if got := s.Usage().Project.InputTokens; got != 100 {
		t.Fatalf("project A total = %d, want 100", got)
	}

	if _, err := s.OpenProject(ctx, projectB); err != nil {
		t.Fatal(err)
	}
	if got := s.Usage().Project.InputTokens; got != 500 {
		t.Errorf("after switching to project B total = %d, want 500 (A's 100 must be gone)", got)
	}
}

// Missing persisted data is treated as an empty history, and the project still
// opens with its PRDs and run controls intact.
func TestUsageMissingHistoryOpensEmpty(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "main", oneStoryPRD)

	s := newTestSession(t)
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if got := s.Usage().Project.Records; got != 0 {
		t.Errorf("missing history should open empty, got %d records", got)
	}
	if len(s.PRDs()) == 0 {
		t.Error("the PRD list must still be populated with no history file")
	}
}

// Invalid persisted data surfaces a visible, actionable error while leaving the
// PRD list and run controls usable.
func TestUsageInvalidHistorySurfacesErrorButKeepsProjectUsable(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "main", oneStoryPRD)
	if err := os.MkdirAll(filepath.Join(root, ".chief"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(usageFilePath(root), []byte("totally not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := newTestSessionWith(t, fakeagent.New(fakeagent.Behaviour{
		Text: "working", WriteFile: "o.txt", FileBody: "x", Commit: true, Done: true,
	}))
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatalf("invalid history must not fail OpenProject: %v", err)
	}

	// The error is visible on the stream.
	assertPublished(t, s, EvUsageError)

	// History is empty, but the PRD list is intact...
	if got := s.Usage().Project.Records; got != 0 {
		t.Errorf("invalid history should be treated as empty, got %d records", got)
	}
	if len(s.PRDs()) == 0 {
		t.Fatal("the PRD list must remain usable after an invalid history file")
	}

	// ...and the run controls still work.
	runID, err := s.Start(context.Background(), StartRequest{PRD: "main"})
	if err != nil {
		t.Fatalf("run controls must stay usable: %v", err)
	}
	if snap := waitFor(t, s, runID); snap.State != StateComplete {
		t.Errorf("state = %s, want complete", snap.State)
	}
}

// Concurrent usage updates must not lose records or corrupt the file: every add
// is serialized under the ledger lock, so the last write to disk is complete.
func TestUsageConcurrentUpdatesArePersistedConsistently(t *testing.T) {
	root := t.TempDir()
	ledger := newUsageLedger()
	ledger.open(newUsageStore(root), nil, nil)

	const goroutines, perGoroutine = 8, 25
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				scope := attemptScopeKey("run_1", "US-001", g+1)
				ledger.add(usageRecord(ledger.nextKey(scope), "run_1", "US-001", g+1, 10, 1, 0.001))
			}
		}(g)
	}
	wg.Wait()

	const wantRecords = goroutines * perGoroutine
	if got := ledger.report().Project.Records; got != wantRecords {
		t.Errorf("in-memory records = %d, want %d", got, wantRecords)
	}

	// The persisted file must hold every record too — no stale write clobbered it.
	persisted, err := newUsageStore(root).load()
	if err != nil {
		t.Fatalf("load after concurrent writes: %v", err)
	}
	if len(persisted) != wantRecords {
		t.Errorf("persisted %d records, want %d — a concurrent write was lost", len(persisted), wantRecords)
	}
	if got := ledger.report().Project.InputTokens; got != int64(wantRecords*10) {
		t.Errorf("input total = %d, want %d", got, wantRecords*10)
	}
}
