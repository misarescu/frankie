//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/gateway"
	"github.com/misarescu/frankie/internal/sandbox"
)

const gatewayModel = "claude-haiku-5-5"

// gatewayProbeScript calls the gateway like Claude Code does (Anthropic messages
// API) and checks that keys and models are enforced.
const gatewayProbeScript = `
r() { echo "RESULT $1 $2"; }
msg() { curl -s -m 90 -o /tmp/out.json -w '%{http_code}' "${GW_URL}/v1/messages" \
  -H "x-api-key: $1" -H 'anthropic-version: 2023-06-01' -H 'content-type: application/json' \
  -d "{\"model\":\"$2\",\"max_tokens\":8,\"messages\":[{\"role\":\"user\",\"content\":\"Reply with OK\"}]}"; }
r messages "$(msg "$VKEY" "$MODEL")"
r forged_key "$(msg sk-forged "$MODEL")"
r other_model "$(msg "$VKEY" claude-opus-5-5)"
r direct_provider "$(curl -sk -m 20 -o /dev/null -w '%{http_connect}' https://api.anthropic.com/)"
`

// FR-SB-4 / invariant 1: agents reach models only through the gateway, with a
// per-run virtual key limited by budget and model; the provider key stays there.
var _ = Describe("LLM gateway", Ordered, ContinueOnFailure, Label("gateway"), func() {
	var results map[string]string

	BeforeAll(func(ctx SpecContext) {
		secret, err := cs.CoreV1().Secrets(platformNS).Get(ctx, "frankie-llm-gateway", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "read the gateway master key")
		local := forwardToComponent(ctx, "llm-gateway", 4000)
		gw, err := gateway.New("http://127.0.0.1:"+strconv.Itoa(local), string(secret.Data["master-key"]))
		Expect(err).NotTo(HaveOccurred())

		id := domain.NewID().String()
		vkey, err := gw.CreateKey(ctx, gateway.KeyRequest{RunID: "e2e-" + id, Models: []string{gatewayModel}, MaxBudgetUSD: 0.05, TTL: 15 * time.Minute})
		Expect(err).NotTo(HaveOccurred(), "mint a virtual key")
		DeferCleanup(func(ctx SpecContext) { Expect(gw.DeleteKey(ctx, vkey)).To(Succeed()) })

		pod, err := sandbox.AgentPod(sandbox.PodSpec{
			Name: "e2e-gateway-" + id[len(id)-12:], Namespace: agentsNS, Image: runnerImage,
			RuntimeClassName: "kata", ServiceAccountName: "frankie-agent",
			RunID: id, StepRunID: id, IssueKey: "e2e-1",
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			},
			WorkspaceSize:  resource.MustParse("1Gi"),
			Deadline:       5 * time.Minute,
			EgressProxyURL: proxyURL,
			Env: map[string]string{
				"GW_URL": "http://frankie-llm-gateway." + platformNS + ".svc:4000",
				"VKEY":   vkey, "MODEL": gatewayModel,
			},
			Command: []string{"/bin/sh", "-c", gatewayProbeScript},
		})
		Expect(err).NotTo(HaveOccurred())
		created, err := cs.CoreV1().Pods(agentsNS).Create(ctx, pod, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(ctx SpecContext) {
			Expect(cs.CoreV1().Pods(agentsNS).Delete(ctx, created.Name, metav1.DeleteOptions{})).To(Succeed())
		})
		results = probeResults(ctx, created.Name)
		AddReportEntry("gateway probe results", results)
	}, NodeTimeout(8*time.Minute))

	It("serves the run's model with its virtual key", func() {
		Expect(results).To(HaveKeyWithValue("messages", "200"))
	})
	It("rejects a forged key", func() {
		Expect(results["forged_key"]).To(BeElementOf("401", "403"))
	})
	It("rejects models the key was not granted", func() {
		Expect(results["other_model"]).To(BeElementOf("401", "403"))
	})
	It("keeps agents away from the provider API itself", func() {
		Expect(results).To(HaveKeyWithValue("direct_provider", "403"))
	})
})

// forwardToComponent port-forwards a local port to a ready pod of a platform
// component, like kubectl port-forward, and returns the local port.
func forwardToComponent(ctx context.Context, component string, port int) int {
	pods, err := cs.CoreV1().Pods(platformNS).List(ctx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/component=" + component})
	Expect(err).NotTo(HaveOccurred())
	var name string
	for _, p := range pods.Items {
		if p.Status.Phase == corev1.PodRunning {
			name = p.Name
		}
	}
	Expect(name).NotTo(BeEmpty(), "no running %s pod", component)

	transport, upgrader, err := spdy.RoundTripperFor(restConfig)
	Expect(err).NotTo(HaveOccurred())
	url := cs.CoreV1().RESTClient().Post().Resource("pods").Namespace(platformNS).Name(name).SubResource("portforward").URL()
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, url)
	stop, ready := make(chan struct{}), make(chan struct{})
	fw, err := portforward.NewOnAddresses(dialer, []string{"127.0.0.1"}, []string{fmt.Sprintf("0:%d", port)}, stop, ready, io.Discard, GinkgoWriter)
	Expect(err).NotTo(HaveOccurred())
	go func() { _ = fw.ForwardPorts() }()
	Eventually(ready).WithTimeout(30 * time.Second).Should(BeClosed())
	DeferCleanup(func() { close(stop) })
	ports, err := fw.GetPorts()
	Expect(err).NotTo(HaveOccurred())
	return int(ports[0].Local)
}
