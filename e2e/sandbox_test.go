//go:build e2e

// Package e2e holds acceptance tests that run against a real cluster.
// They need the platform chart installed and these environment variables:
//
//	FRANKIE_E2E_RUNNER_IMAGE  runner image, e.g. ghcr.io/misarescu/frankie-runner:sha-87f0290 (required)
//	FRANKIE_E2E_NAMESPACE     agents namespace (default frankie-agents)
//	FRANKIE_E2E_PROXY_URL     egress proxy (default http://factory-egress-proxy.frankie.svc:3128)
//	KUBECONFIG                cluster access, as for kubectl
//
// Run with: mise run e2e
package e2e

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/sandbox"
)

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func client(t *testing.T) *kubernetes.Clientset {
	t.Helper()
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		t.Fatalf("kubeconfig: %v", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	return cs
}

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
func TestAgentSandbox(t *testing.T) {
	image := os.Getenv("FRANKIE_E2E_RUNNER_IMAGE")
	if image == "" {
		t.Skip("FRANKIE_E2E_RUNNER_IMAGE not set")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
	defer cancel()
	cs := client(t)
	ns := env("FRANKIE_E2E_NAMESPACE", "frankie-agents")

	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil || len(nodes.Items) == 0 {
		t.Fatalf("list nodes: %v", err)
	}
	node := nodes.Items[0]
	var nodeIP string
	for _, a := range node.Status.Addresses {
		if a.Type == corev1.NodeInternalIP {
			nodeIP = a.Address
		}
	}

	id := domain.NewID().String()
	pod, err := sandbox.AgentPod(sandbox.PodSpec{
		Name: "e2e-sandbox-" + id[len(id)-12:], Namespace: ns, Image: image,
		RuntimeClassName: "kata", ServiceAccountName: "frankie-agent",
		RunID: id, StepRunID: id, IssueKey: "e2e-1",
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("500m"),
			corev1.ResourceMemory: resource.MustParse("512Mi"),
		},
		WorkspaceSize:  resource.MustParse("1Gi"),
		Deadline:       5 * time.Minute,
		EgressProxyURL: env("FRANKIE_E2E_PROXY_URL", "http://factory-egress-proxy.frankie.svc:3128"),
		Env:            map[string]string{"NODE_IP": nodeIP},
		Command:        []string{"/bin/sh", "-c", probeScript},
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := cs.CoreV1().Pods(ns).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create probe pod: %v", err)
	}
	t.Cleanup(func() {
		_ = cs.CoreV1().Pods(ns).Delete(context.Background(), created.Name, metav1.DeleteOptions{})
	})

	results := waitForResults(ctx, t, cs, ns, created.Name)
	t.Logf("probe results: %v", results)

	expect := func(name string, ok bool, why string) {
		t.Helper()
		if !ok {
			t.Errorf("%s = %q: %s", name, results[name], why)
		}
	}
	expect("uid", results["uid"] == "10001", "must run as the non-root agent user")
	expect("kernel", results["kernel"] != "" && results["kernel"] != node.Status.NodeInfo.KernelVersion,
		"must differ from the host kernel "+node.Status.NodeInfo.KernelVersion+" (Kata VM)")
	expect("sa_token", results["sa_token"] == "absent", "no service account token (FR-SB-5)")
	expect("dns", results["dns"] == "ok", "cluster DNS must resolve")
	expect("proxy_allowed", results["proxy_allowed"] != "000" && results["proxy_allowed"] != "", "allowlisted host must be reachable through the proxy")
	expect("proxy_denied_host", results["proxy_denied_host"] == "403", "unlisted host must be refused by the proxy")
	expect("proxy_denied_lan", results["proxy_denied_lan"] == "403", "the node/LAN must be refused by the proxy")
	expect("direct_internet", results["direct_internet"] == "000", "direct internet access must be dropped")
	expect("direct_lan", results["direct_lan"] == "000", "direct LAN access must be dropped")
	expect("direct_k8s_api", results["direct_k8s_api"] == "000", "the Kubernetes API must be unreachable")
}

// waitForResults waits for the probe pod to finish and parses its RESULT lines.
func waitForResults(ctx context.Context, t *testing.T, cs *kubernetes.Clientset, ns, name string) map[string]string {
	t.Helper()
	for {
		p, err := cs.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get pod: %v", err)
		}
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			break
		}
		for _, s := range p.Status.ContainerStatuses {
			if w := s.State.Waiting; w != nil && (w.Reason == "ImagePullBackOff" || w.Reason == "ErrImagePull" || w.Reason == "CreateContainerConfigError") {
				t.Fatalf("probe pod cannot start: %s: %s", w.Reason, w.Message)
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("probe pod did not finish: phase %s", p.Status.Phase)
		case <-time.After(2 * time.Second):
		}
	}
	raw, err := cs.CoreV1().Pods(ns).GetLogs(name, &corev1.PodLogOptions{}).Do(ctx).Raw()
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	results := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(string(raw)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 2 && f[0] == "RESULT" {
			results[f[1]] = strings.Join(f[2:], " ")
		}
	}
	if len(results) == 0 {
		t.Fatalf("no probe results in logs:\n%s", raw)
	}
	return results
}

// The agents namespace enforces Pod Security "restricted": a privileged pod is
// refused by the API server even if something other than the builder creates it.
func TestAgentNamespaceRefusesPrivilegedPods(t *testing.T) {
	if os.Getenv("FRANKIE_E2E_RUNNER_IMAGE") == "" {
		t.Skip("FRANKIE_E2E_RUNNER_IMAGE not set")
	}
	cs := client(t)
	ns := env("FRANKIE_E2E_NAMESPACE", "frankie-agents")
	privileged := true
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("e2e-privileged-%d", time.Now().Unix()), Namespace: ns},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "x", Image: "busybox", SecurityContext: &corev1.SecurityContext{Privileged: &privileged},
		}}},
	}
	_, err := cs.CoreV1().Pods(ns).Create(t.Context(), pod, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}})
	if !apierrors.IsForbidden(err) || !strings.Contains(err.Error(), "restricted") {
		t.Fatalf("privileged pod: err = %v, want a Pod Security 'restricted' rejection", err)
	}
}
