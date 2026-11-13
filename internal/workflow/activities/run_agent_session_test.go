package activities

import (
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	runnerv1 "github.com/misarescu/frankie/gen/frankie/runner/v1"
	"github.com/misarescu/frankie/internal/domain"
)

func TestEveryRunnerEventTypeMaps(t *testing.T) {
	for v := range runnerv1.EventType_name {
		typ := runnerv1.EventType(v)
		if typ == runnerv1.EventType_EVENT_TYPE_UNSPECIFIED {
			continue
		}
		if _, ok := domainEventTypes[typ]; !ok {
			t.Errorf("%s has no domain event type", typ)
		}
	}
	if domainEventType(runnerv1.EventType_EVENT_TYPE_UNSPECIFIED) != domain.EventError {
		t.Error("unknown types should be recorded as errors")
	}
}

func TestIsPrecondition(t *testing.T) {
	already := status.Error(codes.FailedPrecondition, "a session was already started on this runner")
	prepare := status.Error(codes.FailedPrecondition, "prepare workspace: workspace: git clone: exit status 128")
	if !isPrecondition(already, "already started") {
		t.Error("an earlier attempt's session must count as started")
	}
	if isPrecondition(prepare, "already started") {
		t.Error("a failed workspace clone must not be mistaken for an earlier attempt")
	}
	if isPrecondition(status.Error(codes.Internal, "already started"), "already started") {
		t.Error("only FailedPrecondition counts")
	}
}

// A reviewer's session can read the issue branch but never push it: its token
// grant and its workspace are both read-only, and its tools are the role's.
func TestReviewerSessionIsReadOnly(t *testing.T) {
	a := &Activities{Agent: AgentConfig{Model: "claude-sonnet-5-5", SessionTimeout: time.Hour, GitProxyURL: "http://cp:8081/",
		GatewayURL: "http://gw:4000", AuthorName: "frankie", AuthorEmail: "agent@frankie.invalid"}}
	start := StartPoint{CloneRef: "agent/gc-1/add-search", Continuing: true}

	reviewer := testRun
	reviewer.Step = &AgentStep{StepID: "code-reviewer", SystemPrompt: "review", Contract: "review-verdict",
		AllowedTools: []string{"read", "bash:readonly"}, Model: "claude-opus-5-5"}
	set := a.sessionSettings(reviewer)
	if g := a.sessionGrant(testInput, reviewer, set); !g.ReadOnly || g.IssueKey != "gc-1" {
		t.Errorf("reviewer grant = %+v", g)
	}
	req := a.startRequest(testInput, reviewer, set, start, "tok", "vkey")
	w := req.GetWorkspace()
	if !w.GetReadOnly() || w.GetCompareRef() != "main" || w.GetBaseRef() != "agent/gc-1/add-search" ||
		!slices.Equal(req.GetAllowedTools(), []string{"read", "bash:readonly"}) || req.GetModel() != "claude-opus-5-5" || req.GetSystemPrompt() != "review" {
		t.Errorf("reviewer start = %+v", req)
	}

	coder := testRun
	coder.Step = &AgentStep{StepID: "implement", SystemPrompt: "code"}
	set = a.sessionSettings(coder)
	if g := a.sessionGrant(testInput, coder, set); g.ReadOnly {
		t.Errorf("coder grant = %+v", g)
	}
	req = a.startRequest(testInput, coder, set, start, "tok", "vkey")
	if req.GetWorkspace().GetReadOnly() || len(req.GetAllowedTools()) != 0 || req.GetModel() != "claude-sonnet-5-5" {
		t.Errorf("coder start = %+v", req)
	}

	// IssueRun's step keeps the M2 settings.
	if set := a.sessionSettings(testRun); set.readOnly || set.systemPrompt != SystemPrompt() || set.timeout != time.Hour {
		t.Errorf("IssueRun settings = %+v", set)
	}
}
