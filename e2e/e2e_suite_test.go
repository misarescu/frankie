//go:build e2e

// Package e2e holds acceptance tests that run against a real cluster, written
// with Ginkgo and Gomega (D25). They need the platform chart installed and:
//
//	FRANKIE_E2E_RUNNER_IMAGE  runner image, e.g. ghcr.io/misarescu/frankie-runner:sha-87f0290 (required)
//	FRANKIE_E2E_NAMESPACE     agents namespace (default frankie-agents)
//	FRANKIE_E2E_PROXY_URL     egress proxy (default http://factory-egress-proxy.frankie.svc:3128)
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
	cs          *kubernetes.Clientset
	runnerImage string
	agentsNS    string
	proxyURL    string
)

var _ = BeforeSuite(func() {
	runnerImage = os.Getenv("FRANKIE_E2E_RUNNER_IMAGE")
	if runnerImage == "" {
		Skip("FRANKIE_E2E_RUNNER_IMAGE not set")
	}
	agentsNS = envOr("FRANKIE_E2E_NAMESPACE", "frankie-agents")
	proxyURL = envOr("FRANKIE_E2E_PROXY_URL", "http://factory-egress-proxy.frankie.svc:3128")

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
