package loop

import (
	"errors"
	"fmt"
)

// UsageCurrencyUSD is the default ISO 4217 currency code for provider-reported
// costs, which every supported CLI reports in US dollars.
const UsageCurrencyUSD = "USD"

// errNegativeUsage is returned when a provider reports a negative token count,
// which violates the non-negative invariant of the usage model.
var errNegativeUsage = errors.New("usage token count is negative")

// Usage is the normalized, provider-agnostic representation of the usage a single
// agent payload reported. Every token field is an independently optional
// non-negative count: a nil pointer means the provider did not report that field,
// which is deliberately distinct from a reported value of 0.
type Usage struct {
	InputTokens      *int64
	OutputTokens     *int64
	ReasoningTokens  *int64
	CacheReadTokens  *int64
	CacheWriteTokens *int64
	TotalTokens      *int64

	// ReportedCost is the provider-supplied cost for the payload; EstimatedCost is
	// a locally derived cost. Currency is the ISO 4217 code both are expressed in.
	ReportedCost  *float64
	EstimatedCost *float64
	Currency      string

	// ContextWindow is the model's context-window size in tokens, when known.
	ContextWindow *int64
	// Model is the provider model identifier used for the request, when reported.
	Model string
}

// HasAnyField reports whether the usage carries at least one reported value, so
// callers can skip emitting empty usage payloads.
func (u *Usage) HasAnyField() bool {
	if u == nil {
		return false
	}
	tokens := []*int64{
		u.InputTokens, u.OutputTokens, u.ReasoningTokens,
		u.CacheReadTokens, u.CacheWriteTokens, u.TotalTokens, u.ContextWindow,
	}
	for _, t := range tokens {
		if t != nil {
			return true
		}
	}
	return u.ReportedCost != nil || u.EstimatedCost != nil || u.Model != ""
}

// validateTokens returns an error if any reported token count is negative.
func (u *Usage) validateTokens() error {
	tokens := []*int64{
		u.InputTokens, u.OutputTokens, u.ReasoningTokens,
		u.CacheReadTokens, u.CacheWriteTokens, u.TotalTokens, u.ContextWindow,
	}
	for _, t := range tokens {
		if t != nil && *t < 0 {
			return fmt.Errorf("%w: %d", errNegativeUsage, *t)
		}
	}
	return nil
}

// finalizeUsage validates the assembled usage and stamps a default currency when a
// cost is present but no currency was set. It returns an error for malformed
// (negative-token) usage so callers can raise a warning instead of crashing.
func finalizeUsage(u *Usage) (*Usage, error) {
	if err := u.validateTokens(); err != nil {
		return nil, err
	}
	if u.Currency == "" && (u.ReportedCost != nil || u.EstimatedCost != nil) {
		u.Currency = UsageCurrencyUSD
	}
	if !u.HasAnyField() {
		return nil, nil
	}
	return u, nil
}
