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

func usageEvent(t *testing.T, ev *Event) *Usage {
	t.Helper()
	if ev == nil {
		t.Fatal("expected event, got nil")
	}
	if ev.Usage == nil {
		t.Fatalf("expected Usage set on %v event, got nil", ev.Type)
	}
	return ev.Usage
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

func TestParseLine_claudeComplete(t *testing.T) {
	line := `{"type":"assistant","message":{"model":"claude-opus-4-8","content":[{"type":"text","text":"working"}],"usage":{"input_tokens":2,"output_tokens":8,"cache_read_input_tokens":20572,"cache_creation_input_tokens":9974}}}`
	ev := ParseLine(line)
	u := usageEvent(t, ev)
	if u.Model != "claude-opus-4-8" {
		t.Errorf("expected model claude-opus-4-8, got %q", u.Model)
	}
	assertInt64Ptr(t, "InputTokens", u.InputTokens, 2)
	assertInt64Ptr(t, "OutputTokens", u.OutputTokens, 8)
	assertInt64Ptr(t, "CacheReadTokens", u.CacheReadTokens, 20572)
	assertInt64Ptr(t, "CacheWriteTokens", u.CacheWriteTokens, 9974)
	// Assistant text must still be carried alongside the usage.
	if ev.Type != EventAssistantText || ev.Text != "working" {
		t.Errorf("expected assistant text carried, got %v %q", ev.Type, ev.Text)
	}
}

func TestParseLine_claudePartial(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":100,"output_tokens":0}}}`
	u := usageEvent(t, ParseLine(line))
	assertInt64Ptr(t, "InputTokens", u.InputTokens, 100)
	assertInt64Ptr(t, "OutputTokens", u.OutputTokens, 0)
	if u.CacheReadTokens != nil {
		t.Errorf("expected CacheReadTokens unavailable, got %d", *u.CacheReadTokens)
	}
	if u.CacheWriteTokens != nil {
		t.Errorf("expected CacheWriteTokens unavailable, got %d", *u.CacheWriteTokens)
	}
}

func TestParseLine_claudeResultCost(t *testing.T) {
	line := `{"type":"result","subtype":"success","total_cost_usd":0.1234,"usage":{"input_tokens":50,"output_tokens":25}}`
	ev := ParseLine(line)
	u := usageEvent(t, ev)
	if ev.Type != EventUsage {
		t.Errorf("expected EventUsage, got %v", ev.Type)
	}
	assertFloatPtr(t, "ReportedCost", u.ReportedCost, 0.1234)
	if u.Currency != UsageCurrencyUSD {
		t.Errorf("expected currency USD, got %q", u.Currency)
	}
	assertInt64Ptr(t, "InputTokens", u.InputTokens, 50)
}

func TestParseLine_claudeMissingUsage(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"no usage here"}]}}`
	ev := ParseLine(line)
	if ev == nil {
		t.Fatal("expected assistant text event, got nil")
	}
	if ev.Usage != nil {
		t.Errorf("expected no usage, got %+v", ev.Usage)
	}
	if ev.Type != EventAssistantText {
		t.Errorf("expected EventAssistantText, got %v", ev.Type)
	}
}

func TestParseLine_claudeMalformedUsage(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"x"}],"usage":{"input_tokens":"oops"}}}`
	ev := ParseLine(line)
	if ev == nil {
		t.Fatal("expected warning event, got nil")
	}
	if ev.Type != EventWarning {
		t.Fatalf("expected EventWarning, got %v", ev.Type)
	}
	if ev.Text == "" {
		t.Error("expected a diagnosable warning message")
	}
}

