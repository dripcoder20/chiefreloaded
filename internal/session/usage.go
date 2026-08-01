package session

import (
	"log/slog"
	"strconv"
	"strings"
	"sync"

	chiefloop "github.com/dripcoder/loop/internal/chief/loop"
)

// This file is the session's usage read model: it turns the normalized,
// per-payload usage the agent loop reports (chiefloop.Usage) into records that
// are attributed to an attempt, a story, a run (session) and the project, and
// rolls those records up into totals.
//
// Two invariants make the totals trustworthy over a lossy transport:
//
//   - Every UsageReport is an ABSOLUTE cumulative total, never a delta. A
//     consumer that reconnects and re-reads the latest report (or replays the
//     event that carried it) re-adopts the same absolute numbers, so no
//     aggregate can be counted twice by the frontend.
//   - The ledger deduplicates by a stable record Key, so submitting the same
//     record twice on the server is a no-op. Between the two, replay and
//     reconnect are both idempotent.

// UsageRecord is one normalized usage payload, attributed to the work that
// consumed it. Token fields stay pointers so a nil (provider did not report the
// field) is distinct from a reported 0 — the same rule the loop's Usage model
// keeps. Cost is resolved to the reported cost when present, otherwise the
// locally estimated cost.
type UsageRecord struct {
	// Key deduplicates the record. It is stable for a given payload so a repeated
	// delivery collapses onto the same entry rather than inflating a total.
	Key string `json:"key"`

	RunID    string `json:"runId"`
	PRD      string `json:"prd,omitempty"`
	StoryID  string `json:"storyId,omitempty"`
	Attempt  int    `json:"attempt"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	At       int64  `json:"at"` // unix milliseconds

	InputTokens      *int64 `json:"inputTokens,omitempty"`
	OutputTokens     *int64 `json:"outputTokens,omitempty"`
	ReasoningTokens  *int64 `json:"reasoningTokens,omitempty"`
	CacheReadTokens  *int64 `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens *int64 `json:"cacheWriteTokens,omitempty"`
	TotalTokens      *int64 `json:"totalTokens,omitempty"`

	// ContextWindow is the model's context-window size in tokens, when the
	// provider reports it. Nil when unknown.
	ContextWindow *int64 `json:"contextWindow,omitempty"`

	Cost *float64 `json:"cost,omitempty"`
	// Estimated is true when Cost is a locally derived estimate rather than a
	// provider-reported figure. It is meaningless when Cost is nil.
	Estimated bool   `json:"estimated,omitempty"`
	Currency  string `json:"currency,omitempty"`
}

// totalTokens returns the record's token count: the provider-reported total when
// present, otherwise the sum of the reported component fields. No provider
// reports a single total today, so this is where the roll-up is derived.
func (rec UsageRecord) totalTokens() int64 {
	if rec.TotalTokens != nil {
		return *rec.TotalTokens
	}
	var total int64
	for _, p := range []*int64{
		rec.InputTokens, rec.OutputTokens, rec.ReasoningTokens,
		rec.CacheReadTokens, rec.CacheWriteTokens,
	} {
		total += valueOrZero(p)
	}
	return total
}

// Cost-kind labels. A group carries the kind of the costs it summed so the UI can
// label a total Reported or Estimated, or flag a Mixed total it must not conflate.
const (
	costKindReported  = "reported"
	costKindEstimated = "estimated"
	costKindMixed     = "mixed"
)

// UsageGroup is the subtotal for a single (provider, model, currency) triple
// within a scope. A scope with more than one group holds usage that must not be
// combined into one figure — different providers, models, or currencies — so the
// UI renders each group on its own rather than summing across them.
type UsageGroup struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Currency string `json:"currency,omitempty"`

	Records          int   `json:"records"`
	InputTokens      int64 `json:"inputTokens"`
	OutputTokens     int64 `json:"outputTokens"`
	ReasoningTokens  int64 `json:"reasoningTokens"`
	CacheReadTokens  int64 `json:"cacheReadTokens"`
	CacheWriteTokens int64 `json:"cacheWriteTokens"`
	TotalTokens      int64 `json:"totalTokens"`

	Cost float64 `json:"cost"`
	// HasCost is true once any record in the group carried a cost, so a real
	// reported 0.00 is distinguishable from "no cost reported".
	HasCost bool `json:"hasCost"`
	// CostKind is reported, estimated, or mixed once HasCost; empty otherwise.
	CostKind string `json:"costKind,omitempty"`

	// ContextWindow is the largest context-window size seen in the group, 0 when
	// no record reported one. PeakContextTokens is the biggest single-payload
	// token footprint; together they give context utilization when the window is
	// known.
	ContextWindow     int64 `json:"contextWindow,omitempty"`
	PeakContextTokens int64 `json:"peakContextTokens,omitempty"`
}

