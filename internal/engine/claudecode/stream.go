package claudecode

import (
	"encoding/json"

	"github.com/procuracy/procuracy/internal/audit"
	"github.com/procuracy/procuracy/internal/engine"
)

// streamEvent is the top-level shape of a Claude Code stream-json line.
// We only decode the fields we need; unknown fields are ignored.
type streamEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype,omitempty"`

	// For type=assistant — contains the Anthropic API message format
	Message *assistantMessage `json:"message,omitempty"`

	// For type=result
	TotalCostUSD float64          `json:"total_cost_usd,omitempty"`
	NumTurns     int              `json:"num_turns,omitempty"`
	DurationMS   int64            `json:"duration_ms,omitempty"`
	StopReason   string           `json:"stop_reason,omitempty"`
	ResultText   string           `json:"result,omitempty"`
	Usage        *json.RawMessage `json:"usage,omitempty"`
}

type assistantMessage struct {
	Content []contentBlock `json:"content,omitempty"`
}

type contentBlock struct {
	Type  string          `json:"type"`
	Name  string          `json:"name,omitempty"`  // for tool_use blocks
	ID    string          `json:"id,omitempty"`    // for tool_use blocks
	Input json.RawMessage `json:"input,omitempty"` // for tool_use blocks
	Text  string          `json:"text,omitempty"`  // for text blocks
}

// processEvent parses a single stream-json line and writes relevant
// audit entries. It also updates the result struct with final totals
// when the result event arrives.
func processEvent(line []byte, w *audit.Writer, result *engine.Result) {
	var ev streamEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		// Malformed JSON from claude — log it as an error but don't crash.
		w.Append(audit.Entry{
			Type:  audit.TypeError,
			Error: "failed to parse claude stream-json event",
			Details: map[string]any{
				"raw_line": string(line),
			},
		})
		return
	}

	switch ev.Type {
	case "system":
		// The init event — we already logged lifecycle/started before
		// spawning, so nothing extra needed here.

	case "assistant":
		if ev.Message == nil {
			return
		}
		// Log each tool_use block as a tool_call audit entry.
		for _, block := range ev.Message.Content {
			if block.Type != "tool_use" {
				continue
			}
			var inputMap map[string]any
			if len(block.Input) > 0 {
				json.Unmarshal(block.Input, &inputMap)
			}
			w.Append(audit.Entry{
				Type:   audit.TypeToolCall,
				Verb:   block.Name,
				Result: audit.ResultOK,
				Details: map[string]any{
					"tool_use_id": block.ID,
					"tool":        block.Name,
					"input":       inputMap,
				},
			})
		}

	case "result":
		result.TotalCost = ev.TotalCostUSD
		result.Turns = ev.NumTurns
		if ev.Subtype == "error" {
			result.Success = false
			result.Error = ev.ResultText
		}
		// Log cost as an audit entry.
		if ev.TotalCostUSD > 0 {
			w.Append(audit.Entry{
				Type:    audit.TypeCost,
				CostUSD: ev.TotalCostUSD,
				Result:  audit.ResultOK,
				Details: map[string]any{
					"total_cost_usd": ev.TotalCostUSD,
					"num_turns":      ev.NumTurns,
					"stop_reason":    ev.StopReason,
				},
			})
		}
	}
}
