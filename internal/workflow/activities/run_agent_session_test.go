package activities

import (
	"testing"

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
