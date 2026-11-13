//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/images"
	"github.com/misarescu/frankie/internal/sandbox"
)

// M2 (docs/spec/10, D30): a toolchain image is built in-cluster from a repo's
// mise.toml on top of the platform base image, pushed to the in-cluster registry,
// pulled by the node, and reused for an unchanged mise.toml.
//
//	FRANKIE_E2E_BUILDKIT_IMAGE  default moby/buildkit:v0.35.0
var _ = Describe("Toolchain images", Ordered, Label("toolchain"), func() {
	var (
		ensurer images.Ensurer
		in      images.Inputs
		first   images.Image
	)

	BeforeAll(func() {
		httpClient, err := rest.HTTPClientFor(restConfig)
		Expect(err).NotTo(HaveOccurred())
		// The registry is cluster-internal; the API server proxies to it.
		registry := images.HTTPRegistry{
			BaseURL: strings.TrimSuffix(restConfig.Host, "/") + "/api/v1/namespaces/" + platformNS + "/services/http:frankie-registry:5000/proxy",
			Client:  httpClient,
		}
		ensurer = images.Ensurer{
			Registry: registry,
			Builder: images.KubeBuilder{
				Kube: cs,
				Config: images.BuildConfig{
					Namespace: "frankie-build", BuildKitImage: envOr("FRANKIE_E2E_BUILDKIT_IMAGE", "moby/buildkit:v0.35.0"),
					RuntimeClassName: "kata", ServiceAccountName: "frankie-build",
					PushHost:       "frankie-registry." + platformNS + ".svc:5000",
					EgressProxyURL: proxyURL, Deadline: 25 * time.Minute,
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("3Gi"),
					},
					ScratchSize: resource.MustParse("10Gi"),
				},
			},
			PullHost: "localhost:30500",
		}
		// The nonce makes the first build a real one on every run.
		in = images.Inputs{
			Base: runnerImage,
			MiseToml: []byte(fmt.Sprintf(`# e2e %d
[tools]
jq = "1.7.1"

[tasks.test]
run = "jq --version"
`, time.Now().UnixNano())),
		}
	})

	It("builds the image when the registry does not have it", func(ctx SpecContext) {
		var err error
		first, err = ensurer.Ensure(ctx, repo, in)
		Expect(err).NotTo(HaveOccurred())
		Expect(first.Built).To(BeTrue())
		Expect(first.Ref).To(HavePrefix("localhost:30500/toolchains/"))
	}, SpecTimeout(30*time.Minute))

	It("reuses the image for an unchanged mise.toml instead of rebuilding", func(ctx SpecContext) {
		second, err := ensurer.Ensure(ctx, repo, in)
		Expect(err).NotTo(HaveOccurred())
		Expect(second.Built).To(BeFalse())
		Expect(second.Ref).To(Equal(first.Ref))
	}, SpecTimeout(2*time.Minute))

	It("runs the repo's tasks from the image in a sandbox pod with no network access to the tools", func(ctx SpecContext) {
		id := domain.NewID().String()
		pod, err := sandbox.AgentPod(sandbox.PodSpec{
			Name: "e2e-toolchain-" + id[len(id)-12:], Namespace: agentsNS, Image: first.Ref,
			RuntimeClassName: "kata", ServiceAccountName: "frankie-agent",
			RunID: id, StepRunID: id, IssueKey: "e2e-1",
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			},
			WorkspaceSize:  resource.MustParse("1Gi"),
			Deadline:       5 * time.Minute,
			EgressProxyURL: proxyURL,
			// Like a gate pod: the repo's mise.toml in the workspace, tools already in the image.
			Env: map[string]string{"MISE_TOML": string(in.MiseToml)},
			Command: []string{"/bin/sh", "-c", `
mkdir -p /workspace/repo && cd /workspace/repo && printf '%s' "$MISE_TOML" > mise.toml
echo "RESULT task $(mise run test 2>&1 | tail -1)"
`},
		})
		Expect(err).NotTo(HaveOccurred())
		created, err := cs.CoreV1().Pods(agentsNS).Create(ctx, pod, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(ctx SpecContext) {
			Expect(cs.CoreV1().Pods(agentsNS).Delete(ctx, created.Name, metav1.DeleteOptions{})).To(Succeed())
		})
		Expect(probeResults(ctx, created.Name)).To(HaveKeyWithValue("task", "jq-1.7.1"))
	}, SpecTimeout(8*time.Minute))
})
