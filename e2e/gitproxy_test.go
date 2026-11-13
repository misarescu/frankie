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
	"github.com/misarescu/frankie/internal/gitproxy"
	"github.com/misarescu/frankie/internal/sandbox"
)

// gitProbeScript clones through the git proxy and tries pushes the proxy must
// refuse. Refused pushes never reach the upstream, so nothing is written there.
const gitProbeScript = `
r() { echo "RESULT $1 $2"; }
export GIT_TERMINAL_PROMPT=0
U="http://x-token:${GIT_TOKEN}@${CP_HOST}:8081/git/${REPO}.git"
if git clone -q --depth 1 "$U" /workspace/repo >/tmp/clone.log 2>&1; then r clone ok; else r clone "fail:$(tail -1 /tmp/clone.log | tr ' ' _)"; fi
cd /workspace/repo || exit 0
git -c user.name=e2e -c user.email=e2e@frankie.invalid commit -q --allow-empty -m "e2e: must never land"
push() { out=$(git push origin "HEAD:$1" 2>&1); if echo "$out" | grep -q "remote rejected"; then echo rejected; else echo "accepted:$(echo "$out" | tail -1 | tr ' ' _)"; fi; }
r push_main "$(push refs/heads/main)"
r push_other_issue "$(push refs/heads/agent/e2e-2/x)"
r push_tag "$(push refs/tags/e2e-must-not-exist)"
r wrong_token_clone "$(git ls-remote "http://x-token:fgp1.forged.sig@${CP_HOST}:8081/git/${REPO}.git" >/dev/null 2>&1 && echo ok || echo refused)"
`

// M1 acceptance (docs/spec/10): from a sandboxed agent pod, pushing to main fails.
// D21: the agent's token clones its repo but may only push its own issue's branches.
var _ = Describe("Git proxy from an agent pod", Ordered, ContinueOnFailure, Label("sandbox", "gitproxy"), func() {
	var results map[string]string

	BeforeAll(func(ctx SpecContext) {
		key, err := cs.CoreV1().Secrets(platformNS).Get(ctx, "frankie-controlplane", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "read the git proxy signing key")
		signer, err := gitproxy.NewSigner(key.Data["gitproxy-key"])
		Expect(err).NotTo(HaveOccurred())
		token, err := signer.Issue(gitproxy.Grant{Repo: repo, IssueKey: "e2e-1", StepRunID: "e2e"}, 10*time.Minute)
		Expect(err).NotTo(HaveOccurred())

		id := domain.NewID().String()
		pod, err := sandbox.AgentPod(sandbox.PodSpec{
			Name: "e2e-gitproxy-" + id[len(id)-12:], Namespace: agentsNS, Image: runnerImage,
			RuntimeClassName: "kata", ServiceAccountName: "frankie-agent",
			RunID: id, StepRunID: id, IssueKey: "e2e-1",
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			},
			WorkspaceSize:  resource.MustParse("2Gi"),
			Deadline:       5 * time.Minute,
			EgressProxyURL: proxyURL,
			Env:            map[string]string{"GIT_TOKEN": token, "CP_HOST": controlPlaneHost, "REPO": repo},
			Command:        []string{"/bin/sh", "-c", networkReady + gitProbeScript},
		})
		Expect(err).NotTo(HaveOccurred())
		created, err := cs.CoreV1().Pods(agentsNS).Create(ctx, pod, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(ctx SpecContext) {
			Expect(cs.CoreV1().Pods(agentsNS).Delete(ctx, created.Name, metav1.DeleteOptions{})).To(Succeed())
		})
		results = probeResults(ctx, created.Name)
		AddReportEntry("git probe results", results)
	}, NodeTimeout(6*time.Minute))

	It("clones the repo through the proxy with its step token", func() {
		Expect(results).To(HaveKeyWithValue("clone", "ok"))
	})
	It("cannot push to main", func() {
		Expect(results).To(HaveKeyWithValue("push_main", "rejected"))
	})
	It("cannot push another issue's branch", func() {
		Expect(results).To(HaveKeyWithValue("push_other_issue", "rejected"))
	})
	It("cannot push tags", func() {
		Expect(results).To(HaveKeyWithValue("push_tag", "rejected"))
	})
	It("is refused with a forged token", func() {
		Expect(results).To(HaveKeyWithValue("wrong_token_clone", "refused"))
	})
})
