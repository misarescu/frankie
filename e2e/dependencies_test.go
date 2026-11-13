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

// M3.1 done-when: a non-Go project gets a working dependency image from its
// own declaration. A Python toy: the toolchain pins Python, the dependency
// image warms requirements.txt with pip, and a sandbox pod running the image
// by digest imports the package without installing anything.
var _ = Describe("Dependency images", Ordered, Label("dependencies"), func() {
	var (
		ensurer   images.Ensurer
		toolchain images.Image
		deps      images.Dependencies
		first     images.Image
	)

	BeforeAll(func() {
		httpClient, err := rest.HTTPClientFor(restConfig)
		Expect(err).NotTo(HaveOccurred())
		ensurer = images.Ensurer{
			Registry: images.HTTPRegistry{
				BaseURL: strings.TrimSuffix(restConfig.Host, "/") + "/api/v1/namespaces/" + platformNS + "/services/http:frankie-registry:5000/proxy",
				Client:  httpClient,
			},
			Digests: memDigests{},
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
	})

	const miseToml = "[tools]\npython = \"3.13\"\n"

	It("builds the toolchain image", func(ctx SpecContext) {
		repository, err := images.Repository(repo)
		Expect(err).NotTo(HaveOccurred())
		toolchain, err = ensurer.Ensure(ctx, repository, images.Inputs{Base: runnerImage, MiseToml: []byte(miseToml)})
		Expect(err).NotTo(HaveOccurred())
	}, SpecTimeout(30*time.Minute))

	It("builds a dependency image FROM it with the repo's own warm-up", func(ctx SpecContext) {
		// The nonce makes the build a real one on every run.
		deps = images.Dependencies{
			Toolchain: toolchain,
			// The control plane adds mise.toml to every dependency image, so
			// the toolchain's python shim resolves (here added by hand).
			Files: map[string][]byte{
				"mise.toml":            []byte(miseToml),
				"requirements.txt":     []byte("six==1.17.0\n"),
				"tools/nonce.txt":      []byte(fmt.Sprint(time.Now().UnixNano())),
				"tools/warm/README.md": []byte("nested paths survive the build context\n"),
			},
			Run: "ls tools/warm/README.md && python -m pip install --user --no-cache-dir -r requirements.txt",
		}
		repository, err := images.DependencyRepository(repo)
		Expect(err).NotTo(HaveOccurred())
		first, err = ensurer.Ensure(ctx, repository, deps)
		Expect(err).NotTo(HaveOccurred())
		Expect(first.Built).To(BeTrue())
		Expect(first.Ref).To(MatchRegexp(`^localhost:30500/dependencies/[^@:]+@sha256:[0-9a-f]{64}$`))
	}, SpecTimeout(30*time.Minute))

	It("reuses it while the declared files are unchanged", func(ctx SpecContext) {
		repository, err := images.DependencyRepository(repo)
		Expect(err).NotTo(HaveOccurred())
		again, err := ensurer.Ensure(ctx, repository, deps)
		Expect(err).NotTo(HaveOccurred())
		Expect(again.Built).To(BeFalse())
		Expect(again.Ref).To(Equal(first.Ref))
	}, SpecTimeout(2*time.Minute))

	It("gives a sandbox pod the warmed dependencies without installing them", func(ctx SpecContext) {
		id := domain.NewID().String()
		pod, err := sandbox.AgentPod(sandbox.PodSpec{
			Name: "e2e-deps-" + id[len(id)-12:], Namespace: agentsNS, Image: first.Ref,
			RuntimeClassName: "kata", ServiceAccountName: "frankie-agent",
			RunID: id, StepRunID: id, IssueKey: "e2e-1",
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			},
			WorkspaceSize:  resource.MustParse("1Gi"),
			Deadline:       5 * time.Minute,
			EgressProxyURL: proxyURL,
			// Like an agent or gate pod: the repo's mise.toml in the checkout,
			// which is how mise resolves the toolchain's python.
			Env: map[string]string{"MISE_TOML": miseToml},
			Command: []string{"/bin/sh", "-c", `
mkdir -p /workspace/repo && cd /workspace/repo && printf '%s' "$MISE_TOML" > mise.toml
echo "RESULT six $(python -c 'import six; print(six.__version__)' 2>&1 | tail -1)"
echo "RESULT deps_dir $(test -e /workspace/deps/requirements.txt && echo visible || echo hidden)"
`},
		})
		Expect(err).NotTo(HaveOccurred())
		created, err := cs.CoreV1().Pods(agentsNS).Create(ctx, pod, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(ctx SpecContext) {
			Expect(cs.CoreV1().Pods(agentsNS).Delete(ctx, created.Name, metav1.DeleteOptions{})).To(Succeed())
		})
		results := probeResults(ctx, created.Name)
		Expect(results).To(HaveKeyWithValue("six", "1.17.0"))
		// The warm-up's directory is under the workspace volume: the repo's checkout never sees it.
		Expect(results).To(HaveKeyWithValue("deps_dir", "hidden"))
	}, SpecTimeout(8*time.Minute))
})
