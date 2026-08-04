//go:build e2e

// These tests exercise the usage pipeline end to end: a scripted agent that
// emits Claude-shaped usage flows through the real parser, attribution and
// aggregation, is persisted to .chief/usage.json, and is read back through the
// headless `loopctl usage` command — the same read model the GUI status bar and
// usage panel render from.
package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// usageAgent behaves like fakeAgent but also emits a Claude `result` line
// carrying token usage and a reported cost, and it makes US-002 fail its first
// attempt (no commit, no completion signal) so the run retries it — giving the
// story two attempts and letting the tests assert per-attempt attribution.
//
// A cross-process counter file (in stateDir, kept out of the git repo so it is
// never committed) records how many times each story has been invoked.
func usageAgent(t *testing.T, dir, stateDir string) string {
	t.Helper()

	path := filepath.Join(dir, "fake-claude-usage")
	script := `#!/bin/sh
prompt="$*"
id=$(printf '%s' "$prompt" | sed -n 's/.*"id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
title=$(printf '%s' "$prompt" | sed -n 's/.*"title"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)

count_file="` + stateDir + `/count-$id"
n=$(cat "$count_file" 2>/dev/null || echo 0)
n=$((n + 1))
printf '%s' "$n" > "$count_file"

printf '{"type":"system","subtype":"init"}\n'
printf '{"type":"assistant","message":{"content":[{"type":"text","text":"Working on %s."}]}}\n' "$id"

# US-002 does nothing on its first attempt, forcing a run-level retry. Every
# attempt — including the failed one — still reports usage.
if [ "$id" != "US-002" ] || [ "$n" -ge 2 ]; then
  printf 'work for %s\n' "$id" > "file-$id.txt"
  git add -A >/dev/null 2>&1
  git commit -q -m "feat: $id - $title" >/dev/null 2>&1
  printf '{"type":"assistant","message":{"content":[{"type":"text","text":"Done. <chief-done/>"}]}}\n'
fi

printf '{"type":"result","model":"claude-sonnet-4-6","total_cost_usd":0.01,"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":20,"cache_creation_input_tokens":10}}\n'
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// readUsage runs `loopctl usage -json` and decodes the report.
func readUsage(t *testing.T, root string) usageReport {
	t.Helper()
	out, err := runCtl(t, root, "usage", "-json")
	if err != nil {
		t.Fatalf("loopctl usage: %v\n%s", err, out)
	}
	var report usageReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("decoding usage report: %v\n%s", err, out)
	}
	return report
}

// The subset of the read model these tests assert on. Mirrors the Go json tags.
type usageTotals struct {
	Records          int     `json:"records"`
	InputTokens      int64   `json:"inputTokens"`
	OutputTokens     int64   `json:"outputTokens"`
	CacheReadTokens  int64   `json:"cacheReadTokens"`
	CacheWriteTokens int64   `json:"cacheWriteTokens"`
	TotalTokens      int64   `json:"totalTokens"`
	Cost             float64 `json:"cost"`
	Currency         string  `json:"currency"`
}

type usageReport struct {
	Project  usageTotals            `json:"project"`
	Runs     map[string]usageTotals `json:"runs"`
	Stories  map[string]usageTotals `json:"stories"`
	Attempts map[string]usageTotals `json:"attempts"`
}

// runUsageProject wires the usage agent into a fresh project and runs it. The
// shared runCtl builds a fresh agent per invocation, so this test overrides the
// environment directly to keep one agent + one state dir for the whole run.
func runUsageProject(t *testing.T, root, agent string) {
	t.Helper()
	t.Setenv("CHIEF_AGENT", "claude")
	t.Setenv("CHIEF_AGENT_PATH", agent)
	out, err := runCtl(t, root, "run", "main")
	if err != nil {
		t.Fatalf("loopctl run: %v\n%s", err, out)
	}
}

func TestUsageReportsLiveTotalsAcrossStoriesAndAttempts(t *testing.T) {
	root := newProject(t)
	stateDir := t.TempDir()
	agent := usageAgent(t, t.TempDir(), stateDir)

	runUsageProject(t, root, agent)
	report := readUsage(t, root)

	// Four attempts total: US-001, US-002 (failed) , US-002 (retry), US-003.
	if report.Project.Records != 4 {
		t.Fatalf("expected 4 usage records project-wide, got %d\n%+v", report.Project.Records, report.Project)
	}

	// Each attempt reports the same fixed usage, so the grand total is 4×.
	assertTotals(t, "general/project", report.Project, usageTotals{
		Records: 4, InputTokens: 400, OutputTokens: 200,
		CacheReadTokens: 80, CacheWriteTokens: 40,
		TotalTokens: 720, Cost: 0.04, Currency: "USD",
	})

	// One run, so the session total equals the project total.
	if len(report.Runs) != 1 {
		t.Fatalf("expected exactly one run, got %d: %+v", len(report.Runs), report.Runs)
	}
	runID := onlyRunID(t, report.Runs)
	assertTotals(t, "session/run", report.Runs[runID], report.Project)

	// Story scope: US-002 was attempted twice, the others once.
	assertTotals(t, "story US-001", report.Stories[runID+"/US-001"], usageTotals{
		Records: 1, InputTokens: 100, OutputTokens: 50, CacheReadTokens: 20,
		CacheWriteTokens: 10, TotalTokens: 180, Cost: 0.01, Currency: "USD",
	})
	assertTotals(t, "story US-002 (retried)", report.Stories[runID+"/US-002"], usageTotals{
		Records: 2, InputTokens: 200, OutputTokens: 100, CacheReadTokens: 40,
		CacheWriteTokens: 20, TotalTokens: 360, Cost: 0.02, Currency: "USD",
	})

	// Attempt scope: US-002's two attempts are recorded under separate keys,
	// and every attempt across the run is accounted for individually.
	if n := countAttempts(report.Attempts, runID+"/US-002#"); n != 2 {
		t.Errorf("expected 2 recorded attempts for US-002, got %d: %+v", n, report.Attempts)
	}
	if len(report.Attempts) != 4 {
		t.Errorf("expected 4 per-attempt scopes total, got %d: %+v", len(report.Attempts), report.Attempts)
	}
}

func countAttempts(attempts map[string]usageTotals, prefix string) int {
	n := 0
	for key := range attempts {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			n++
		}
	}
	return n
}

func TestUsageHistoryReloadsFromDiskAfterRestart(t *testing.T) {
	root := newProject(t)
	stateDir := t.TempDir()
	agent := usageAgent(t, t.TempDir(), stateDir)

	runUsageProject(t, root, agent)
	before := readUsage(t, root)

	// The store must be on disk for a cold start to find it.
	if _, err := os.Stat(filepath.Join(root, ".chief", "usage.json")); err != nil {
		t.Fatalf(".chief/usage.json was not persisted: %v", err)
	}

	// A brand-new loopctl process (fresh session, cold ledger) must reload the
	// same completed history from disk — no run, no live events.
	after := readUsage(t, root)

	assertTotals(t, "project after restart", after.Project, before.Project)
	if len(after.Runs) != len(before.Runs) {
		t.Fatalf("run count changed across restart: %d → %d", len(before.Runs), len(after.Runs))
	}
	for id, totals := range before.Runs {
		assertTotals(t, "run "+id+" after restart", after.Runs[id], totals)
	}
	for id, totals := range before.Stories {
		assertTotals(t, "story "+id+" after restart", after.Stories[id], totals)
	}
}

func assertTotals(t *testing.T, scope string, got, want usageTotals) {
	t.Helper()
	if got != want {
		t.Errorf("%s totals mismatch:\n got  %+v\n want %+v", scope, got, want)
	}
}

func onlyRunID(t *testing.T, runs map[string]usageTotals) string {
	t.Helper()
	for id := range runs {
		return id
	}
	t.Fatal("no runs in report")
	return ""
}
