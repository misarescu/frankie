package helm

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// object is one rendered manifest.
type object struct {
	Kind      string
	Name      string
	Namespace string
	Doc       map[string]any
}

// clusterScoped are the kinds that live outside any namespace, so two releases
// on one cluster would share them.
var clusterScoped = []string{
	"Namespace", "ClusterRole", "ClusterRoleBinding", "PriorityClass", "RuntimeClass",
	"StorageClass", "PersistentVolume", "CustomResourceDefinition", "IngressClass",
	"ValidatingWebhookConfiguration", "MutatingWebhookConfiguration", "APIService",
}

// render runs helm template for release in namespace ns and returns its objects;
// an object without a namespace is in ns, a cluster-scoped one in none.
func render(t *testing.T, release, ns string, args ...string) []object {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		if os.Getenv("CI") == "" {
			t.Skip("helm not on PATH (run through mise)")
		}
		t.Fatal("helm not on PATH")
	}
	cmd := exec.CommandContext(t.Context(), "helm", append([]string{"template", release, "frankie", "-n", ns, "--set", "images.tag=sha-test"}, args...)...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("helm template %s: %v\n%s", release, err, stderr.String())
	}
	var objs []object
	for doc := range strings.SplitSeq(out.String(), "\n---") {
		var m map[string]any
		if err := yaml.Unmarshal([]byte(doc), &m); err != nil {
			t.Fatalf("parse rendered manifest: %v\n%s", err, doc)
		}
		if len(m) == 0 {
			continue
		}
		meta, _ := m["metadata"].(map[string]any)
		o := object{Kind: fmt.Sprint(m["kind"]), Name: fmt.Sprint(meta["name"]), Doc: m}
		if !slices.Contains(clusterScoped, o.Kind) {
			o.Namespace = ns
			if n, ok := meta["namespace"].(string); ok && n != "" {
				o.Namespace = n
			}
		}
		objs = append(objs, o)
	}
	return objs
}

// production renders the factory as Kapsule runs it: values.yaml and the
// concrete profile, release frankie in namespace frankie.
func production(t *testing.T) []object {
	return render(t, "frankie", "frankie", "-f", "frankie/values-concrete.yaml")
}

// preview renders change request 7's preview as D131 describes it: the concrete
// profile, the preview profile, and what the ResourceSet sets per preview,
// which creates the namespaces itself (D138).
func preview(t *testing.T, args ...string) []object {
	return render(t, "frankie-pr-7", "frankie-pr-7", append([]string{
		"-f", "frankie/values-concrete.yaml",
		"-f", "frankie/values-preview.yaml",
		"--set", "namespaces.create=false",
		"--set", "agents.namespace=frankie-pr-7-agents",
		"--set", "toolchains.build.namespace=frankie-pr-7-build",
		"--set", "toolchains.registry.nodePort=30510",
		"--set-json", `controlPlane.repos=[{"name":"misarescu/frankie-dummy","upstream":"https://github.com/misarescu/frankie-dummy.git","keyPrefix":"pr7dum","baseBranch":"e2e/pr-7"}]`,
		"--set", "controlPlane.intake.label=e2e-pr-7",
	}, args...)...)
}

// nodePorts lists every NodePort the objects' Services claim.
func nodePorts(objs []object) []string {
	var ports []string
	for _, o := range objs {
		if o.Kind != "Service" {
			continue
		}
		spec, _ := o.Doc["spec"].(map[string]any)
		ps, _ := spec["ports"].([]any)
		for _, p := range ps {
			if np, ok := p.(map[string]any)["nodePort"]; ok {
				ports = append(ports, fmt.Sprint(np))
			}
		}
	}
	return ports
}

// D131 (4), D138: a preview creates nothing cluster-wide, not even its
// namespaces, which the ResourceSet creates; production creates its own.
func TestPreviewCreatesNothingClusterScoped(t *testing.T) {
	for _, o := range preview(t) {
		if slices.Contains(clusterScoped, o.Kind) {
			t.Errorf("preview renders cluster-scoped %s %q", o.Kind, o.Name)
		}
	}
	var namespaces []string
	for _, o := range production(t) {
		if o.Kind == "Namespace" {
			namespaces = append(namespaces, o.Name)
		}
	}
	slices.Sort(namespaces)
	if want := []string{"frankie-agents", "frankie-build"}; !slices.Equal(namespaces, want) {
		t.Errorf("production creates namespaces %v, want %v", namespaces, want)
	}
}

// D138: the previews' Helm service account is bound only in the preview's three
// namespaces, to a role granting these kinds (misarescu/frankie-cd,
// apps/kapsule/frankie-previews/rbac.yaml). A preview object elsewhere, or of
// another kind, fails here rather than in the preview.
func TestPreviewStaysWithinItsDeployerRole(t *testing.T) {
	granted := []string{
		"ConfigMap", "Secret", "Service", "ServiceAccount", "Pod",
		"Deployment", "StatefulSet", "Job",
		"NetworkPolicy", "Role", "RoleBinding",
	}
	namespaces := []string{"frankie-pr-7", "frankie-pr-7-agents", "frankie-pr-7-build"}
	for _, o := range preview(t) {
		if !slices.Contains(granted, o.Kind) {
			t.Errorf("preview renders %s %q, which its deployer role does not grant", o.Kind, o.Name)
		}
		if !slices.Contains(namespaces, o.Namespace) {
			t.Errorf("preview renders %s %q in namespace %q, outside %v", o.Kind, o.Name, o.Namespace, namespaces)
		}
	}
}

