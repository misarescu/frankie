package runnertest

import (
	"testing"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/runner"
)

func TestRecorder(t *testing.T) {
	var r Recorder
	r.Emit(domain.EventAssistantMessage, runner.AssistantMessage{Text: "hi"})
	r.Emit(domain.EventDone, runner.Done{Status: runner.DoneSucceeded})
	if len(r.All()) != 2 || r.Done(t).Status != runner.DoneSucceeded {
		t.Errorf("events = %+v", r.All())
	}
}
