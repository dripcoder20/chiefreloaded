package loop

import (
	"encoding/json"
	"errors"
	"strings"
)

// codexEvent represents the top-level structure of a Codex exec --json JSONL line.
type codexEvent struct {
	Type    string     `json:"type"`
	Item    *codexItem `json:"item,omitempty"`
	Message string     `json:"message,omitempty"` // top-level for type "error"
	Error   *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Usage json.RawMessage `json:"usage,omitempty"` // present on turn.completed
}

// codexUsageRaw mirrors the token fields Codex reports in a turn.completed usage
// object. Pointer fields keep a missing field distinct from a reported 0.
type codexUsageRaw struct {
	InputTokens       *int64 `json:"input_tokens"`
	CachedInputTokens *int64 `json:"cached_input_tokens"`
	OutputTokens      *int64 `json:"output_tokens"`
}

// codexItem represents an item in item.started / item.completed / item.updated events.
type codexItem struct {
	ID               string `json:"id"`
	Type             string `json:"type"`
	Text             string `json:"text,omitempty"`
	Command          string `json:"command,omitempty"`
	AggregatedOutput string `json:"aggregated_output,omitempty"`
	ExitCode         *int   `json:"exit_code,omitempty"`
	Status           string `json:"status,omitempty"`
	Server           string `json:"server,omitempty"`
	Tool             string `json:"tool,omitempty"`
}

// ParseLineCodex parses a single line of Codex exec --json JSONL output and returns an Event.
// If the line cannot be parsed or is not relevant, it returns nil.
func ParseLineCodex(line string) *Event {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}

	var ev codexEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return nil
	}

	switch ev.Type {
	case "thread.started", "turn.started":
		return &Event{Type: EventIterationStart}

	case "turn.failed":
		msg := ""
		if ev.Error != nil {
			msg = ev.Error.Message
		}
		return &Event{Type: EventError, Err: errors.New(msg)}

	case "error":
		msg := ev.Message
		if msg == "" && ev.Error != nil {
			msg = ev.Error.Message
		}
		if msg == "" {
			msg = "unknown error"
		}
		return &Event{Type: EventError, Err: errors.New(msg)}

	case "item.started":
		if ev.Item == nil {
			return nil
		}
		switch ev.Item.Type {
		case "command_execution":
			return &Event{
				Type: EventToolStart,
				Tool: ev.Item.Command,
			}
		case "mcp_tool_call":
			toolName := ev.Item.Tool
			if ev.Item.Server != "" {
				toolName = ev.Item.Server + "/" + ev.Item.Tool
			}
			return &Event{
				Type: EventToolStart,
				Tool: toolName,
			}
		}
		return nil

	case "item.completed":
		if ev.Item == nil {
			return nil
		}
		switch ev.Item.Type {
		case "command_execution":
			return &Event{
				Type: EventToolResult,
				Text: ev.Item.AggregatedOutput,
			}
		case "mcp_tool_call":
			return &Event{
				Type: EventToolResult,
				Text: ev.Item.AggregatedOutput,
			}
		case "agent_message":
			text := ev.Item.Text
			if strings.Contains(text, "<chief-done/>") {
				return &Event{Type: EventStoryDone, Text: text}
			}
			return &Event{Type: EventAssistantText, Text: text}
		case "file_change":
			return &Event{
				Type: EventToolResult,
				Tool: "file_change",
				Text: ev.Item.AggregatedOutput,
			}
		}
		return nil

	case "turn.completed":
		return parseCodexUsage(ev.Usage)

	default:
		return nil
	}
}

// parseCodexUsage normalizes a Codex turn.completed usage object into an event.
func parseCodexUsage(rawUsage json.RawMessage) *Event {
	if len(rawUsage) == 0 || string(rawUsage) == "null" {
		return nil
	}
	var raw codexUsageRaw
	if err := json.Unmarshal(rawUsage, &raw); err != nil {
		return warningEvent("codex", err)
	}
	usage, err := finalizeUsage(&Usage{
		InputTokens:     raw.InputTokens,
		OutputTokens:    raw.OutputTokens,
		CacheReadTokens: raw.CachedInputTokens,
	})
	if err != nil {
		return warningEvent("codex", err)
	}
	if usage == nil {
		return nil
	}
	return &Event{Type: EventUsage, Usage: usage}
}
