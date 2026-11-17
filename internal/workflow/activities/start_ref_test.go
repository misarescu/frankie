package activities

import (
	"slices"
	"testing"

	"github.com/misarescu/frankie/internal/images"
)

const earlierAttemptSHA = "15d3b56dc87b1d230a78cc373b793a9307ad9422"

func TestStartPoint(t *testing.T) {
	branch := BranchName(testInput.IssueKey, testInput.Title)
	side := branch + "--e2e-tests"
	tests := []struct {
		name   string
		branch string // the step's branch; empty: the issue's
		files  map[string]string
		want   StartPoint
	}{
		{"fresh issue starts from the base branch", "", map[string]string{},
			StartPoint{CloneRef: "main", FilesRef: "main"}},
		{"a re-run continues the issue's agent branch", "", map[string]string{"branch:" + branch: earlierAttemptSHA},
			StartPoint{CloneRef: branch, FilesRef: earlierAttemptSHA, Continuing: true}},
		{"the issue's branch, named", branch, map[string]string{"branch:" + branch: earlierAttemptSHA},
			StartPoint{CloneRef: branch, FilesRef: earlierAttemptSHA, Continuing: true}},
		// M7 (D122): a side writer starts from the base branch even when the
		// issue's branch exists, so it never sees the other writer's work, and
		// continues its own branch once it pushed.
		{"a side writer starts from the base branch", side, map[string]string{"branch:" + branch: earlierAttemptSHA},
			StartPoint{CloneRef: "main", FilesRef: "main"}},
		{"a side writer continues its own branch", side, map[string]string{"branch:" + branch: earlierAttemptSHA, "branch:" + side: testBaseSHA},
			StartPoint{CloneRef: side, FilesRef: testBaseSHA, Continuing: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Activities{GitHub: fakeRepo(t, tt.files)}
			got, err := a.startPoint(t.Context(), testInput, tt.branch)
			if err != nil || got != tt.want {
				t.Fatalf("startPoint = %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
}

// The agent's toolchain comes from the code it checks out: on a re-run that is
// the agent branch, whose mise.toml may differ from the base branch's.
func TestEnsureToolchainImageFollowsTheAgentBranch(t *testing.T) {
	branch := BranchName(testInput.IssueKey, testInput.Title)
	gh, reads := fakeRepoWithReads(t, map[string]string{
		"mise.toml":                      "[tools]\ngo = \"1.27\"\n",
		earlierAttemptSHA + ":mise.toml": "[tools]\ngo = \"1.28\"\n",
		"branch:" + branch:               earlierAttemptSHA,
	})
	a, b := toolchainActivities(t, nil)
	a.GitHub = gh
	if _, err := ensure(t, a); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(*reads, "mise.toml@"+earlierAttemptSHA) || slices.Contains(*reads, "mise.toml@"+testBaseSHA) {
		t.Errorf("reads = %v; want mise.toml read at the agent branch head only", *reads)
	}
	if got := string(b.builds[0].Spec.(images.Inputs).MiseToml); got != "[tools]\ngo = \"1.28\"\n" {
		t.Errorf("built from %q, want the branch's mise.toml", got)
	}
}
