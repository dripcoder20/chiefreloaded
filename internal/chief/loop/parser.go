package loop

import (
	"encoding/json"
	"fmt"
	"strings"
)

// EventType represents the type of event parsed from Claude's stream-json output.
type EventType int

const (
	// EventUnknown represents an unrecognized event type.
	EventUnknown EventType = iota
	// EventIterationStart is emitted at the start of a Claude iteration (system init).
	EventIterationStart
	// EventAssistantText is emitted when Claude outputs text.
	EventAssistantText
	// EventToolStart is emitted when Claude invokes a tool.
	EventToolStart
	// EventToolResult is emitted when a tool returns a result.
	EventToolResult
	// EventStoryDone is emitted when Claude signals a story is done via <chief-done/>.
	EventStoryDone
	// EventComplete is emitted when all stories are complete (buildPrompt returns error).
	EventComplete
	// EventMaxIterationsReached is emitted when max iterations are reached.
	EventMaxIterationsReached
	// EventError is emitted when an error occurs.
	EventError
	// EventRetrying is emitted when retrying after a crash.
	EventRetrying
	// EventWatchdogTimeout is emitted when the watchdog kills a hung process.
	EventWatchdogTimeout
	// EventUsage is emitted when a provider reports normalized usage for a payload.
	EventUsage
	// EventWarning is emitted when a payload cannot be parsed but the run continues.
	EventWarning
)

// String returns the string representation of an EventType.
func (e EventType) String() string {
	switch e {
	case EventIterationStart:
		return "IterationStart"
	case EventAssistantText:
		return "AssistantText"
	case EventToolStart:
		return "ToolStart"
	case EventToolResult:
		return "ToolResult"
	case EventStoryDone:
		return "StoryDone"
	case EventComplete:
		return "Complete"
	case EventMaxIterationsReached:
		return "MaxIterationsReached"
	case EventError:
		return "Error"
	case EventRetrying:
		return "Retrying"
	case EventWatchdogTimeout:
		return "WatchdogTimeout"
	case EventUsage:
		return "Usage"
	case EventWarning:
		return "Warning"
	default:
		return "Unknown"
	}
}

// Event represents a parsed event from Claude's stream-json output.
type Event struct {
	Type       EventType
	Iteration  int
	Text       string
	Tool       string
	ToolInput  map[string]interface{}
	StoryID    string
	Err        error
	RetryCount int    // Current retry attempt (1-based)
	RetryMax   int    // Maximum retries allowed
	Usage      *Usage // Normalized provider usage, set on EventUsage
}

// streamMessage represents the top-level structure of a stream-json line.
type streamMessage struct {
	Type    string          `json:"type"`
	Subtype string          `json:"subtype,omitempty"`
	Message json.RawMessage `json:"message,omitempty"`
}

// assistantMessage represents the structure of an assistant message.
type assistantMessage struct {
	Content []contentBlock `json:"content"`
}

// contentBlock represents a block of content in an assistant message.
type contentBlock struct {
	Type  string                 `json:"type"`
	Text  string                 `json:"text,omitempty"`
	ID    string                 `json:"id,omitempty"`
	Name  string                 `json:"name,omitempty"`
	Input map[string]interface{} `json:"input,omitempty"`
}

// userMessage represents a tool result message.
type userMessage struct {
	Content []toolResultBlock `json:"content"`
}

// toolResultBlock represents a tool result in a user message.
type toolResultBlock struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}

// ParseLine parses a single line of stream-json output and returns an Event.
// If the line cannot be parsed or is not relevant, it returns nil.
func ParseLine(line string) *Event {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}

	var msg streamMessage
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return nil
	}

	switch msg.Type {
	case "system":
		if msg.Subtype == "init" {
			return &Event{Type: EventIterationStart}
		}
		return nil

	case "assistant":
		return attachClaudeUsage(parseAssistantMessage(msg.Message), msg.Message)

	case "user":
		return parseUserMessage(msg.Message)

	case "result":
		return parseClaudeResult(line)

	default:
		return nil
	}
}