func TestParseLine_claudeDuplicateUsage(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"x"}],"usage":{"input_tokens":5,"output_tokens":7}}}`
	first := usageEvent(t, ParseLine(line))
	second := usageEvent(t, ParseLine(line))
	// Parsing is pure: duplicate delivery yields identical values (dedup is a
	// later aggregation concern, not the parser's).
	assertInt64Ptr(t, "first InputTokens", first.InputTokens, 5)
	assertInt64Ptr(t, "second InputTokens", second.InputTokens, 5)
	assertInt64Ptr(t, "first OutputTokens", first.OutputTokens, 7)
	assertInt64Ptr(t, "second OutputTokens", second.OutputTokens, 7)
}

// --- Codex -----------------------------------------------------------------

func TestParseLineCodex_usagePartial(t *testing.T) {
	line := `{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":0}}`
	u := usageEvent(t, ParseLineCodex(line))
	assertInt64Ptr(t, "InputTokens", u.InputTokens, 10)
	assertInt64Ptr(t, "OutputTokens", u.OutputTokens, 0)
	if u.CacheReadTokens != nil {
		t.Errorf("expected CacheReadTokens unavailable, got %d", *u.CacheReadTokens)
	}
}

func TestParseLineCodex_usageMissing(t *testing.T) {
	if ev := ParseLineCodex(`{"type":"turn.completed"}`); ev != nil {
		t.Errorf("expected nil for turn.completed without usage, got %v", ev)
	}
}

func TestParseLineCodex_usageMalformed(t *testing.T) {
	line := `{"type":"turn.completed","usage":{"input_tokens":[1,2,3]}}`
	ev := ParseLineCodex(line)
	if ev == nil || ev.Type != EventWarning {
		t.Fatalf("expected EventWarning, got %v", ev)
	}
}

func TestParseLineCodex_usageDuplicate(t *testing.T) {
	line := `{"type":"turn.completed","usage":{"input_tokens":24763,"cached_input_tokens":24448,"output_tokens":122}}`
	a := usageEvent(t, ParseLineCodex(line))
	b := usageEvent(t, ParseLineCodex(line))
	assertInt64Ptr(t, "a InputTokens", a.InputTokens, 24763)
	assertInt64Ptr(t, "b InputTokens", b.InputTokens, 24763)
}

// --- OpenCode --------------------------------------------------------------

func TestParseLineOpenCode_usageComplete(t *testing.T) {
	line := `{"type":"step_finish","part":{"reason":"tool-calls","cost":0.001,"tokens":{"input":671,"output":8,"reasoning":0,"cache":{"read":21415,"write":0}}}}`
	ev := ParseLineOpenCode(line)
	u := usageEvent(t, ev)
	if ev.Type != EventUsage {
		t.Errorf("expected EventUsage, got %v", ev.Type)
	}
	assertInt64Ptr(t, "InputTokens", u.InputTokens, 671)
	assertInt64Ptr(t, "OutputTokens", u.OutputTokens, 8)
	assertInt64Ptr(t, "ReasoningTokens", u.ReasoningTokens, 0)
	assertInt64Ptr(t, "CacheReadTokens", u.CacheReadTokens, 21415)
	assertInt64Ptr(t, "CacheWriteTokens", u.CacheWriteTokens, 0)
	assertFloatPtr(t, "ReportedCost", u.ReportedCost, 0.001)
}

func TestParseLineOpenCode_usageOnStopCarriesComplete(t *testing.T) {
	line := `{"type":"step_finish","part":{"reason":"stop","cost":0.002,"tokens":{"input":10,"output":2}}}`
	ev := ParseLineOpenCode(line)
	if ev == nil || ev.Type != EventComplete {
		t.Fatalf("expected EventComplete, got %v", ev)
	}
	u := usageEvent(t, ev)
	assertInt64Ptr(t, "InputTokens", u.InputTokens, 10)
	assertFloatPtr(t, "ReportedCost", u.ReportedCost, 0.002)
}

func TestParseLineOpenCode_usagePartial(t *testing.T) {
	line := `{"type":"step_finish","part":{"reason":"tool-calls","tokens":{"input":5}}}`
	u := usageEvent(t, ParseLineOpenCode(line))
	assertInt64Ptr(t, "InputTokens", u.InputTokens, 5)
	if u.OutputTokens != nil {
		t.Errorf("expected OutputTokens unavailable, got %d", *u.OutputTokens)
	}
	if u.ReportedCost != nil {
		t.Errorf("expected ReportedCost unavailable, got %v", *u.ReportedCost)
	}
}

