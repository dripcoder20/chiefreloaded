package loop

import "testing"

// assertInt64Ptr fails the test unless p is non-nil and equal to want.
func assertInt64Ptr(t *testing.T, field string, p *int64, want int64) {
	t.Helper()
	if p == nil {
		t.Errorf("%s: expected %d, got nil (unavailable)", field, want)
		return
	}
	if *p != want {
		t.Errorf("%s: expected %d, got %d", field, want, *p)
	}
}

// assertFloatPtr fails the test unless p is non-nil and equal to want.
func assertFloatPtr(t *testing.T, field string, p *float64, want float64) {
	t.Helper()
	if p == nil {
		t.Errorf("%s: expected %v, got nil (unavailable)", field, want)
		return
	}
	if *p != want {
		t.Errorf("%s: expected %v, got %v", field, want, *p)
	}
}

// extracted fails the test unless the line yielded usage without an error.
func extracted(t *testing.T, extract usageExtractor, line string) *Usage {
	t.Helper()
	u, err := extract(line)
	if err != nil {
		t.Fatalf("unexpected extraction error: %v", err)
	}
	if u == nil {
		t.Fatal("expected usage, got nil (unavailable)")
	}
	return u
}

// assertNoUsage fails the test unless the line yielded neither usage nor error.
func assertNoUsage(t *testing.T, extract usageExtractor, line string) {
	t.Helper()
	u, err := extract(line)
	if err != nil {
		t.Fatalf("unexpected extraction error: %v", err)
	}
	if u != nil {
		t.Errorf("expected no usage, got %+v", u)
	}
}

// assertMalformed fails the test unless the line was reported as malformed.
func assertMalformed(t *testing.T, extract usageExtractor, line string) {
	t.Helper()
	u, err := extract(line)
	if err == nil {
		t.Fatalf("expected a malformed-payload error, got usage %+v", u)
	}
	if u != nil {
		t.Errorf("a malformed payload must yield no usage, got %+v", u)
	}
}

// --- Model semantics -------------------------------------------------------

func TestFinalizeUsage_missingFieldsStayUnavailable(t *testing.T) {
	in := int64(10)
	u, err := finalizeUsage(&Usage{InputTokens: &in})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil {
		t.Fatal("expected usage, got nil")
	}
	assertInt64Ptr(t, "InputTokens", u.InputTokens, 10)
	// Every unset field must remain nil, never coerced to 0.
	for name, p := range map[string]*int64{
		"OutputTokens":     u.OutputTokens,
		"ReasoningTokens":  u.ReasoningTokens,
		"CacheReadTokens":  u.CacheReadTokens,
		"CacheWriteTokens": u.CacheWriteTokens,
		"TotalTokens":      u.TotalTokens,
		"ContextWindow":    u.ContextWindow,
	} {
		if p != nil {
			t.Errorf("%s: expected nil, got %d", name, *p)
		}
	}
}

func TestFinalizeUsage_reportedZeroIsPreserved(t *testing.T) {
	zero := int64(0)
	u, err := finalizeUsage(&Usage{OutputTokens: &zero})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil || u.OutputTokens == nil {
		t.Fatal("expected reported zero to be preserved")
	}
	if *u.OutputTokens != 0 {
		t.Errorf("expected 0, got %d", *u.OutputTokens)
	}
}

func TestFinalizeUsage_emptyReturnsNil(t *testing.T) {
	u, err := finalizeUsage(&Usage{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u != nil {
		t.Errorf("expected nil for empty usage, got %+v", u)
	}
}

func TestFinalizeUsage_negativeTokensIsMalformed(t *testing.T) {
	neg := int64(-5)
	if _, err := finalizeUsage(&Usage{InputTokens: &neg}); err == nil {
		t.Fatal("expected error for negative token count")
	}
}

func TestFinalizeUsage_defaultsCurrencyWhenCostPresent(t *testing.T) {
	cost := 0.5
	u, err := finalizeUsage(&Usage{ReportedCost: &cost})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil || u.Currency != UsageCurrencyUSD {
		t.Fatalf("expected currency %q, got %+v", UsageCurrencyUSD, u)
	}
}

// --- Claude ----------------------------------------------------------------

func TestExtractClaudeUsage_complete(t *testing.T) {
	line := `{"type":"assistant","message":{"model":"claude-opus-4-8","content":[{"type":"text","text":"working"}],"usage":{"input_tokens":2,"output_tokens":8,"cache_read_input_tokens":20572,"cache_creation_input_tokens":9974}}}`
	u := extracted(t, extractClaudeUsage, line)
	if u.Model != "claude-opus-4-8" {
		t.Errorf("expected model claude-opus-4-8, got %q", u.Model)
	}
	assertInt64Ptr(t, "InputTokens", u.InputTokens, 2)
	assertInt64Ptr(t, "OutputTokens", u.OutputTokens, 8)
	assertInt64Ptr(t, "CacheReadTokens", u.CacheReadTokens, 20572)
	assertInt64Ptr(t, "CacheWriteTokens", u.CacheWriteTokens, 9974)
}

func TestExtractClaudeUsage_partial(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":100,"output_tokens":0}}}`
	u := extracted(t, extractClaudeUsage, line)
	assertInt64Ptr(t, "InputTokens", u.InputTokens, 100)
	assertInt64Ptr(t, "OutputTokens", u.OutputTokens, 0)
	if u.CacheReadTokens != nil {
		t.Errorf("expected CacheReadTokens unavailable, got %d", *u.CacheReadTokens)
	}
	if u.CacheWriteTokens != nil {
		t.Errorf("expected CacheWriteTokens unavailable, got %d", *u.CacheWriteTokens)
	}
}

func TestExtractClaudeUsage_resultCost(t *testing.T) {
	line := `{"type":"result","subtype":"success","total_cost_usd":0.1234,"usage":{"input_tokens":50,"output_tokens":25}}`
	u := extracted(t, extractClaudeUsage, line)
	assertFloatPtr(t, "ReportedCost", u.ReportedCost, 0.1234)
	if u.Currency != UsageCurrencyUSD {
		t.Errorf("expected currency USD, got %q", u.Currency)
	}
	assertInt64Ptr(t, "InputTokens", u.InputTokens, 50)
}

func TestExtractClaudeUsage_missing(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"no usage here"}]}}`
	assertNoUsage(t, extractClaudeUsage, line)
}

func TestExtractClaudeUsage_malformed(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"x"}],"usage":{"input_tokens":"oops"}}}`
	assertMalformed(t, extractClaudeUsage, line)
}

