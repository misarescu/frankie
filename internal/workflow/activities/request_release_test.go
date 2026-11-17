package activities

import (
	"context"
	"testing"

	"go.temporal.io/sdk/client"
)

// startCall is one signal-with-start a fakeStarter received.
type startCall struct {
	id, signal string
	arg        any
	opts       client.StartWorkflowOptions
	workflow   any
	input      any
}

type fakeStarter struct{ calls []startCall }

func (s *fakeStarter) SignalWithStartWorkflow(_ context.Context, id, signal string, arg any, opts client.StartWorkflowOptions, wf any, args ...any) (client.WorkflowRun, error) {
	s.calls = append(s.calls, startCall{id, signal, arg, opts, wf, args[0]})
	return nil, nil
}

// M7, D114: a merge from the queue asks its repo's release workflow for a
// release of the merge commit; a retried merge asks again.
func TestMergeQueuedRequestsRelease(t *testing.T) {
	f := newQueueActivityFixture(t)
	starter := &fakeStarter{}
	f.a.Queue, f.a.TaskQueue = starter, "frankie"
	for range 2 {
		if out, _ := f.merge(t, queuedBase); out.Outcome != QueueMerged {
			t.Fatalf("merge = %+v", out)
		}
	}
	if len(starter.calls) != 2 {
		t.Fatalf("calls = %+v, want one per merge attempt", starter.calls)
	}
	c := starter.calls[0]
	if c.id != "release/misarescu/Goodreads-Clone" || c.signal != ReleaseRequestSignal || c.arg != (ReleaseRequest{SHA: "m3rge", BaseBranch: "main"}) ||
		c.opts.ID != c.id || c.opts.TaskQueue != "frankie" || c.workflow != "Release" || c.input.(ReleaseInput).Repo != "misarescu/Goodreads-Clone" {
		t.Errorf("call = %+v", c)
	}
}
