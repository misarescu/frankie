package sandbox

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RoleBranchUpdate labels the merge queue's branch update pods (D87). They
// reach only the git proxy, which the gate NetworkPolicy also allows, so they
// carry the gate role's network label.
const RoleBranchUpdate = RoleGate

// BranchUpdateContainer is the update pod's only container. Its exit code and
// termination message are the result (BranchUpdateExit*).
const BranchUpdateContainer = "update"

// Exit codes of the update container. Anything else is an error.
const (
	BranchUpdateExitDone     = 0 // updated and pushed, or already up to date
	BranchUpdateExitMoved    = 3 // the branch no longer points to the expected commit
	BranchUpdateExitConflict = 4 // the base cannot be merged or rebased cleanly
)

// BranchUpdateMode is how the branch takes in its base's new commits.
type BranchUpdateMode string

// Update modes: merge the base into the branch (for squash and merge-commit
// merges, where only the final tree matters, so a conflict is a real one), or
// rebase the branch onto it (for rebase merges, which keep its commits).
const (
	UpdateMerge  BranchUpdateMode = "merge"
	UpdateRebase BranchUpdateMode = "rebase"
)

// branchUpdateScript brings the agent branch up to date with its base branch
// and pushes it only if the branch still points to the commit the queue
// expects (a lease). It runs only git, and reports through the termination
// message:
//
//	uptodate <head> <base>
//	updated <new head> <base>
//	moved <head>
//	conflict <base> <file>...
const branchUpdateScript = `set -eu
dir=${UPDATE_DIR:-/workspace/repo}
report() { printf '%s' "$1" > "${UPDATE_REPORT:-/dev/termination-log}"; }
for attempt in 1 2 3 4 5; do
  rm -rf "$dir"
  git clone -q --no-tags --single-branch --branch "$UPDATE_BRANCH" -- "$UPDATE_URL" "$dir" && break
  [ "$attempt" = 5 ] && exit 1
  sleep "$attempt"
done
cd "$dir"
head=$(git rev-parse HEAD)
if [ "$head" != "$UPDATE_HEAD" ]; then report "moved $head"; exit 3; fi
git fetch -q --no-tags origin "+refs/heads/$UPDATE_BASE:refs/remotes/origin/$UPDATE_BASE"
base=$(git rev-parse "refs/remotes/origin/$UPDATE_BASE")
if git merge-base --is-ancestor "$base" HEAD; then report "uptodate $head $base"; exit 0; fi
if [ "$UPDATE_MODE" = rebase ]; then
  if ! git rebase -q "$base"; then
    files=$(git diff --name-only --diff-filter=U | head -n 50 | tr '\n' ' ')
    git rebase --abort || true
    report "conflict $base $files"; exit 4
  fi
else
  if ! git merge -q --no-edit -m "Merge $UPDATE_BASE into $UPDATE_BRANCH" "$base"; then
    files=$(git diff --name-only --diff-filter=U | head -n 50 | tr '\n' ' ')
    git merge --abort || true
    report "conflict $base $files"; exit 4
  fi
fi
new=$(git rev-parse HEAD)
git push -q --force-with-lease="refs/heads/$UPDATE_BRANCH:$UPDATE_HEAD" origin "HEAD:refs/heads/$UPDATE_BRANCH"
report "updated $new $base"
`

// BranchUpdatePodSpec describes one branch update pod (D87).
type BranchUpdatePodSpec struct {
	Name               string
	Namespace          string
	Image              string // any image with git: the platform base image
	RuntimeClassName   string
	ServiceAccountName string
	RunID              string
	StepRunID          string
	IssueKey           string
	Limits             corev1.ResourceList
	CPURequest         resource.Quantity
	WorkspaceSize      resource.Quantity
	Deadline           time.Duration
	EgressProxyURL     string // unused by git, which reaches only the git proxy (NO_PROXY)
	NoProxy            []string
	CloneURL           string // git proxy URL of the repo
	Token              string // git proxy token that may push the issue's branches
	Branch             string // the agent branch
	HeadSHA            string // the commit the branch must still point to
	BaseBranch         string
	Mode               BranchUpdateMode
	AuthorName         string // committer of merge or rebased commits
	AuthorEmail        string
	NodeSelector       map[string]string   // nodes it may run on (D100); empty means any
	Tolerations        []corev1.Toleration // taints it tolerates, e.g. the agent pool's (D101)
	// GuestMemoryHeadroom is VM memory beyond the containers' limits (D103).
	GuestMemoryHeadroom resource.Quantity
}

