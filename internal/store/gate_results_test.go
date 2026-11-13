package store

import (
	"encoding/json"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
)

func TestRecordGateResult(t *testing.T) {
	s := newTestStore(t)
	is := mustIssue(t, s, mustProject(t, s).ID, "running")
	run := mustRun(t, s, is.ID)
	step, err := s.CreateStepRun(t.Context(), NewStepRun{RunID: run.ID, StepID: "checks", Attempt: 1, Kind: domain.StepGate})
	if err != nil {
		t.Fatal(err)
	}
	in := NewGateResult{StepRunID: step.ID, Type: domain.GateCommand, Policy: domain.GateBlocking, Verdict: domain.GateFail,
		Details: json.RawMessage(`{"commands":[{"command":"mise run test","exitCode":1}]}`)}

	first, err := s.RecordGateResult(t.Context(), in)
	if err != nil {
		t.Fatalf("RecordGateResult: %v", err)
	}
	if first.Verdict != domain.GateFail || first.Type != domain.GateCommand || first.StepRunID != step.ID {
		t.Errorf("result = %+v", first)
	}

	t.Run("a retry records once and emits one event", func(t *testing.T) {
		again, err := s.RecordGateResult(t.Context(), in)
		if err != nil || again.ID != first.ID {
			t.Fatalf("again = %+v, %v; want the same row", again, err)
		}
		events, err := s.ListRunEvents(t.Context(), run.ID, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, e := range events {
			if e.Type == domain.EventGateResult {
				n++
				var p map[string]any
				_ = json.Unmarshal(e.Payload, &p)
				if p["verdict"] != "fail" || p["gate"] != "checks" {
					t.Errorf("event payload = %s", e.Payload)
				}
			}
		}
		if n != 1 {
			t.Errorf("gate_result events = %d, want 1", n)
		}
	})

	t.Run("listed per run", func(t *testing.T) {
		list, err := s.ListGateResultsForRun(t.Context(), run.ID)
		if err != nil || len(list) != 1 || list[0].ID != first.ID {
			t.Fatalf("list = %+v, %v", list, err)
		}
	})

	t.Run("rejects", func(t *testing.T) {
		for name, bad := range map[string]NewGateResult{
			"unknown type":    {StepRunID: step.ID, Type: "lint", Policy: domain.GateBlocking, Verdict: domain.GatePass},
			"unknown verdict": {StepRunID: step.ID, Type: domain.GateCommand, Policy: domain.GateBlocking, Verdict: "ok"},
			"details array":   {StepRunID: step.ID, Type: domain.GateCommand, Policy: domain.GateBlocking, Verdict: domain.GatePass, Details: json.RawMessage(`[]`)},
			"no step run":     {StepRunID: domain.NewID(), Type: domain.GateCommand, Policy: domain.GateBlocking, Verdict: domain.GatePass},
		} {
			if _, err := s.RecordGateResult(t.Context(), bad); err == nil {
				t.Errorf("%s: accepted", name)
			}
		}
	})
}
