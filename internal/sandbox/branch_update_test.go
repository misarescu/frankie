package sandbox

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func updateSpec() BranchUpdatePodSpec {
	return BranchUpdatePodSpec{
		Name: "gc-1-enqueue-update-00000004", Namespace: "frankie-agents", Image: "ghcr.io/x/runner:sha-1",
		RuntimeClassName: "kata", ServiceAccountName: "frankie-agent",
		RunID: "run-1", StepRunID: "step-2", IssueKey: "gc-1",
		Limits:        corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("1Gi")},
		WorkspaceSize: resource.MustParse("2Gi"), Deadline: 10 * time.Minute, EgressProxyURL: "http://proxy:3128",
		CloneURL: "http://frankie-controlplane.frankie.svc:8081/git/o/r.git", Token: "fgp1.secret.sig",
		Branch: "agent/gc-1/add-search", HeadSHA: "0123456789abcdef0123456789abcdef01234567", BaseBranch: "main",
		Mode: UpdateMerge, AuthorName: "frankie-agent", AuthorEmail: "agent@frankie.invalid",
	}
}

// M6 (D87): the update pod runs only git, as the agent user, with the push
// token in a header and the gate role's network.
func TestBranchUpdatePod(t *testing.T) {
	pod, err := BranchUpdatePod(updateSpec())
	if err != nil {
		t.Fatal(err)
	}
	if len(pod.Spec.InitContainers) != 0 || len(pod.Spec.Containers) != 1 {
		t.Fatalf("containers = %+v", pod.Spec.Containers)
	}
	c := pod.Spec.Containers[0]
	if c.Name != BranchUpdateContainer || c.Command[2] != branchUpdateScript || c.TerminationMessagePolicy != corev1.TerminationMessageReadFile {
		t.Errorf("container = %s %v", c.Name, c.TerminationMessagePolicy)
	}
	for k, want := range map[string]string{
		"UPDATE_BRANCH": "agent/gc-1/add-search", "UPDATE_HEAD": "0123456789abcdef0123456789abcdef01234567",
		"UPDATE_BASE": "main", "UPDATE_MODE": "merge", "GIT_COMMITTER_NAME": "frankie-agent",
	} {
		if got := envValue(c.Env, k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if v := envValue(c.Env, "GIT_CONFIG_VALUE_0"); !strings.HasPrefix(v, "Authorization: Basic ") {
		t.Errorf("no credential header: %q", v)
	}
	if pod.Labels[RoleLabel] != RoleGate || pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Errorf("labels %v, automount %v", pod.Labels, pod.Spec.AutomountServiceAccountToken)
	}
	if pod.Annotations[SafeToEvictAnnotation] != "false" { // D99
		t.Errorf("an autoscaler may evict the update: annotations = %v", pod.Annotations)
	}
	if *c.SecurityContext.AllowPrivilegeEscalation || pod.Spec.SecurityContext.RunAsNonRoot == nil || !*pod.Spec.SecurityContext.RunAsNonRoot {
		t.Error("the update container is not locked down like the gate's")
	}
}

func TestBranchUpdatePodRejects(t *testing.T) {
	for name, mutate := range map[string]func(*BranchUpdatePodSpec){
		"not an agent branch": func(s *BranchUpdatePodSpec) { s.Branch = "main" },
		"short head":          func(s *BranchUpdatePodSpec) { s.HeadSHA = "0123456" },
		"option as base":      func(s *BranchUpdatePodSpec) { s.BaseBranch = "--upload-pack=x" },
		"bad mode":            func(s *BranchUpdatePodSpec) { s.Mode = "squash" },
		"no token":            func(s *BranchUpdatePodSpec) { s.Token = "" },
		"credentials in url":  func(s *BranchUpdatePodSpec) { s.CloneURL = "http://u:p@host/git/o/r.git" },
		"no author":           func(s *BranchUpdatePodSpec) { s.AuthorEmail = "" },
	} {
		s := updateSpec()
		mutate(&s)
		if _, err := BranchUpdatePod(s); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParseBranchUpdate(t *testing.T) {
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	tests := []struct {
		code    int32
		msg     string
		want    BranchUpdate
		wantErr bool
	}{
		{0, "updated " + a + " " + b, BranchUpdate{Outcome: "updated", HeadSHA: a, BaseSHA: b}, false},
		{0, "uptodate " + a + " " + b, BranchUpdate{Outcome: "uptodate", HeadSHA: a, BaseSHA: b}, false},
		{3, "moved " + a, BranchUpdate{Outcome: "moved", HeadSHA: a}, false},
		{4, "conflict " + b + " src/a.go web/App.jsx", BranchUpdate{Outcome: "conflict", BaseSHA: b, Conflicts: []string{"src/a.go", "web/App.jsx"}}, false},
		{0, "conflict " + b, BranchUpdate{}, true}, // the exit code and the report disagree
		{1, "", BranchUpdate{}, true},
		{0, "updated nope " + b, BranchUpdate{}, true},
	}
	for _, tt := range tests {
		got, err := ParseBranchUpdate(tt.code, tt.msg)
		if (err != nil) != tt.wantErr || got.Outcome != tt.want.Outcome || got.HeadSHA != tt.want.HeadSHA || got.BaseSHA != tt.want.BaseSHA ||
			!slices.Equal(got.Conflicts, tt.want.Conflicts) {
			t.Errorf("Parse(%d, %q) = %+v, %v", tt.code, tt.msg, got, err)
		}
	}
}

// gitRemote is a bare repository with main and an agent branch forked from it.
type gitRemote struct {
	t      *testing.T
	url    string
	work   string
	branch string
}

func (g gitRemote) git(args ...string) string {
	g.t.Helper()
	cmd := exec.CommandContext(g.t.Context(), "git", args...)
	cmd.Dir = g.work
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x", "GIT_CONFIG_NOSYSTEM=1", "HOME="+g.work)
	out, err := cmd.CombinedOutput()
	if err != nil {
		g.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (g gitRemote) commit(branch, file, body string) string {
	if g.git("symbolic-ref", "--short", "HEAD") != branch {
		g.git("checkout", "-q", branch)
	}
	if err := os.WriteFile(filepath.Join(g.work, file), []byte(body), 0o600); err != nil {
		g.t.Fatal(err)
	}
	g.git("add", file)
	g.git("commit", "-q", "-m", file)
	g.git("push", "-q", "origin", branch)
	return g.git("rev-parse", "HEAD")
}

func newGitRemote(t *testing.T) gitRemote {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	g := gitRemote{t: t, url: "file://" + filepath.Join(root, "remote.git"), work: filepath.Join(root, "work"), branch: "agent/gc-1/x"}
	if err := os.MkdirAll(g.work, 0o700); err != nil {
		t.Fatal(err)
	}
	g.git("init", "-q", "--bare", "-b", "main", filepath.Join(root, "remote.git"))
	g.git("init", "-q", "-b", "main")
	g.git("remote", "add", "origin", g.url)
	g.commit("main", "README", "hello\n")
	g.git("checkout", "-q", "-b", g.branch)
	g.commit(g.branch, "feature.txt", "one\n")
	return g
}

// runUpdate runs the update script the pod runs, against the local remote.
func (g gitRemote) runUpdate(head string, mode BranchUpdateMode) (BranchUpdate, error) {
	g.t.Helper()
	dir := g.t.TempDir()
	report := filepath.Join(dir, "report")
	cmd := exec.CommandContext(g.t.Context(), "/bin/sh", "-c", branchUpdateScript)
	cmd.Env = append(os.Environ(), "UPDATE_DIR="+filepath.Join(dir, "repo"), "UPDATE_REPORT="+report,
		"UPDATE_URL="+g.url, "UPDATE_BRANCH="+g.branch, "UPDATE_HEAD="+head, "UPDATE_BASE=main", "UPDATE_MODE="+string(mode),
		"GIT_AUTHOR_NAME=frankie", "GIT_AUTHOR_EMAIL=f@x", "GIT_COMMITTER_NAME=frankie", "GIT_COMMITTER_EMAIL=f@x",
		"GIT_CONFIG_NOSYSTEM=1", "HOME="+dir)
	out, err := cmd.CombinedOutput()
	code := int32(0)
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = int32(exit.ExitCode())
	} else if err != nil {
		g.t.Fatalf("update script: %v\n%s", err, out)
	}
	msg, _ := os.ReadFile(report)
	u, perr := ParseBranchUpdate(code, string(msg))
	if perr != nil {
		g.t.Logf("script output:\n%s", out)
	}
	return u, perr
}

func (g gitRemote) remoteHead(branch string) string {
	g.git("fetch", "-q", "origin")
	return g.git("rev-parse", "origin/"+branch)
}

// M6 (D87): the update script, run against a real git remote.
func TestBranchUpdateScript(t *testing.T) {
	t.Run("already up to date", func(t *testing.T) {
		g := newGitRemote(t)
		head := g.remoteHead(g.branch)
		u, err := g.runUpdate(head, UpdateMerge)
		if err != nil || u.Outcome != "uptodate" || u.HeadSHA != head || u.BaseSHA != g.remoteHead("main") {
			t.Fatalf("update = %+v, %v", u, err)
		}
	})
	for _, mode := range []BranchUpdateMode{UpdateMerge, UpdateRebase} {
		t.Run("base moved, "+string(mode), func(t *testing.T) {
			g := newGitRemote(t)
			head := g.remoteHead(g.branch)
			base := g.commit("main", "other.txt", "two\n")
			u, err := g.runUpdate(head, mode)
			if err != nil || u.Outcome != "updated" || u.BaseSHA != base || u.HeadSHA == head {
				t.Fatalf("update = %+v, %v", u, err)
			}
			if got := g.remoteHead(g.branch); got != u.HeadSHA {
				t.Errorf("remote branch at %s, reported %s", got, u.HeadSHA)
			}
			g.git("merge-base", "--is-ancestor", base, u.HeadSHA) // fails the test if the base is not in
			parents := strings.Fields(g.git("rev-list", "--parents", "-n", "1", u.HeadSHA))
			if wantMerge := mode == UpdateMerge; (len(parents) == 3) != wantMerge {
				t.Errorf("%s produced a commit with parents %v", mode, parents[1:])
			}
		})
	}
	t.Run("branch moved since it was checked", func(t *testing.T) {
		g := newGitRemote(t)
		old := g.remoteHead(g.branch)
		now := g.commit(g.branch, "feature.txt", "one more\n")
		g.commit("main", "other.txt", "two\n")
		u, err := g.runUpdate(old, UpdateMerge)
		if err != nil || u.Outcome != "moved" || u.HeadSHA != now {
			t.Fatalf("update = %+v, %v", u, err)
		}
		if got := g.remoteHead(g.branch); got != now {
			t.Errorf("a moved branch was pushed: %s", got)
		}
	})
	for _, mode := range []BranchUpdateMode{UpdateMerge, UpdateRebase} {
		t.Run("conflict, "+string(mode), func(t *testing.T) {
			g := newGitRemote(t)
			head := g.remoteHead(g.branch)
			g.commit("main", "feature.txt", "theirs\n")
			u, err := g.runUpdate(head, mode)
			if err != nil || u.Outcome != "conflict" || !slices.Equal(u.Conflicts, []string{"feature.txt"}) {
				t.Fatalf("update = %+v, %v", u, err)
			}
			if got := g.remoteHead(g.branch); got != head {
				t.Errorf("a conflicted branch was pushed: %s", got)
			}
		})
	}
}

// D100: branch update pods run where agent pods do.
func TestBranchUpdatePodNodeSelector(t *testing.T) {
	s := updateSpec()
	s.NodeSelector = map[string]string{"k8s.scaleway.com/pool-name": "frankie-agents"}
	pod, err := BranchUpdatePod(s)
	if err != nil {
		t.Fatal(err)
	}
	if pod.Spec.NodeSelector["k8s.scaleway.com/pool-name"] != "frankie-agents" {
		t.Errorf("node selector = %v", pod.Spec.NodeSelector)
	}
	s.NodeSelector = map[string]string{"not a key!": "x"}
	if _, err := BranchUpdatePod(s); err == nil {
		t.Error("a bad selector was accepted")
	}
}

// D101: branch update pods tolerate what agent pods do.
func TestBranchUpdatePodTolerations(t *testing.T) {
	s := updateSpec()
	s.Tolerations = []corev1.Toleration{agentPoolTaint}
	pod, err := BranchUpdatePod(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(pod.Spec.Tolerations) != 1 || pod.Spec.Tolerations[0] != agentPoolTaint {
		t.Errorf("tolerations = %v", pod.Spec.Tolerations)
	}
	s.Tolerations = []corev1.Toleration{{Key: "pool", Operator: "In"}}
	if _, err := BranchUpdatePod(s); err == nil {
		t.Error("a bad toleration was accepted")
	}
}