func (s BranchUpdatePodSpec) validate() error {
	base := PodSpec{
		Name: s.Name, Namespace: s.Namespace, Image: s.Image, RuntimeClassName: s.RuntimeClassName,
		ServiceAccountName: s.ServiceAccountName, RunID: s.RunID, StepRunID: s.StepRunID, IssueKey: s.IssueKey,
		Limits: s.Limits, CPURequest: s.CPURequest, WorkspaceSize: s.WorkspaceSize, Deadline: s.Deadline, EgressProxyURL: s.EgressProxyURL,
		NodeSelector: s.NodeSelector, Tolerations: s.Tolerations, GuestMemoryHeadroom: s.GuestMemoryHeadroom,
	}
	errs := []error{base.validate()}
	if u, err := url.Parse(s.CloneURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		errs = append(errs, fmt.Errorf("clone url %q must be an http(s) URL without credentials", s.CloneURL))
	}
	if s.Token == "" {
		errs = append(errs, errors.New("token is required"))
	}
	if !strings.HasPrefix(s.Branch, "agent/") {
		errs = append(errs, fmt.Errorf("branch %q is not an agent branch", s.Branch))
	}
	if !shaRE.MatchString(s.HeadSHA) {
		errs = append(errs, fmt.Errorf("head %q is not a full SHA", s.HeadSHA))
	}
	if s.BaseBranch == "" || strings.ContainsAny(s.BaseBranch, " \t\n:~^?*[\\") || strings.HasPrefix(s.BaseBranch, "-") {
		errs = append(errs, fmt.Errorf("base branch %q is not a branch name", s.BaseBranch))
	}
	if s.Mode != UpdateMerge && s.Mode != UpdateRebase {
		errs = append(errs, fmt.Errorf("update mode %q is not merge or rebase", s.Mode))
	}
	if s.AuthorName == "" || s.AuthorEmail == "" {
		errs = append(errs, errors.New("author name and email are required"))
	}
	return errors.Join(errs...)
}

