package runnertest

import (
	"fmt"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/runner"
)

// checkStream is the suite's own oracle; it must reject broken streams.
func TestCheckStreamRejectsBrokenStreams(t *testing.T) {
	done := Event{Type: domain.EventDone, Payload: runner.Done{Status: runner.DoneSucceeded}}
	call := Event{Type: domain.EventToolCall, Payload: runner.ToolCall{ID: "c1", Name: "bash"}}
	tests := map[string][]Event{
		"no done":                 {call},
		"two done events":         {done, done},
		"done not last":           {done, call},
		"wrong payload":           {{Type: domain.EventToolCall, Payload: runner.AssistantMessage{Text: "x"}}, done},
		"unknown type":            {{Type: "telepathy", Payload: nil}, done},
		"output for unknown call": {{Type: domain.EventCommandOutput, Payload: runner.CommandOutput{ToolCallID: "nope"}}, done},
		"usage going down": {
			{Type: domain.EventTokenUsage, Payload: runner.TokenUsage{InputTokens: 10, Turns: 1}},
			{Type: domain.EventTokenUsage, Payload: runner.TokenUsage{InputTokens: 5, Turns: 1}}, done},
	}
	for name, events := range tests {
		t.Run(name, func(t *testing.T) {
			var r collected
			checkStream(&r, events)
			if len(r) == 0 {
				t.Errorf("accepted %+v", events)
			}
		})
	}
	var ok collected
	checkStream(&ok, []Event{call, {Type: domain.EventCommandOutput, Payload: runner.CommandOutput{ToolCallID: "c1"}},
		{Type: domain.EventTokenUsage, Payload: runner.TokenUsage{InputTokens: 1}}, done})
	if len(ok) > 0 {
		t.Errorf("rejected a valid stream: %v", ok)
	}
}

// collected records what checkStream reports.
type collected []string

func (c *collected) Helper() {}

func (c *collected) Errorf(format string, args ...any) { *c = append(*c, fmt.Sprintf(format, args...)) }
