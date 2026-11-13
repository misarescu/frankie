//go:build e2e

package e2e

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/sandbox"
)

// D37: sandboxes get Go modules only from the in-cluster module proxy; the
// public Go proxy and the Cloud Storage it redirects to stay unreachable.
//
//	FRANKIE_E2E_GOPROXY  default http://frankie-athens.<platform namespace>.svc:3000
var _ = Describe("Go module proxy", Label("gomodules"), func() {
	It("serves modules to agent pods, which cannot reach the public Go proxy", func(ctx SpecContext) {
		goproxy := envOr("FRANKIE_E2E_GOPROXY", "http://frankie-athens."+platformNS+".svc:3000")
		id := domain.NewID().String()
		pod, err := sandbox.AgentPod(sandbox.PodSpec{
			Name: "e2e-gomodules-" + id[len(id)-12:], Namespace: agentsNS, Image: runnerImage,
			RuntimeClassName: "kata", ServiceAccountName: "frankie-agent",
			RunID: id, StepRunID: id, IssueKey: "e2e-1",
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			},
			WorkspaceSize:  resource.MustParse("1Gi"),
			Deadline:       5 * time.Minute,
			EgressProxyURL: proxyURL,
			Env:            map[string]string{"GOPROXY": goproxy},
			Command: []string{"/bin/sh", "-c", `
r() { echo "RESULT $1 $2"; }
r athens_list "$(curl -s -m 60 -o /dev/null -w '%{http_code}' "$GOPROXY/github.com/pkg/errors/@v/list")"
r athens_zip "$(curl -s -m 120 -o /tmp/m.zip -w '%{http_code}' "$GOPROXY/github.com/pkg/errors/@v/v0.9.1.zip") $(head -c 2 /tmp/m.zip)"
r public_proxy "$(curl -s -m 20 -o /dev/null -w '%{http_connect}' https://proxy.golang.org/github.com/pkg/errors/@v/list)"
r cloud_storage "$(curl -s -m 20 -o /dev/null -w '%{http_connect}' https://storage.googleapis.com/)"
r sumdb "$(curl -s -m 20 -o /dev/null -w '%{http_code}' https://sum.golang.org/lookup/github.com/pkg/errors@v0.9.1)"
`},
		})
		Expect(err).NotTo(HaveOccurred())
		created, err := cs.CoreV1().Pods(agentsNS).Create(ctx, pod, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(ctx SpecContext) {
			Expect(cs.CoreV1().Pods(agentsNS).Delete(ctx, created.Name, metav1.DeleteOptions{})).To(Succeed())
		})
		results := probeResults(ctx, created.Name)
		AddReportEntry("probe results", results)

		Expect(results).To(HaveKeyWithValue("athens_list", "200"))
		Expect(results).To(HaveKeyWithValue("athens_zip", "200 PK"), "Athens must serve the module zip")
		Expect(results).To(HaveKeyWithValue("public_proxy", "403"), "proxy.golang.org must be refused by the egress proxy")
		Expect(results).To(HaveKeyWithValue("cloud_storage", "403"), "storage.googleapis.com must be refused by the egress proxy")
		Expect(results).To(HaveKeyWithValue("sumdb", "200"), "the checksum database stays reachable")
	}, NodeTimeout(7*time.Minute))
})