// UsageTotals is a summed set of usage records at some scope. The flat fields are
// a plain aggregate across every record (nil record fields contribute nothing);
// Groups breaks the same records down by provider, model and currency so mixed
// usage is never presented as one misleading number.
type UsageTotals struct {
	Records          int     `json:"records"`
	InputTokens      int64   `json:"inputTokens"`
	OutputTokens     int64   `json:"outputTokens"`
	ReasoningTokens  int64   `json:"reasoningTokens"`
	CacheReadTokens  int64   `json:"cacheReadTokens"`
	CacheWriteTokens int64   `json:"cacheWriteTokens"`
	TotalTokens      int64   `json:"totalTokens"`
	Cost             float64 `json:"cost"`
	Currency         string  `json:"currency,omitempty"`

	// Groups holds the per-(provider, model, currency) subtotals, in first-seen
	// order. A single group means the flat totals above are safe to show as one
	// figure; more than one means they are not.
	Groups []UsageGroup `json:"groups,omitempty"`
}

// addRecord folds one record into the running totals and its matching group.
func (t *UsageTotals) addRecord(rec UsageRecord) {
	t.Records++
	t.InputTokens += valueOrZero(rec.InputTokens)
	t.OutputTokens += valueOrZero(rec.OutputTokens)
	t.ReasoningTokens += valueOrZero(rec.ReasoningTokens)
	t.CacheReadTokens += valueOrZero(rec.CacheReadTokens)
	t.CacheWriteTokens += valueOrZero(rec.CacheWriteTokens)
	t.TotalTokens += rec.totalTokens()
	if rec.Cost != nil {
		t.Cost += *rec.Cost
	}
	if t.Currency == "" && rec.Currency != "" {
		t.Currency = rec.Currency
	}
	t.foldGroup(rec)
}

// foldGroup folds a record into the subtotal for its (provider, model, currency)
// triple, appending a new group the first time that triple appears.
func (t *UsageTotals) foldGroup(rec UsageRecord) {
	i := t.groupIndex(rec.Provider, rec.Model, rec.Currency)
	if i < 0 {
		t.Groups = append(t.Groups, UsageGroup{
			Provider: rec.Provider, Model: rec.Model, Currency: rec.Currency,
		})
		i = len(t.Groups) - 1
	}
	t.Groups[i].addRecord(rec)
}

// groupIndex returns the index of the group matching the triple, or -1 if none
// has been started yet.
func (t *UsageTotals) groupIndex(provider, model, currency string) int {
	for i := range t.Groups {
		g := &t.Groups[i]
		if g.Provider == provider && g.Model == model && g.Currency == currency {
			return i
		}
	}
	return -1
}

// addRecord folds one record into the group subtotal.
func (g *UsageGroup) addRecord(rec UsageRecord) {
	g.Records++
	g.InputTokens += valueOrZero(rec.InputTokens)
	g.OutputTokens += valueOrZero(rec.OutputTokens)
	g.ReasoningTokens += valueOrZero(rec.ReasoningTokens)
	g.CacheReadTokens += valueOrZero(rec.CacheReadTokens)
	g.CacheWriteTokens += valueOrZero(rec.CacheWriteTokens)
	total := rec.totalTokens()
	g.TotalTokens += total
	if total > g.PeakContextTokens {
		g.PeakContextTokens = total
	}
	if rec.ContextWindow != nil && *rec.ContextWindow > g.ContextWindow {
		g.ContextWindow = *rec.ContextWindow
	}
	if rec.Cost != nil {
		g.Cost += *rec.Cost
		g.HasCost = true
		g.noteCostKind(rec.Estimated)
	}
}

