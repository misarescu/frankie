//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/sandbox"
)

// probeScript reports what an agent pod can reach, one "RESULT <name> <value>" line each.
// curl's %{http_code} is 000 when no HTTP response came back; %{http_connect} is the
// proxy's answer to CONNECT.
const probeScript = `
r() { echo "RESULT $1 $2"; }
r uid "$(id -u)"
r kernel "$(uname -r)"
r sa_token "$(test -e /var/run/secrets/kubernetes.io/serviceaccount/token && echo present || echo absent)"
r dns "$(getent hosts kubernetes.default.svc.cluster.local >/dev/null && echo ok || echo fail)"
r proxy_allowed "$(curl -sk -m 20 -o /dev/null -w '%{http_code}' https://proxy.golang.org/)"
r proxy_denied_host "$(curl -sk -m 20 -o /dev/null -w '%{http_connect}' https://example.com/)"
r proxy_denied_lan "$(curl -sk -m 20 -o /dev/null -w '%{http_connect}' https://${NODE_IP}:6443/)"
r direct_internet "$(curl --noproxy '*' -sk -m 8 -o /dev/null -w '%{http_code}' https://example.com/)"
r direct_lan "$(curl --noproxy '*' -sk -m 8 -o /dev/null -w '%{http_code}' https://${NODE_IP}:6443/)"
r direct_k8s_api "$(curl --noproxy '*' -sk -m 8 -o /dev/null -w '%{http_code}' https://kubernetes.default.svc/)"
`

// M1 acceptance (docs/spec/10) and FR-SB-1/2/5: an agent pod runs in a Kata VM
// without cluster credentials and reaches the internet only through the egress proxy.
var _ = Describe("Agent sandbox", Ordered, Label("sandbox"), func() {
	var (
		results    map[string]string
		hostKernel string
	)

	BeforeAll(func(ctx SpecContext) {
		nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(nodes.Items).NotTo(BeEmpty())
		node := nodes.Items[0]
		hostKernel = node.Status.NodeInfo.KernelVersion
		var nodeIP string
		for _, a := range node.Status.Addresses {
			if a.Type == corev1.NodeInternalIP {
				nodeIP = a.Address
			}
		}

		id := domain.NewID().String()
		pod, err := sandbox.AgentPod(sandbox.PodSpec{
			Name: "e2e-sandbox-" + id[len(id)-12:], Namespace: agentsNS, Image: runnerImage,
			RuntimeClassName: "kata", ServiceAccountName: "frankie-agent",
			RunID: id, StepRunID: id, IssueKey: "e2e-1",
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			},
			WorkspaceSize:  resource.MustParse("1Gi"),
			Deadline:       5 * time.Minute,
			EgressProxyURL: proxyURL,
			Env:            map[string]string{"NODE_IP": nodeIP},
			Command:        []string{"/bin/sh", "-c", probeScript},
		})
		Expect(err).NotTo(HaveOccurred())
		created, err := cs.CoreV1().Pods(agentsNS).Create(ctx, pod, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), "create probe pod")
		DeferCleanup(func(ctx SpecContext) {
			Expect(cs.CoreV1().Pods(agentsNS).Delete(ctx, created.Name, metav1.DeleteOptions{})).To(Succeed())
		})

		results = probeResults(ctx, created.Name)
		AddReportEntry("probe results", results)
	}, NodeTimeout(6*time.Minute))

	It("runs as the non-root agent user", func() {
		Expect(results).To(HaveKeyWithValue("uid", "10001"))
	})
	It("runs in a Kata VM, not on the host kernel", func() {
		Expect(results["kernel"]).NotTo(BeEmpty())
		Expect(results["kernel"]).NotTo(Equal(hostKernel))
	})
	It("has no service account token (FR-SB-5)", func() {
		Expect(results).To(HaveKeyWithValue("sa_token", "absent"))
	})
	It("resolves cluster DNS", func() {
		Expect(results).To(HaveKeyWithValue("dns", "ok"))
	})
	It("reaches an allowlisted host through the egress proxy", func() {
		Expect(results["proxy_allowed"]).NotTo(BeElementOf("", "000"))
	})
	It("is refused by the proxy for unlisted hosts", func() {
		Expect(results).To(HaveKeyWithValue("proxy_denied_host", "403"))
	})
	It("is refused by the proxy for the node and LAN", func() {
		Expect(results).To(HaveKeyWithValue("proxy_denied_lan", "403"))
	})
	It("cannot reach the internet directly", func() {
		Expect(results).To(HaveKeyWithValue("direct_internet", "000"))
	})
	It("cannot reach the LAN directly", func() {
		Expect(results).To(HaveKeyWithValue("direct_lan", "000"))
	})
	It("cannot reach the Kubernetes API", func() {
		Expect(results).To(HaveKeyWithValue("direct_k8s_api", "000"))
	})
})

// probeResults waits for the probe pod to finish and parses its RESULT lines.
func probeResults(ctx context.Context, name string) map[string]string {
	Eventually(func(g Gomega) corev1.PodPhase {
		p, err := cs.CoreV1().Pods(agentsNS).Get(ctx, name, metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		for _, s := range p.Status.ContainerStatuses {
			if w := s.State.Waiting; w != nil && (w.Reason == "ImagePullBackOff" || w.Reason == "ErrImagePull" || w.Reason == "CreateContainerConfigError") {
				StopTrying(fmt.Sprintf("probe pod cannot start: %s: %s", w.Reason, w.Message)).Now()
			}
		}
		return p.Status.Phase
	}).WithContext(ctx).WithTimeout(4*time.Minute).WithPolling(2*time.Second).
		Should(BeElementOf(corev1.PodSucceeded, corev1.PodFailed), "probe pod did not finish")

	raw, err := cs.CoreV1().Pods(agentsNS).GetLogs(name, &corev1.PodLogOptions{}).Do(ctx).Raw()
	Expect(err).NotTo(HaveOccurred())
	results := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(string(raw)))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) >= 2 && f[0] == "RESULT" {
			results[f[1]] = strings.Join(f[2:], " ")
		}
	}
	Expect(results).NotTo(BeEmpty(), "no probe results in logs:\n%s", raw)
	return results
}

// The agents namespace enforces Pod Security "restricted": a privileged pod is
// refused by the API server even if something other than the builder creates it.
var _ = Describe("Agents namespace", Label("sandbox"), func() {
	It("refuses privileged pods", func(ctx SpecContext) {
		privileged := true
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("e2e-privileged-%d", time.Now().Unix()), Namespace: agentsNS},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name: "x", Image: "busybox", SecurityContext: &corev1.SecurityContext{Privileged: &privileged},
			}}},
		}
		_, err := cs.CoreV1().Pods(agentsNS).Create(ctx, pod, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}})
		Expect(apierrors.IsForbidden(err)).To(BeTrue(), "want Forbidden, got %v", err)
		Expect(err.Error()).To(ContainSubstring("restricted"))
	})
})