func TestExtractClaudeUsage_duplicate(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"x"}],"usage":{"input_tokens":5,"output_tokens":7}}}`
	// Extraction is pure: duplicate delivery yields identical values (dedup is a
	// later aggregation concern, not the parser's).
	first := extracted(t, extractClaudeUsage, line)
	second := extracted(t, extractClaudeUsage, line)
	assertInt64Ptr(t, "first InputTokens", first.InputTokens, 5)
	assertInt64Ptr(t, "second InputTokens", second.InputTokens, 5)
	assertInt64Ptr(t, "first OutputTokens", first.OutputTokens, 7)
	assertInt64Ptr(t, "second OutputTokens", second.OutputTokens, 7)
}

// The vendored parser must be untouched by usage extraction: an assistant line
// still produces its text event, and a result line is still ignored.
func TestParseLine_isUnchangedByUsageExtraction(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"working"}],"usage":{"input_tokens":2}}}`
	ev := ParseLine(line)
	if ev == nil || ev.Type != EventAssistantText || ev.Text != "working" {
		t.Fatalf("expected assistant text event, got %+v", ev)
	}
	if ev := ParseLine(`{"type":"result","total_cost_usd":0.1}`); ev != nil {
		t.Errorf("expected result line to stay ignored upstream, got %+v", ev)
	}
}

// --- Codex -----------------------------------------------------------------

func TestExtractCodexUsage_partial(t *testing.T) {
	line := `{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":0}}`
	u := extracted(t, extractCodexUsage, line)
	assertInt64Ptr(t, "InputTokens", u.InputTokens, 10)
	assertInt64Ptr(t, "OutputTokens", u.OutputTokens, 0)
	if u.CacheReadTokens != nil {
		t.Errorf("expected CacheReadTokens unavailable, got %d", *u.CacheReadTokens)
	}
}

func TestExtractCodexUsage_missing(t *testing.T) {
	assertNoUsage(t, extractCodexUsage, `{"type":"turn.completed"}`)
}

func TestExtractCodexUsage_malformed(t *testing.T) {
	assertMalformed(t, extractCodexUsage, `{"type":"turn.completed","usage":{"input_tokens":[1,2,3]}}`)
}

func TestExtractCodexUsage_duplicate(t *testing.T) {
	line := `{"type":"turn.completed","usage":{"input_tokens":24763,"cached_input_tokens":24448,"output_tokens":122}}`
	a := extracted(t, extractCodexUsage, line)
	b := extracted(t, extractCodexUsage, line)
	assertInt64Ptr(t, "a InputTokens", a.InputTokens, 24763)
	assertInt64Ptr(t, "b InputTokens", b.InputTokens, 24763)
	assertInt64Ptr(t, "a CacheReadTokens", a.CacheReadTokens, 24448)
}

// --- OpenCode --------------------------------------------------------------

func TestExtractOpenCodeUsage_complete(t *testing.T) {
	line := `{"type":"step_finish","part":{"reason":"tool-calls","cost":0.001,"tokens":{"input":671,"output":8,"reasoning":0,"cache":{"read":21415,"write":0}}}}`
	u := extracted(t, extractOpenCodeUsage, line)
	assertInt64Ptr(t, "InputTokens", u.InputTokens, 671)
	assertInt64Ptr(t, "OutputTokens", u.OutputTokens, 8)
	assertInt64Ptr(t, "ReasoningTokens", u.ReasoningTokens, 0)
	assertInt64Ptr(t, "CacheReadTokens", u.CacheReadTokens, 21415)
	assertInt64Ptr(t, "CacheWriteTokens", u.CacheWriteTokens, 0)
	assertFloatPtr(t, "ReportedCost", u.ReportedCost, 0.001)
}