// claudeUsageRaw mirrors the token fields Claude reports in a usage object.
// Pointer fields keep a missing field distinct from a reported value of 0.
type claudeUsageRaw struct {
	InputTokens              *int64 `json:"input_tokens"`
	OutputTokens             *int64 `json:"output_tokens"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
}

// claudeMessageMeta carries the usage and model an assistant message reports
// alongside its content blocks.
type claudeMessageMeta struct {
	Model string          `json:"model"`
	Usage json.RawMessage `json:"usage"`
}

// claudeResultMeta carries the total cost and usage a final result line reports.
type claudeResultMeta struct {
	TotalCostUSD *float64        `json:"total_cost_usd"`
	Model        string          `json:"model"`
	Usage        json.RawMessage `json:"usage"`
}

// warningEvent builds a diagnosable warning event for a malformed usage payload
// so the run continues instead of terminating.
func warningEvent(provider string, err error) *Event {
	return &Event{
		Type: EventWarning,
		Text: fmt.Sprintf("%s: malformed usage payload: %v", provider, err),
	}
}

// claudeUsageFrom normalizes a Claude-shaped usage object plus model and cost.
// It returns an error only when the usage object is present but malformed.
func claudeUsageFrom(rawUsage json.RawMessage, model string, cost *float64) (*Usage, error) {
	u := &Usage{Model: model, ReportedCost: cost}
	if len(rawUsage) > 0 && string(rawUsage) != "null" {
		var raw claudeUsageRaw
		if err := json.Unmarshal(rawUsage, &raw); err != nil {
			return nil, err
		}
		u.InputTokens = raw.InputTokens
		u.OutputTokens = raw.OutputTokens
		u.CacheReadTokens = raw.CacheReadInputTokens
		u.CacheWriteTokens = raw.CacheCreationInputTokens
	}
	return finalizeUsage(u)
}

// attachClaudeUsage extracts usage from an assistant message and attaches it to
// the produced content event, emitting a standalone usage event when the message
// carried usage but no content.
func attachClaudeUsage(ev *Event, raw json.RawMessage) *Event {
	if len(raw) == 0 {
		return ev
	}
	var meta claudeMessageMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return ev
	}
	usage, err := claudeUsageFrom(meta.Usage, meta.Model, nil)
	if err != nil {
		return warningEvent("claude", err)
	}
	if usage == nil {
		return ev
	}
	if ev == nil {
		return &Event{Type: EventUsage, Usage: usage}
	}
	ev.Usage = usage
	return ev
}

// parseClaudeResult extracts usage and cost from a final result line.
func parseClaudeResult(line string) *Event {
	var meta claudeResultMeta
	if err := json.Unmarshal([]byte(line), &meta); err != nil {
		return nil
	}
	usage, err := claudeUsageFrom(meta.Usage, meta.Model, meta.TotalCostUSD)
	if err != nil {
		return warningEvent("claude", err)
	}
	if usage == nil {
		return nil
	}
	return &Event{Type: EventUsage, Usage: usage}
}

// parseAssistantMessage parses an assistant message and returns appropriate events.
func parseAssistantMessage(raw json.RawMessage) *Event {
	if raw == nil {
		return nil
	}

	var msg assistantMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil
	}

	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			text := block.Text
			// Check for <chief-done/> tag
			if strings.Contains(text, "<chief-done/>") {
				return &Event{
					Type: EventStoryDone,
					Text: text,
				}
			}
			return &Event{
				Type: EventAssistantText,
				Text: text,
			}

		case "tool_use":
			return &Event{
				Type:      EventToolStart,
				Tool:      block.Name,
				ToolInput: block.Input,
			}
		}
	}

	return nil
}

// parseUserMessage parses a user message (typically tool results).
func parseUserMessage(raw json.RawMessage) *Event {
	if raw == nil {
		return nil
	}

	var msg userMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil
	}

	for _, block := range msg.Content {
		if block.Type == "tool_result" {
			return &Event{
				Type: EventToolResult,
				Text: block.Content,
			}
		}
	}

	return nil
}
