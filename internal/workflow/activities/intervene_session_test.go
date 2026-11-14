package activities

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	runnerv1 "github.com/misarescu/frankie/gen/frankie/runner/v1"
	"github.com/misarescu/frankie/internal/domain"
)

// fakeRunner records the steers and aborts it receives; finished makes it
// answer like a runner whose session already ended.
type fakeRunner struct {
	runnerv1.UnimplementedRunnerServiceServer
	mu       sync.Mutex
	steers   []string
	aborts   []string
	finished bool
}

func (f *fakeRunner) Steer(_ context.Context, req *runnerv1.SteerRequest) (*runnerv1.SteerResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.finished {
		return nil, status.Error(codes.FailedPrecondition, "session finished")
	}
	f.steers = append(f.steers, req.GetText())
	return &runnerv1.SteerResponse{}, nil
}

func (f *fakeRunner) Abort(_ context.Context, req *runnerv1.AbortRequest) (*runnerv1.AbortResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.aborts = append(f.aborts, req.GetReason())
	return &runnerv1.AbortResponse{}, nil
}

func serveFakeRunner(t *testing.T, f *fakeRunner) string {
	t.Helper()
	lis, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	runnerv1.RegisterRunnerServiceServer(srv, f)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

// docs/spec/04, "Human intervention": a steer reaches the running session, an
// abort stops it naming who and why, and both are recorded in the transcript.
func TestInterveneSession(t *testing.T) {
	st, in := readyIssue(t)
	f := &fakeRunner{}
	addr := serveFakeRunner(t, f)
	a := &Activities{Store: st, Log: quietLog(), RunnerAddr: func(AgentPod) string { return addr }}
	run := startedRun(t, a, in)
	env := activityEnv(a)
	val, err := env.ExecuteActivity(a.StartStep, StepStart{RunID: run.RunID, StepID: "implement", Attempt: 1, Kind: domain.StepAgent})
	if err != nil {
		t.Fatal(err)
	}
	var stepRunID string
	if err := val.Get(&stepRunID); err != nil {
		t.Fatal(err)
	}
	pod := AgentPod{Name: "p", IP: "10.0.0.1"}
	intervene := func(action, text string) bool {
		t.Helper()
		val, err := env.ExecuteActivity(a.InterveneSession, pod, Intervention{RunID: run.RunID, StepRunID: stepRunID, Action: action, By: "ana", Text: text})
		if err != nil {
			t.Fatal(err)
		}
		var delivered bool
		if err := val.Get(&delivered); err != nil {
			t.Fatal(err)
		}
		return delivered
	}

	if !intervene(domain.InterventionSteer, "use the existing helper") {
		t.Error("steer not delivered")
	}
	if !intervene(domain.InterventionAbort, "wrong approach") {
		t.Error("abort not delivered")
	}
	f.finished = true
	if intervene(domain.InterventionSteer, "too late") {
		t.Error("a steer to a finished session counted as delivered")
	}
	if len(f.steers) != 1 || f.steers[0] != "use the existing helper" || len(f.aborts) != 1 || f.aborts[0] != "aborted by ana: wrong approach" {
		t.Errorf("runner got steers %q, aborts %q", f.steers, f.aborts)
	}

	events, err := st.ListRunEvents(t.Context(), uuid.MustParse(run.RunID), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var got []domain.Intervention
	for _, e := range events {
		if e.Type != domain.EventIntervention {
			continue
		}
		if e.StepRunID == nil || e.StepRunID.String() != stepRunID {
			t.Errorf("intervention event on step %v", e.StepRunID)
		}
		var p domain.Intervention
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatal(err)
		}
		got = append(got, p)
	}
	want := []domain.Intervention{
		{Action: domain.InterventionSteer, By: "ana", Text: "use the existing helper", Delivered: true},
		{Action: domain.InterventionAbort, By: "ana", Text: "wrong approach", Delivered: true},
		{Action: domain.InterventionSteer, By: "ana", Text: "too late", Delivered: false},
	}
	if len(got) != len(want) {
		t.Fatalf("interventions = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("intervention %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