// A step_finish with reason "stop" still ends the run upstream; usage rides the
// same line and must be extracted regardless of the reason.
func TestExtractOpenCodeUsage_onStop(t *testing.T) {
	line := `{"type":"step_finish","part":{"reason":"stop","cost":0.002,"tokens":{"input":10,"output":2}}}`
	u := extracted(t, extractOpenCodeUsage, line)
	assertInt64Ptr(t, "InputTokens", u.InputTokens, 10)
	assertFloatPtr(t, "ReportedCost", u.ReportedCost, 0.002)
	if ev := ParseLineOpenCode(line); ev == nil || ev.Type != EventComplete {
		t.Fatalf("expected upstream EventComplete to be unaffected, got %+v", ev)
	}
}

func TestExtractOpenCodeUsage_partial(t *testing.T) {
	line := `{"type":"step_finish","part":{"reason":"tool-calls","tokens":{"input":5}}}`
	u := extracted(t, extractOpenCodeUsage, line)
	assertInt64Ptr(t, "InputTokens", u.InputTokens, 5)
	if u.OutputTokens != nil {
		t.Errorf("expected OutputTokens unavailable, got %d", *u.OutputTokens)
	}
	if u.ReportedCost != nil {
		t.Errorf("expected ReportedCost unavailable, got %v", *u.ReportedCost)
	}
}

func TestExtractOpenCodeUsage_missing(t *testing.T) {
	assertNoUsage(t, extractOpenCodeUsage, `{"type":"step_finish","part":{"reason":"tool-calls"}}`)
}

func TestExtractOpenCodeUsage_malformed(t *testing.T) {
	line := `{"type":"step_finish","part":{"reason":"tool-calls","tokens":{"input":"nope"}}}`
	assertMalformed(t, extractOpenCodeUsage, line)
}

func TestExtractOpenCodeUsage_duplicate(t *testing.T) {
	line := `{"type":"step_finish","part":{"reason":"tool-calls","tokens":{"input":100,"output":50}}}`
	a := extracted(t, extractOpenCodeUsage, line)
	b := extracted(t, extractOpenCodeUsage, line)
	assertInt64Ptr(t, "a OutputTokens", a.OutputTokens, 50)
	assertInt64Ptr(t, "b OutputTokens", b.OutputTokens, 50)
}

// --- Cursor ----------------------------------------------------------------

func TestExtractCursorUsage_complete(t *testing.T) {
	line := `{"type":"result","subtype":"success","model":"claude-4-sonnet","total_cost_usd":0.045,"usage":{"input_tokens":1200,"output_tokens":300,"cache_read_input_tokens":800,"cache_creation_input_tokens":400}}`
	u := extracted(t, extractCursorUsage, line)
	if u.Model != "claude-4-sonnet" {
		t.Errorf("expected model claude-4-sonnet, got %q", u.Model)
	}
	assertInt64Ptr(t, "InputTokens", u.InputTokens, 1200)
	assertInt64Ptr(t, "OutputTokens", u.OutputTokens, 300)
	assertInt64Ptr(t, "CacheReadTokens", u.CacheReadTokens, 800)
	assertInt64Ptr(t, "CacheWriteTokens", u.CacheWriteTokens, 400)
	assertFloatPtr(t, "ReportedCost", u.ReportedCost, 0.045)
}

func TestExtractCursorUsage_partial(t *testing.T) {
	line := `{"type":"result","subtype":"success","usage":{"input_tokens":10,"output_tokens":0}}`
	u := extracted(t, extractCursorUsage, line)
	assertInt64Ptr(t, "InputTokens", u.InputTokens, 10)
	assertInt64Ptr(t, "OutputTokens", u.OutputTokens, 0)
	if u.CacheReadTokens != nil {
		t.Errorf("expected CacheReadTokens unavailable, got %d", *u.CacheReadTokens)
	}
}

func TestExtractCursorUsage_missing(t *testing.T) {
	assertNoUsage(t, extractCursorUsage, `{"type":"result","subtype":"success","duration_ms":1234,"result":"done"}`)
}

func TestExtractCursorUsage_malformed(t *testing.T) {
	assertMalformed(t, extractCursorUsage, `{"type":"result","subtype":"success","usage":{"input_tokens":{"x":1}}}`)
}

func TestExtractCursorUsage_duplicate(t *testing.T) {
	line := `{"type":"result","subtype":"success","usage":{"input_tokens":1200,"output_tokens":300}}`
	a := extracted(t, extractCursorUsage, line)
	b := extracted(t, extractCursorUsage, line)
	assertInt64Ptr(t, "a InputTokens", a.InputTokens, 1200)
	assertInt64Ptr(t, "b InputTokens", b.InputTokens, 1200)
}

// A line that is not JSON at all is upstream's business, not a usage error.
func TestExtractUsage_nonJSONLineIsNotAnError(t *testing.T) {
	for name, extract := range usageExtractors {
		u, err := extract("this is plain text, not json")
		if u != nil || err != nil {
			t.Errorf("%s: expected (nil, nil) for a non-JSON line, got (%+v, %v)", name, u, err)
		}
	}
}
