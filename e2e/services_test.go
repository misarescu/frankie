//go:build e2e

package e2e

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/sandbox"
)

// FR-SB-9 (M2): a service declared in .factory/config.yaml runs as a sidecar of
// a Kata sandbox pod under Pod Security "restricted", and the step's container
// starts only once it accepts connections on localhost.
var _ = Describe("Sidecar services", Label("services"), func() {
	It("runs postgres from the catalog next to the step", func(ctx SpecContext) {
		p, err := config.ParseProject([]byte("services: [postgres:17]\n"), 1)
		Expect(err).NotTo(HaveOccurred())
		var services []sandbox.Service
		for _, s := range p.Services {
			services = append(services, sandbox.Service{Name: s.Name, Image: s.Image, Port: s.Port, Env: s.Env,
				RunAsUser: s.RunAsUser, DataDir: s.DataDir, Limits: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("500m"), corev1.ResourceMemory: resource.MustParse("512Mi")}})
		}

		id := domain.NewID().String()
		pod, err := sandbox.AgentPod(sandbox.PodSpec{
			Name: "e2e-services-" + id[len(id)-12:], Namespace: agentsNS, Image: runnerImage,
			RuntimeClassName: "kata", ServiceAccountName: "frankie-agent",
			RunID: id, StepRunID: id, IssueKey: "e2e-1",
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
			WorkspaceSize:  resource.MustParse("1Gi"),
			Deadline:       6 * time.Minute,
			EgressProxyURL: proxyURL,
			Services:       services,
			// The step starts after the startup probe passed, so the port must be open now.
			Command: []string{"/bin/bash", "-c", `
echo "RESULT postgres_port $(timeout 5 bash -c 'echo > /dev/tcp/127.0.0.1/5432' 2>/dev/null && echo open || echo closed)"
`},
		})
		Expect(err).NotTo(HaveOccurred())
		created, err := cs.CoreV1().Pods(agentsNS).Create(ctx, pod, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), "the restricted namespace must admit the pod with its sidecar")
		DeferCleanup(func(ctx SpecContext) {
			Expect(cs.CoreV1().Pods(agentsNS).Delete(ctx, created.Name, metav1.DeleteOptions{})).To(Succeed())
		})

		results := probeResults(ctx, created.Name)
		Expect(results).To(HaveKeyWithValue("postgres_port", "open"))

		p2, err := cs.CoreV1().Pods(agentsNS).Get(ctx, created.Name, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(p2.Status.InitContainerStatuses).NotTo(BeEmpty())
		Expect(p2.Status.InitContainerStatuses[0].Name).To(Equal("svc-postgres"))
		Expect(p2.Status.InitContainerStatuses[0].RestartCount).To(BeZero(), "postgres crashed and restarted")
	}, NodeTimeout(8*time.Minute))
})
