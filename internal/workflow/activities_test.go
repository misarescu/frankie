package workflow

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	runnerv1 "github.com/misarescu/frankie/gen/frankie/runner/v1"
	"github.com/misarescu/frankie/internal/domain"
)

func TestPodNameIsStablePerStep(t *testing.T) {
	a, b := podName(testInput, testRun), podName(testInput, testRun)
	if a != b || a != "gc-1-implement-00000003" {
		t.Errorf("podName = %q, %q", a, b)
	}
}

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

func TestDeleteAgentPodIsIdempotent(t *testing.T) {
	kube := fake.NewClientset(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "frankie-agents"}})
	a := &Activities{Kube: kube, Agent: AgentConfig{Namespace: "frankie-agents"}}
	for i := range 2 {
		if err := a.DeleteAgentPod(t.Context(), "p"); err != nil {
			t.Fatalf("delete #%d: %v", i, err)
		}
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
