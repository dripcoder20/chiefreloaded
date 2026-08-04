package loop

// This file is Loop's, not chief's. Usage extraction lives here rather than
// inside the vendored parsers for one structural reason: chief's parsers return
// a *Event whose fields are fixed upstream, and there is no way to add a Usage
// field to that struct from a separate file. Editing the vendored parsers would
// fail `task verify-vendor` and be silently reverted by the next sync.
//
// So usage travels on its own path. ObserveUsage decorates a Provider: the
// wrapped ParseLine still returns whatever upstream produced — the agent-output
// stream is untouched — and the same raw line is additionally scanned for a
// usage payload, which is handed to a callback. Re-parsing the line costs one
// extra json.Unmarshal per usage-bearing line, which is a small price for a
// vendored tree that stays a clean regeneration.

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// UsageFunc receives the usage a single agent payload reported. A malformed
// usage payload arrives as a nil Usage and a non-nil error so the caller can
// raise a diagnosable warning; neither case may terminate the run.
type UsageFunc func(*Usage, error)

// usageExtractor pulls normalized usage out of one raw output line, returning
// (nil, nil) for the great majority of lines that carry none.
type usageExtractor func(line string) (*Usage, error)

// usageExtractors maps an output format to its extractor. Keys are lower-cased
// Provider.Name() values as implemented in internal/chief/agent.
var usageExtractors = map[string]usageExtractor{
	"claude":   extractClaudeUsage,
	"codex":    extractCodexUsage,
	"cursor":   extractCursorUsage,
	"opencode": extractOpenCodeUsage,
}

// UsageFormatter is implemented by a provider whose wire format is not the one
// its name implies — the scripted test agent is named "fake" but emits Claude's
// stream-json. Without it such a provider would silently report no usage.
type UsageFormatter interface {
	UsageFormat() string
}

// usageFormat reports which output format a provider emits.
func usageFormat(p Provider) string {
	if f, ok := p.(UsageFormatter); ok {
		return strings.ToLower(f.UsageFormat())
	}
	return strings.ToLower(p.Name())
}

// usageObserver is a Provider that reports usage as a side effect of parsing.
type usageObserver struct {
	Provider
	extract usageExtractor
	onUsage UsageFunc
}

// ObserveUsage wraps a provider so every parsed line is also scanned for usage.
//
// It returns the provider unchanged when the callback is nil or the provider's
// output format has no known usage shape, so callers never need to branch.
func ObserveUsage(p Provider, onUsage UsageFunc) Provider {
	if p == nil || onUsage == nil {
		return p
	}
	extract, ok := usageExtractors[usageFormat(p)]
	if !ok {
		return p
	}
	return &usageObserver{Provider: p, extract: extract, onUsage: onUsage}
}

// ParseLine delegates to the wrapped provider and reports any usage the same
// line carried. The returned event is upstream's, unmodified.
func (o *usageObserver) ParseLine(line string) *Event {
	usage, err := o.extract(line)
	if err != nil {
		o.onUsage(nil, fmt.Errorf("%s: malformed usage payload: %w", o.Provider.Name(), err))
	}
	if usage != nil {
		o.onUsage(usage, nil)
	}
	return o.Provider.ParseLine(line)
}

// LoopCommand and InteractiveCommand are redeclared so the embedded Provider's
// methods are not shadowed by accident if upstream widens the interface; they
// are pure delegation.
func (o *usageObserver) LoopCommand(ctx context.Context, prompt, workDir string) *exec.Cmd {
	return o.Provider.LoopCommand(ctx, prompt, workDir)
}

func (o *usageObserver) InteractiveCommand(workDir, prompt string) *exec.Cmd {
	return o.Provider.InteractiveCommand(workDir, prompt)
}

// ---------------------------------------------------------------- claude ----

