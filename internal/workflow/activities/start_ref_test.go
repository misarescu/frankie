package activities

import (
	"slices"
	"testing"
)

const earlierAttemptSHA = "15d3b56dc87b1d230a78cc373b793a9307ad9422"

func TestStartPoint(t *testing.T) {
	branch := BranchName(testInput.IssueKey, testInput.Title)
	tests := []struct {
		name  string
		files map[string]string
		want  StartPoint
	}{
		{"fresh issue starts from the base branch", map[string]string{},
			StartPoint{CloneRef: "main", FilesRef: "main"}},
		{"a re-run continues the issue's agent branch", map[string]string{"branch:" + branch: earlierAttemptSHA},
			StartPoint{CloneRef: branch, FilesRef: earlierAttemptSHA, Continuing: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Activities{GitHub: fakeRepo(t, tt.files)}
			got, err := a.startPoint(t.Context(), testInput)
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
	if got := string(b.builds[0].Inputs.MiseToml); got != "[tools]\ngo = \"1.28\"\n" {
		t.Errorf("built from %q, want the branch's mise.toml", got)
	}
}