// D131 (1), (3): a preview beside production on one cluster shares no object
// and no NodePort with it.
func TestPreviewBesideProductionSharesNothing(t *testing.T) {
	key := func(o object) string { return o.Kind + " " + o.Namespace + "/" + o.Name }
	prod := map[string]bool{}
	for _, o := range production(t) {
		prod[key(o)] = true
	}
	pre := preview(t)
	for _, o := range pre {
		if prod[key(o)] {
			t.Errorf("production and the preview both render %s", key(o))
		}
	}
	for _, p := range nodePorts(pre) {
		if slices.Contains(nodePorts(production(t)), p) {
			t.Errorf("production and the preview both claim NodePort %s", p)
		}
	}
}

// D131 (2): a preview keeps nothing beyond its pods; production keeps its claims.
func TestPreviewKeepsNoVolumeClaims(t *testing.T) {
	claims := func(objs []object) []string {
		var out []string
		for _, o := range objs {
			if o.Kind == "PersistentVolumeClaim" {
				out = append(out, o.Name)
			}
			if spec, _ := o.Doc["spec"].(map[string]any); o.Kind == "StatefulSet" && spec["volumeClaimTemplates"] != nil {
				out = append(out, o.Name+" (volumeClaimTemplates)")
			}
		}
		return out
	}
	if got := claims(preview(t)); len(got) != 0 {
		t.Errorf("preview claims volumes: %v", got)
	}
	if got := claims(production(t)); len(got) != 4 {
		t.Errorf("production claims %v, want Postgres, Temporal, the registry and the Go module proxy", got)
	}
}

// D131 (2): a preview runs no web UI; production does.
func TestPreviewRunsNoWebUI(t *testing.T) {
	hasWeb := func(objs []object) bool {
		return slices.ContainsFunc(objs, func(o object) bool { return o.Kind == "Deployment" && strings.HasSuffix(o.Name, "-web") })
	}
	if hasWeb(preview(t)) {
		t.Error("preview renders the web UI")
	}
	if !hasWeb(production(t)) {
		t.Error("production renders no web UI")
	}
}

// D136: verification pods reach stages by namespace name, or by labels for
// the previews of the factory, each in its own namespace; an entry needs
// exactly one of them.
func TestVerifyStageEgress(t *testing.T) {
	objs := render(t, "frankie", "frankie", "-f", "frankie/values-concrete.yaml",
		"--set-json", `agents.verify.stageEgress=[{"namespace":"tailscale","ports":[80]},{"namespaceLabels":{"frankie.dev/preview":"true"},"ports":[8080]}]`)
	i := slices.IndexFunc(objs, func(o object) bool { return o.Kind == "NetworkPolicy" && o.Name == "verify" })
	if i < 0 {
		t.Fatal("no NetworkPolicy verify")
	}
	b, err := yaml.Marshal(objs[i].Doc["spec"])
	if err != nil {
		t.Fatal(err)
	}
	spec := string(b)
	for _, want := range []string{"kubernetes.io/metadata.name: tailscale", "port: 80\n", "frankie.dev/preview: \"true\"", "port: 8080"} {
		if !strings.Contains(spec, want) {
			t.Errorf("verify NetworkPolicy lacks %q:\n%s", want, spec)
		}
	}

	for name, entry := range map[string]string{
		"neither": `[{"ports":[80]}]`,
		"both":    `[{"namespace":"tailscale","namespaceLabels":{"a":"b"},"ports":[80]}]`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := exec.LookPath("helm"); err != nil {
				t.Skip("helm not on PATH")
			}
			cmd := exec.CommandContext(t.Context(), "helm", "template", "frankie", "frankie", "--set", "images.tag=sha-test",
				"-f", "frankie/values-concrete.yaml", "--set-json", "agents.verify.stageEgress="+entry)
			if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "give a namespace or namespaceLabels") {
				t.Errorf("helm template = %v:\n%s", err, out)
			}
		})
	}
}

// D131 (4), D133: the gateway admits the extra peers it is given (production:
// the previews' gateways) and may reach the extra destinations (a preview: the
// gateway it chains to).
func TestGatewayExtraPeers(t *testing.T) {
	objs := render(t, "frankie", "frankie", "-f", "frankie/values-concrete.yaml",
		"--set-json", `llmGateway.ingressFrom=[{"namespaceSelector":{"matchLabels":{"frankie.dev/preview":"true"}},"podSelector":{"matchLabels":{"app.kubernetes.io/component":"llm-gateway"}}}]`,
		"--set-json", `llmGateway.egressTo=[{"to":[{"namespaceSelector":{"matchLabels":{"kubernetes.io/metadata.name":"frankie"}}}],"ports":[{"port":4000,"protocol":"TCP"}]}]`)
	i := slices.IndexFunc(objs, func(o object) bool { return o.Kind == "NetworkPolicy" && o.Name == "frankie-llm-gateway" })
	if i < 0 {
		t.Fatal("no NetworkPolicy frankie-llm-gateway")
	}
	b, err := yaml.Marshal(objs[i].Doc["spec"])
	if err != nil {
		t.Fatal(err)
	}
	spec := string(b)
	for _, want := range []string{"frankie.dev/preview: \"true\"", "kubernetes.io/metadata.name: frankie\n", "port: 4000"} {
		if !strings.Contains(spec, want) {
			t.Errorf("gateway NetworkPolicy lacks %q:\n%s", want, spec)
		}
	}
	// Without extra peers the policy is as before: its agents and control plane only.
	objs = production(t)
	i = slices.IndexFunc(objs, func(o object) bool { return o.Kind == "NetworkPolicy" && o.Name == "frankie-llm-gateway" })
	ingress := objs[i].Doc["spec"].(map[string]any)["ingress"].([]any)
	if from := ingress[0].(map[string]any)["from"].([]any); len(from) != 2 {
		t.Errorf("production gateway admits %d peers, want 2 (agents, control plane)", len(from))
	}
}