// BranchUpdatePod returns the pod that brings an agent branch up to date with
// its base branch for the merge queue (D87): it clones the branch through the
// git proxy, merges or rebases the base in and pushes with a lease on the
// commit the queue expects. It runs only git, with no egress beyond the git
// proxy and no environment from the repo.
func BranchUpdatePod(s BranchUpdatePodSpec) (*corev1.Pod, error) {
	if err := s.validate(); err != nil {
		return nil, fmt.Errorf("sandbox: %w", err)
	}
	limits := s.Limits.DeepCopy()
	limits[corev1.ResourceEphemeralStorage] = s.WorkspaceSize
	resources := corev1.ResourceRequirements{Limits: limits, Requests: requestsFor(limits, s.CPURequest)}
	cred := base64.StdEncoding.EncodeToString([]byte("x-token:" + s.Token))
	env := append(proxyEnv(s.EgressProxyURL, s.NoProxy),
		corev1.EnvVar{Name: "UPDATE_URL", Value: s.CloneURL},
		corev1.EnvVar{Name: "UPDATE_BRANCH", Value: s.Branch},
		corev1.EnvVar{Name: "UPDATE_HEAD", Value: s.HeadSHA},
		corev1.EnvVar{Name: "UPDATE_BASE", Value: s.BaseBranch},
		corev1.EnvVar{Name: "UPDATE_MODE", Value: string(s.Mode)},
		corev1.EnvVar{Name: "GIT_AUTHOR_NAME", Value: s.AuthorName},
		corev1.EnvVar{Name: "GIT_AUTHOR_EMAIL", Value: s.AuthorEmail},
		corev1.EnvVar{Name: "GIT_COMMITTER_NAME", Value: s.AuthorName},
		corev1.EnvVar{Name: "GIT_COMMITTER_EMAIL", Value: s.AuthorEmail},
		corev1.EnvVar{Name: "GIT_TERMINAL_PROMPT", Value: "0"},
		corev1.EnvVar{Name: "GIT_CONFIG_NOSYSTEM", Value: "1"},
		corev1.EnvVar{Name: "GIT_CONFIG_COUNT", Value: "1"},
		corev1.EnvVar{Name: "GIT_CONFIG_KEY_0", Value: "http." + s.CloneURL + ".extraHeader"},
		corev1.EnvVar{Name: "GIT_CONFIG_VALUE_0", Value: "Authorization: Basic " + cred},
	)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        s.Name,
			Namespace:   s.Namespace,
			Annotations: noEvict(),
			Labels: map[string]string{
				RoleLabel:      RoleBranchUpdate,
				RunIDLabel:     s.RunID,
				StepRunIDLabel: s.StepRunID,
				IssueKeyLabel:  s.IssueKey,
			},
		},
		Spec: corev1.PodSpec{
			RuntimeClassName:             ptrTo(s.RuntimeClassName),
			ServiceAccountName:           s.ServiceAccountName,
			NodeSelector:                 s.NodeSelector,
			Tolerations:                  s.Tolerations,
			Resources:                    GuestMemory(*s.Limits.Memory(), s.GuestMemoryHeadroom),
			AutomountServiceAccountToken: ptrTo(false),
			EnableServiceLinks:           ptrTo(false),
			RestartPolicy:                corev1.RestartPolicyNever,
			ActiveDeadlineSeconds:        ptrTo(int64(s.Deadline.Seconds())),
			SecurityContext:              podSecurity(),
			Containers: []corev1.Container{{
				Name: BranchUpdateContainer, Image: s.Image, Command: []string{"/bin/sh", "-c", branchUpdateScript},
				Env: env, Resources: resources, SecurityContext: containerSecurity(),
				VolumeMounts:             []corev1.VolumeMount{{Name: "workspace", MountPath: "/workspace"}},
				TerminationMessagePolicy: corev1.TerminationMessageReadFile,
			}},
			Volumes: []corev1.Volume{{
				Name:         "workspace",
				VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: ptrTo(s.WorkspaceSize)}},
			}},
		},
	}, nil
}

// BranchUpdate is what an update pod reported.
type BranchUpdate struct {
	Outcome   string // uptodate, updated, moved or conflict
	HeadSHA   string // the branch's head after the update (or the moved head)
	BaseSHA   string // the base commit the branch now contains
	Conflicts []string
}

// ParseBranchUpdate reads the update container's exit code and termination
// message. The message comes from a pod that ran git on the agent's branch,
// so a caller checks a reported head against the provider before trusting it.
func ParseBranchUpdate(exitCode int32, message string) (BranchUpdate, error) {
	f := strings.Fields(message)
	bad := fmt.Errorf("branch update: exit %d with report %q", exitCode, message)
	switch {
	case exitCode == BranchUpdateExitDone && len(f) == 3 && (f[0] == "uptodate" || f[0] == "updated") && shaRE.MatchString(f[1]) && shaRE.MatchString(f[2]):
		return BranchUpdate{Outcome: f[0], HeadSHA: f[1], BaseSHA: f[2]}, nil
	case exitCode == BranchUpdateExitMoved && len(f) == 2 && f[0] == "moved" && shaRE.MatchString(f[1]):
		return BranchUpdate{Outcome: "moved", HeadSHA: f[1]}, nil
	case exitCode == BranchUpdateExitConflict && len(f) >= 2 && f[0] == "conflict" && shaRE.MatchString(f[1]):
		return BranchUpdate{Outcome: "conflict", BaseSHA: f[1], Conflicts: f[2:]}, nil
	}
	return BranchUpdate{}, bad
}