func TestParseLineOpenCode_usageMissing(t *testing.T) {
	line := `{"type":"step_finish","part":{"reason":"tool-calls"}}`
	if ev := ParseLineOpenCode(line); ev != nil {
		t.Errorf("expected nil for step_finish without usage, got %v", ev)
	}
}

func TestParseLineOpenCode_usageMalformed(t *testing.T) {
	line := `{"type":"step_finish","part":{"reason":"tool-calls","tokens":{"input":"nope"}}}`
	ev := ParseLineOpenCode(line)
	if ev == nil || ev.Type != EventWarning {
		t.Fatalf("expected EventWarning, got %v", ev)
	}
}

func TestParseLineOpenCode_usageDuplicate(t *testing.T) {
	line := `{"type":"step_finish","part":{"reason":"tool-calls","tokens":{"input":100,"output":50}}}`
	a := usageEvent(t, ParseLineOpenCode(line))
	b := usageEvent(t, ParseLineOpenCode(line))
	assertInt64Ptr(t, "a OutputTokens", a.OutputTokens, 50)
	assertInt64Ptr(t, "b OutputTokens", b.OutputTokens, 50)
}

// --- Cursor ----------------------------------------------------------------

func TestParseLineCursor_usageComplete(t *testing.T) {
	line := `{"type":"result","subtype":"success","model":"claude-4-sonnet","total_cost_usd":0.045,"usage":{"input_tokens":1200,"output_tokens":300,"cache_read_input_tokens":800,"cache_creation_input_tokens":400}}`
	ev := ParseLineCursor(line)
	u := usageEvent(t, ev)
	if ev.Type != EventUsage {
		t.Errorf("expected EventUsage, got %v", ev.Type)
	}
	if u.Model != "claude-4-sonnet" {
		t.Errorf("expected model claude-4-sonnet, got %q", u.Model)
	}
	assertInt64Ptr(t, "InputTokens", u.InputTokens, 1200)
	assertInt64Ptr(t, "OutputTokens", u.OutputTokens, 300)
	assertInt64Ptr(t, "CacheReadTokens", u.CacheReadTokens, 800)
	assertInt64Ptr(t, "CacheWriteTokens", u.CacheWriteTokens, 400)
	assertFloatPtr(t, "ReportedCost", u.ReportedCost, 0.045)
}

func TestParseLineCursor_usagePartial(t *testing.T) {
	line := `{"type":"result","subtype":"success","usage":{"input_tokens":10,"output_tokens":0}}`
	u := usageEvent(t, ParseLineCursor(line))
	assertInt64Ptr(t, "InputTokens", u.InputTokens, 10)
	assertInt64Ptr(t, "OutputTokens", u.OutputTokens, 0)
	if u.CacheReadTokens != nil {
		t.Errorf("expected CacheReadTokens unavailable, got %d", *u.CacheReadTokens)
	}
}

func TestParseLineCursor_usageMissing(t *testing.T) {
	line := `{"type":"result","subtype":"success","duration_ms":1234,"result":"done"}`
	if ev := ParseLineCursor(line); ev != nil {
		t.Errorf("expected nil for result without usage, got %v", ev)
	}
}

func TestParseLineCursor_usageMalformed(t *testing.T) {
	line := `{"type":"result","subtype":"success","usage":{"input_tokens":{"x":1}}}`
	ev := ParseLineCursor(line)
	if ev == nil || ev.Type != EventWarning {
		t.Fatalf("expected EventWarning, got %v", ev)
	}
}

func TestParseLineCursor_usageDuplicate(t *testing.T) {
	line := `{"type":"result","subtype":"success","usage":{"input_tokens":1200,"output_tokens":300}}`
	a := usageEvent(t, ParseLineCursor(line))
	b := usageEvent(t, ParseLineCursor(line))
	assertInt64Ptr(t, "a InputTokens", a.InputTokens, 1200)
	assertInt64Ptr(t, "b InputTokens", b.InputTokens, 1200)
}
