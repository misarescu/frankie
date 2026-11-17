package activities

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/domain"
)

// ReleaseFailureReport is a release whose verification failed at a stage.
type ReleaseFailureReport struct {
	ReleaseID    string
	Repo         string
	SHA          string
	Stage        string
	URL          string `json:",omitempty"`
	Verification Verification
	RolledBack   bool
}

// bugLabel types the issue for intake (D60): a bug runs the bug-fix workflow
// once someone starts it (D94).
const bugLabel = "bug"

// releaseBugMarker identifies the issue a release opened, so a retry finds it.
func releaseBugMarker(releaseID uuid.UUID) string {
	return "<!-- frankie-release-bug: " + releaseID.String() + " -->"
}

// ReportReleaseFailure opens a bug issue on the repo's provider for a release
// whose verification failed (FR-RL-2, FR-TA-3, D115). It names the release, its
// commit, the stage and whether it was rolled back, the failing tests, and the
// suspects: the release's issues the failing tests are tagged with, or all of
// them when no failing test names one. It carries the intake label and `bug`,
// so intake syncs it as a draft bug that waits for someone to start it. It
// returns the issue's reference, recorded on the release. Idempotent: a release
// opens one issue, found again by its marker if recording it failed.
func (a *Activities) ReportReleaseFailure(ctx context.Context, req ReleaseFailureReport) (string, error) {
	id, err := uuid.Parse(req.ReleaseID)
	if err != nil {
		return "", temporal.NewNonRetryableApplicationError("bad release id", "input", err)
	}
	rel, err := a.Store.GetRelease(ctx, id)
	if err != nil {
		return "", err
	}
	if rel.BugIssueRef != "" {
		return rel.BugIssueRef, nil
	}
	marker := releaseBugMarker(rel.ID)
	open, err := a.GitHub.ListLabeledIssues(ctx, req.Repo, bugLabel)
	if err != nil {
		return "", err
	}
	for _, is := range open {
		if strings.Contains(is.Body, marker) {
			return a.recordBugIssue(ctx, rel.ID, is.Ref)
		}
	}

	issues := make([]domain.Issue, 0, len(rel.IssueIDs))
	for _, issueID := range rel.IssueIDs {
		is, err := a.Store.GetIssue(ctx, issueID)
		if err != nil {
			return "", err
		}
		issues = append(issues, is)
	}
	labels := []string{bugLabel}
	if a.IntakeLabel != "" {
		labels = []string{a.IntakeLabel, bugLabel}
	}
	created, err := a.GitHub.CreateIssue(ctx, req.Repo, domain.NewExternalIssue{
		Title: releaseBugTitle(req), Body: releaseBugBody(req, rel.ID, issues) + "\n" + marker + "\n", Labels: labels,
	})
	if err != nil {
		return "", fmt.Errorf("open the bug issue: %w", err)
	}
	return a.recordBugIssue(ctx, rel.ID, created.Ref)
}

func (a *Activities) recordBugIssue(ctx context.Context, releaseID uuid.UUID, ref string) (string, error) {
	rel, err := a.Store.SetReleaseBugIssue(ctx, releaseID, ref)
	if err != nil {
		return "", err
	}
	return rel.BugIssueRef, nil
}

// suspects splits the issue keys the failing tests are tagged with: those in
// the release are its suspects, the others were released before. When no
// failing test names an issue of the release, every issue in it is a suspect.
func suspects(inRelease []string, failures []TestFailure) (suspect, earlier []string) {
	for _, f := range failures {
		for _, k := range f.IssueKeys {
			switch {
			case slices.Contains(inRelease, k):
				if !slices.Contains(suspect, k) {
					suspect = append(suspect, k)
				}
			case !slices.Contains(earlier, k):
				earlier = append(earlier, k)
			}
		}
	}
	if len(suspect) == 0 {
		suspect = slices.Clone(inRelease)
	}
	return suspect, earlier
}

func releaseBugTitle(req ReleaseFailureReport) string {
	f := req.Verification.Failures
	switch len(f) {
	case 0:
		return fmt.Sprintf("Release %s failed verification on %s", short(req.SHA), req.Stage)
	case 1:
		return fmt.Sprintf("Release %s failed on %s: %s", short(req.SHA), req.Stage, f[0].Test)
	}
	return fmt.Sprintf("Release %s failed on %s: %s and %d more", short(req.SHA), req.Stage, f[0].Test, len(f)-1)
}

func releaseBugBody(req ReleaseFailureReport, releaseID uuid.UUID, issues []domain.Issue) string {
	var b strings.Builder
	outcome := "was not rolled back"
	if req.RolledBack {
		outcome = "was rolled back"
	}
	fmt.Fprintf(&b, "Release `%s` of %s failed verification on **%s** and %s.\n\n", short(req.SHA), req.Repo, req.Stage, outcome)
	if s := strings.TrimSpace(req.Verification.Summary); s != "" {
		fmt.Fprintf(&b, "%s\n\n", s)
	}
	if len(req.Verification.Failures) > 0 {
		b.WriteString("**Failing tests**\n\n| Test | Issues | Failure |\n|---|---|---|\n")
		for _, f := range req.Verification.Failures {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", cell(f.Test), cell(strings.Join(f.IssueKeys, ", ")), cell(f.Message))
		}
		b.WriteString("\n")
	}

	keys := make([]string, 0, len(issues))
	byKey := map[string]domain.Issue{}
	for _, is := range issues {
		keys = append(keys, is.Key)
		byKey[is.Key] = is
	}
	suspect, earlier := suspects(keys, req.Verification.Failures)
	switch {
	case len(keys) == 0:
		b.WriteString("**Suspects**: the release contains no issue Frankie merged.\n\n")
	case len(suspect) == len(keys) && !tagged(keys, req.Verification.Failures):
		b.WriteString("**Suspects**: no failing test is tagged with an issue of this release, so every issue it contains is a suspect:\n\n")
	default:
		b.WriteString("**Suspects**: the issues of this release the failing tests are tagged with:\n\n")
	}
	for _, k := range suspect {
		is := byKey[k]
		if is.SourceRef != "" {
			fmt.Fprintf(&b, "- %s: %s (%s)\n", k, is.Title, is.SourceRef)
		} else {
			fmt.Fprintf(&b, "- %s: %s\n", k, is.Title)
		}
	}
	if len(suspect) > 0 {
		b.WriteString("\n")
	}
	if len(earlier) > 0 {
		fmt.Fprintf(&b, "The failing tests also cover issues released earlier: %s.\n\n", strings.Join(earlier, ", "))
	}

	b.WriteString("**Release**\n\n")
	fmt.Fprintf(&b, "- Commit: https://github.com/%s/commit/%s\n", req.Repo, req.SHA)
	fmt.Fprintf(&b, "- Frankie release: `%s`\n", releaseID)
	if req.URL != "" {
		fmt.Fprintf(&b, "- Stage: %s at %s\n", req.Stage, req.URL)
	}
	b.WriteString("\nOpened by Frankie when the release failed verification. Start it from Frankie's board to run the bug-fix workflow.\n")
	return b.String()
}

// tagged reports whether a failing test names one of keys.
func tagged(keys []string, failures []TestFailure) bool {
	for _, f := range failures {
		for _, k := range f.IssueKeys {
			if slices.Contains(keys, k) {
				return true
			}
		}
	}
	return false
}

// cell makes text safe for one Markdown table cell, at most 300 characters.
func cell(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, "|", `\|`)
	if r := []rune(s); len(r) > 300 {
		s = string(r[:299]) + "…"
	}
	return s
}
