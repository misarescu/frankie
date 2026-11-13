package activities

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestDeleteAgentPodIsIdempotent(t *testing.T) {
	kube := fake.NewClientset(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "frankie-agents"}})
	a := &Activities{Kube: kube, Agent: AgentConfig{Namespace: "frankie-agents"}}
	for i := range 2 {
		if err := a.DeleteAgentPod(t.Context(), "p"); err != nil {
			t.Fatalf("delete #%d: %v", i, err)
		}
	}
}
