package loop

import (
	"encoding/json"
	"errors"
	"strings"
)

type opencodeEvent struct {
	Type      string         `json:"type"`
	Timestamp int64          `json:"timestamp"`
	SessionID string         `json:"sessionID"`
	Part      *opencodePart  `json:"part,omitempty"`
	Error     *opencodeError `json:"error,omitempty"`
}

type opencodePart struct {
	ID       string          `json:"id"`
	Type     string          `json:"type,omitempty"`
	Text     string          `json:"text,omitempty"`
	Tool     string          `json:"tool,omitempty"`
	CallID   string          `json:"callID,omitempty"`
	Reason   string          `json:"reason,omitempty"`
	Snapshot string          `json:"snapshot,omitempty"`
	State    *opencodeState  `json:"state,omitempty"`
	Tokens   json.RawMessage `json:"tokens,omitempty"`
	Cost     *float64        `json:"cost,omitempty"`
}

type opencodeState struct {
	Status string                 `json:"status"`
	Input  map[string]interface{} `json:"input,omitempty"`
	Output string                 `json:"output,omitempty"`
	Title  string                 `json:"title,omitempty"`
	Time   *opencodeTime          `json:"time,omitempty"`
}

type opencodeTime struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

// opencodeTokens mirrors the token fields OpenCode reports. Pointer fields keep a
// missing field distinct from a reported value of 0.
type opencodeTokens struct {
	Input     *int64               `json:"input"`
	Output    *int64               `json:"output"`
	Reasoning *int64               `json:"reasoning"`
	Cache     *opencodeCacheTokens `json:"cache,omitempty"`
}

type opencodeCacheTokens struct {
	Read  *int64 `json:"read"`
	Write *int64 `json:"write"`
}

type opencodeError struct {
	Name string             `json:"name"`
	Data *opencodeErrorData `json:"data,omitempty"`
}

type opencodeErrorData struct {
	Message    string `json:"message"`
	StatusCode int    `json:"statusCode,omitempty"`
}

func ParseLineOpenCode(line string) *Event {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}

	var ev opencodeEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return nil
	}

	switch ev.Type {
	case "step_start":
		return &Event{Type: EventIterationStart}

	case "tool_use":
		if ev.Part == nil {
			return nil
		}
		if ev.Part.State != nil && ev.Part.State.Status == "completed" {
			return &Event{
				Type: EventToolResult,
				Tool: ev.Part.Tool,
				Text: ev.Part.State.Output,
			}
		}
		// Tool starting or in-progress
		return &Event{
			Type: EventToolStart,
			Tool: ev.Part.Tool,
		}

	case "text":
		if ev.Part == nil {
			return nil
		}
		if strings.Contains(ev.Part.Text, "<chief-done/>") {
			return &Event{
				Type: EventStoryDone,
				Text: ev.Part.Text,
			}
		}
		return &Event{
			Type: EventAssistantText,
			Text: ev.Part.Text,
		}

	case "step_finish":
		if ev.Part == nil {
			return nil
		}
		usage, err := extractOpenCodeUsage(ev.Part)
		if err != nil {
			return warningEvent("opencode", err)
		}
		if ev.Part.Reason == "stop" {
			return &Event{Type: EventComplete, Usage: usage}
		}
		if usage == nil {
			return nil
		}
		return &Event{Type: EventUsage, Usage: usage}

	case "error":
		msg := "unknown error"
		if ev.Error != nil {
			if ev.Error.Data != nil {
				msg = ev.Error.Data.Message
			}
			if msg == "" {
				msg = ev.Error.Name
			}
		}
		return &Event{Type: EventError, Err: errors.New(msg)}

	default:
		return nil
	}
}

// extractOpenCodeUsage normalizes the tokens and cost reported on a step_finish
// part. It returns an error only when the tokens object is present but malformed.
func extractOpenCodeUsage(part *opencodePart) (*Usage, error) {
	u := &Usage{ReportedCost: part.Cost}
	if len(part.Tokens) > 0 && string(part.Tokens) != "null" {
		var raw opencodeTokens
		if err := json.Unmarshal(part.Tokens, &raw); err != nil {
			return nil, err
		}
		u.InputTokens = raw.Input
		u.OutputTokens = raw.Output
		u.ReasoningTokens = raw.Reasoning
		if raw.Cache != nil {
			u.CacheReadTokens = raw.Cache.Read
			u.CacheWriteTokens = raw.Cache.Write
		}
	}
	return finalizeUsage(u)
}