// claudeUsageRaw mirrors the token fields Claude reports in a usage object.
// Pointer fields keep a missing field distinct from a reported value of 0.
type claudeUsageRaw struct {
	InputTokens              *int64 `json:"input_tokens"`
	OutputTokens             *int64 `json:"output_tokens"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
}

// claudeUsageLine is the subset of a Claude stream-json line usage lives on.
// Assistant lines nest it under message; result lines carry it at the top level
// alongside the run's total cost.
type claudeUsageLine struct {
	Type    string `json:"type"`
	Message *struct {
		Model string          `json:"model"`
		Usage json.RawMessage `json:"usage"`
	} `json:"message"`
	Model        string          `json:"model"`
	TotalCostUSD *float64        `json:"total_cost_usd"`
	Usage        json.RawMessage `json:"usage"`
}

// extractClaudeUsage reads usage from an assistant or result line. A line of any
// other type, or one that is not JSON at all, reports no usage and no error —
// chief's parser has already decided what such a line means.
func extractClaudeUsage(line string) (*Usage, error) {
	var l claudeUsageLine
	if err := json.Unmarshal([]byte(line), &l); err != nil {
		return nil, nil
	}
	switch l.Type {
	case "assistant":
		if l.Message == nil {
			return nil, nil
		}
		return claudeUsageFrom(l.Message.Usage, l.Message.Model, nil)
	case "result":
		return claudeUsageFrom(l.Usage, l.Model, l.TotalCostUSD)
	default:
		return nil, nil
	}
}

// claudeUsageFrom normalizes a Claude-shaped usage object plus model and cost.
// It returns an error only when the usage object is present but malformed.
func claudeUsageFrom(rawUsage json.RawMessage, model string, cost *float64) (*Usage, error) {
	u := &Usage{Model: model, ReportedCost: cost}
	if !isPresent(rawUsage) {
		return finalizeUsage(u)
	}
	var raw claudeUsageRaw
	if err := json.Unmarshal(rawUsage, &raw); err != nil {
		return nil, err
	}
	u.InputTokens = raw.InputTokens
	u.OutputTokens = raw.OutputTokens
	u.CacheReadTokens = raw.CacheReadInputTokens
	u.CacheWriteTokens = raw.CacheCreationInputTokens
	return finalizeUsage(u)
}

// ----------------------------------------------------------------- codex ----

// codexUsageRaw mirrors the token fields Codex reports in a turn.completed usage
// object. Pointer fields keep a missing field distinct from a reported 0.
type codexUsageRaw struct {
	InputTokens       *int64 `json:"input_tokens"`
	CachedInputTokens *int64 `json:"cached_input_tokens"`
	OutputTokens      *int64 `json:"output_tokens"`
}

// codexUsageLine is the subset of a Codex exec --json line usage lives on.
type codexUsageLine struct {
	Type  string          `json:"type"`
	Usage json.RawMessage `json:"usage"`
}

// extractCodexUsage reads usage from a turn.completed line. Codex reports no
// cost, reasoning tokens, cache writes or model, so those stay unavailable.
func extractCodexUsage(line string) (*Usage, error) {
	var l codexUsageLine
	if err := json.Unmarshal([]byte(line), &l); err != nil {
		return nil, nil
	}
	if l.Type != "turn.completed" || !isPresent(l.Usage) {
		return nil, nil
	}
	var raw codexUsageRaw
	if err := json.Unmarshal(l.Usage, &raw); err != nil {
		return nil, err
	}
	return finalizeUsage(&Usage{
		InputTokens:     raw.InputTokens,
		OutputTokens:    raw.OutputTokens,
		CacheReadTokens: raw.CachedInputTokens,
	})
}

// ---------------------------------------------------------------- cursor ----

// extractCursorUsage reads usage from a Cursor result line, which mirrors
// Claude's shape because the Cursor agent is Claude-based.
func extractCursorUsage(line string) (*Usage, error) {
	var l claudeUsageLine
	if err := json.Unmarshal([]byte(line), &l); err != nil {
		return nil, nil
	}
	if l.Type != "result" {
		return nil, nil
	}
	return claudeUsageFrom(l.Usage, l.Model, l.TotalCostUSD)
}

// -------------------------------------------------------------- opencode ----

// opencodeUsageTokens mirrors the token fields OpenCode reports. Pointer fields
// keep a missing field distinct from a reported value of 0.
type opencodeUsageTokens struct {
	Input     *int64 `json:"input"`
	Output    *int64 `json:"output"`
	Reasoning *int64 `json:"reasoning"`
	Cache     *struct {
		Read  *int64 `json:"read"`
		Write *int64 `json:"write"`
	} `json:"cache"`
}

// opencodeUsageLine is the subset of an OpenCode line usage lives on.
type opencodeUsageLine struct {
	Type string `json:"type"`
	Part *struct {
		Tokens json.RawMessage `json:"tokens"`
		Cost   *float64        `json:"cost"`
	} `json:"part"`
}

// extractOpenCodeUsage reads the tokens and cost reported on a step_finish part.
// OpenCode reports no model identifier, so it stays unavailable.
func extractOpenCodeUsage(line string) (*Usage, error) {
	var l opencodeUsageLine
	if err := json.Unmarshal([]byte(line), &l); err != nil {
		return nil, nil
	}
	if l.Type != "step_finish" || l.Part == nil {
		return nil, nil
	}
	u := &Usage{ReportedCost: l.Part.Cost}
	if !isPresent(l.Part.Tokens) {
		return finalizeUsage(u)
	}
	var raw opencodeUsageTokens
	if err := json.Unmarshal(l.Part.Tokens, &raw); err != nil {
		return nil, err
	}
	u.InputTokens = raw.Input
	u.OutputTokens = raw.Output
	u.ReasoningTokens = raw.Reasoning
	if raw.Cache != nil {
		u.CacheReadTokens = raw.Cache.Read
		u.CacheWriteTokens = raw.Cache.Write
	}
	return finalizeUsage(u)
}

// isPresent reports whether a raw JSON field was supplied with a real value, as
// opposed to being absent or an explicit null.
func isPresent(raw json.RawMessage) bool {
	return len(raw) > 0 && string(raw) != "null"
}
