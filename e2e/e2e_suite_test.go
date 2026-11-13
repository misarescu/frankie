//go:build e2e

// Package e2e holds acceptance tests that run against a real cluster, written
// with Ginkgo and Gomega (D25). They need the platform chart installed and:
//
//	FRANKIE_E2E_RUNNER_IMAGE  runner image, e.g. ghcr.io/misarescu/frankie-runner:sha-87f0290 (required; the suite fails without it)
//	FRANKIE_E2E_NAMESPACE     agents namespace (default frankie-agents)
//	FRANKIE_E2E_PROXY_URL     egress proxy (default http://frankie-egress-proxy.frankie.svc:3128)
//	FRANKIE_E2E_PLATFORM_NS   platform namespace (default frankie)
//	KUBECONFIG                cluster access, as for kubectl
//
// Run with: mise run e2e
package e2e

import (
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Frankie cluster acceptance")
}

var (
	cs               *kubernetes.Clientset
	runnerImage      string
	agentsNS         string
	platformNS       string
	proxyURL         string
	controlPlaneHost string
	postgresHost     string
)

var _ = BeforeSuite(func() {
	// An acceptance run that cannot run is a failure, not a pass.
	runnerImage = os.Getenv("FRANKIE_E2E_RUNNER_IMAGE")
	if runnerImage == "" {
		Fail("FRANKIE_E2E_RUNNER_IMAGE is not set. The suite needs the platform chart installed and a published runner image:\n" +
			"  1. merge to main so CI publishes ghcr.io/misarescu/frankie-*:sha-<commit>\n" +
			"  2. helm upgrade --install frankie deploy/helm/frankie -n frankie --create-namespace \\\n" +
			"       -f deploy/helm/frankie/values-concrete.yaml --set images.tag=sha-<commit>\n" +
			"  3. FRANKIE_E2E_RUNNER_IMAGE=ghcr.io/misarescu/frankie-runner:sha-<commit> mise run e2e")
	}
	agentsNS = envOr("FRANKIE_E2E_NAMESPACE", "frankie-agents")
	platformNS = envOr("FRANKIE_E2E_PLATFORM_NS", "frankie")
	controlPlaneHost = "frankie-controlplane." + platformNS + ".svc"
	postgresHost = "frankie-postgres." + platformNS + ".svc"
	proxyURL = envOr("FRANKIE_E2E_PROXY_URL", "http://frankie-egress-proxy.frankie.svc:3128")

	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{}).ClientConfig()
	Expect(err).NotTo(HaveOccurred(), "load kubeconfig")
	cs, err = kubernetes.NewForConfig(cfg)
	Expect(err).NotTo(HaveOccurred())
})

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