// noteCostKind records whether the group's costs are all reported, all estimated,
// or a mix of the two, so a combined figure is never silently mislabeled.
func (g *UsageGroup) noteCostKind(estimated bool) {
	kind := costKindReported
	if estimated {
		kind = costKindEstimated
	}
	if g.CostKind == "" {
		g.CostKind = kind
		return
	}
	if g.CostKind != kind {
		g.CostKind = costKindMixed
	}
}

// UsageReport is the whole attributed roll-up at a moment in time. Its numbers
// are absolute, so a consumer adopts them wholesale rather than accumulating
// deltas. Maps are keyed so a scope with no usage is simply absent.
type UsageReport struct {
	// Project is the grand total across every run in the session's project.
	Project UsageTotals `json:"project"`
	// Runs is the per-run (session) total, keyed by run ID. Pause and resume keep
	// the same run ID, so a resumed run keeps accumulating into the same entry.
	Runs map[string]UsageTotals `json:"runs"`
	// Stories is the per-story total, keyed by runID/storyID. Retried and failed
	// attempts at a story all fold into its entry.
	Stories map[string]UsageTotals `json:"stories"`
	// Attempts is the per-attempt total, keyed by runID/storyID#attempt.
	Attempts map[string]UsageTotals `json:"attempts"`
}

// storyScopeKey identifies a story's totals within a run.
func storyScopeKey(runID, storyID string) string {
	return runID + "/" + storyID
}

// attemptScopeKey identifies a single attempt's totals within a story.
func attemptScopeKey(runID, storyID string, attempt int) string {
	return storyScopeKey(runID, storyID) + "#" + strconv.Itoa(attempt)
}

// buildReport rebuilds the whole report from the ordered record set. Recomputing
// rather than mutating totals in place keeps the scopes guaranteed consistent
// with the records and with each other; the record count is small.
func buildReport(records []UsageRecord) UsageReport {
	rep := UsageReport{
		Runs:     make(map[string]UsageTotals),
		Stories:  make(map[string]UsageTotals),
		Attempts: make(map[string]UsageTotals),
	}
	for _, rec := range records {
		rep.Project.addRecord(rec)
		accumulate(rep.Runs, rec.RunID, rec)
		if rec.StoryID == "" {
			continue
		}
		accumulate(rep.Stories, storyScopeKey(rec.RunID, rec.StoryID), rec)
		accumulate(rep.Attempts, attemptScopeKey(rec.RunID, rec.StoryID, rec.Attempt), rec)
	}
	return rep
}

// accumulate folds a record into the totals stored at key.
func accumulate(m map[string]UsageTotals, key string, rec UsageRecord) {
	totals := m[key]
	totals.addRecord(rec)
	m[key] = totals
}

// usageAttribution is the non-usage context a record is stamped with.
type usageAttribution struct {
	runID    string
	prd      string
	storyID  string
	attempt  int
	provider string
	at       int64
}

// buildUsageRecord stamps a normalized usage payload with its attribution. The
// token pointers are copied so the record does not alias the loop's Usage, which
// is discarded once the attempt ends.
func buildUsageRecord(key string, attr usageAttribution, u *chiefloop.Usage) UsageRecord {
	rec := UsageRecord{
		Key:     key,
		RunID:   attr.runID,
		PRD:     attr.prd,
		StoryID: attr.storyID,
		Attempt: attr.attempt,
		At:      attr.at,
	}
	if u == nil {
		rec.Provider = attr.provider
		return rec
	}
	rec.InputTokens = copyInt64(u.InputTokens)
	rec.OutputTokens = copyInt64(u.OutputTokens)
	rec.ReasoningTokens = copyInt64(u.ReasoningTokens)
	rec.CacheReadTokens = copyInt64(u.CacheReadTokens)
	rec.CacheWriteTokens = copyInt64(u.CacheWriteTokens)
	rec.TotalTokens = copyInt64(u.TotalTokens)
	rec.ContextWindow = copyInt64(u.ContextWindow)
	rec.Cost, rec.Estimated = resolveCost(u)
	rec.Currency = u.Currency
	rec.Model = u.Model
	rec.Provider = attr.provider
	return rec
}

// resolveCost prefers the provider-reported cost and falls back to a locally
// estimated one, reporting which kind was chosen so it can be labeled downstream.
func resolveCost(u *chiefloop.Usage) (*float64, bool) {
	if u.ReportedCost != nil {
		return copyFloat64(u.ReportedCost), false
	}
	return copyFloat64(u.EstimatedCost), u.EstimatedCost != nil
}

// usageLedger owns the session's usage records. It assigns stable keys,
// deduplicates on submission, persists every change, and answers report queries.
// Safe for concurrent use: several runs record into it at once.
//
// Persistence happens inside add under the same lock that appends the record, so
// concurrent updates serialize and the last write to reach disk is always the
// most complete one — a slower goroutine can never clobber the file with a stale
// subset of the records.
type usageLedger struct {
	mu       sync.Mutex
	seen     map[string]struct{}
	records  []UsageRecord
	counters map[string]int // per attempt scope, for stable key assignment

	// store persists records to the open project's .chief/. Nil before a project
	// is open (and in pure unit tests), where the ledger is in-memory only.
	store *usageStore
	log   *slog.Logger
}

func newUsageLedger() *usageLedger {
	return &usageLedger{
		seen:     make(map[string]struct{}),
		counters: make(map[string]int),
	}
}

// nextKey returns a fresh, stable key for the next usage payload of an attempt.
// Distinct payloads within an attempt get distinct keys; the same payload
// re-submitted with its assigned key collapses in add.
func (l *usageLedger) nextKey(scope string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	i := l.counters[scope]
	l.counters[scope]++
	return scope + ":" + strconv.Itoa(i)
}

// add records a usage record, ignoring a key already seen, and persists the new
// history. The bool reports whether the record was newly counted. A persistence
// failure is logged rather than returned: the in-memory total is already correct
// and the next successful write recovers the file.
func (l *usageLedger) add(rec UsageRecord) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.seen[rec.Key]; ok {
		return false
	}
	l.seen[rec.Key] = struct{}{}
	l.records = append(l.records, rec)
	l.persistLocked()
	return true
}

// persistLocked writes the current records through the store, if one is attached.
// Callers hold l.mu.
func (l *usageLedger) persistLocked() {
	if l.store == nil {
		return
	}
	if err := l.store.save(l.records); err != nil && l.log != nil {
		l.log.Warn("persist usage history", "error", err)
	}
}

// report returns the absolute cumulative roll-up across every recorded usage.
func (l *usageLedger) report() UsageReport {
	l.mu.Lock()
	defer l.mu.Unlock()
	return buildReport(l.records)
}

// open points the ledger at a project's store and loads its persisted history in
// place, replacing whatever the previous project left behind. Reset-and-load in
// place (rather than allocating a new ledger) means a live run goroutine still
// holding this pointer keeps writing into the same object.
//
// Key counters are restored from the loaded keys so freshly assigned keys pick up
// after the persisted ones instead of colliding with them.
func (l *usageLedger) open(store *usageStore, records []UsageRecord, log *slog.Logger) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.store = store
	l.log = log
	l.seen = make(map[string]struct{}, len(records))
	l.records = nil
	l.counters = make(map[string]int)
	for _, rec := range records {
		if _, ok := l.seen[rec.Key]; ok {
			continue
		}
		l.seen[rec.Key] = struct{}{}
		l.records = append(l.records, rec)
		l.restoreCounterLocked(rec.Key)
	}
}

// restoreCounterLocked advances the per-scope key counter past a loaded key, so
// the next assigned key for that scope does not reuse an existing one. Keys are
// "<scope>:<index>"; a key that does not fit that shape is left alone. Callers
// hold l.mu.
func (l *usageLedger) restoreCounterLocked(key string) {
	sep := strings.LastIndex(key, ":")
	if sep < 0 {
		return
	}
	index, err := strconv.Atoi(key[sep+1:])
	if err != nil {
		return
	}
	if scope := key[:sep]; index+1 > l.counters[scope] {
		l.counters[scope] = index + 1
	}
}

func valueOrZero(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func copyInt64(p *int64) *int64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func copyFloat64(p *float64) *float64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
