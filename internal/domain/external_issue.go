package domain

import "strings"

// ExternalIssue is an issue as an issue source reports it (docs/spec/02,
// IssueSource), free of provider types (invariant 7).
type ExternalIssue struct {
	Ref    string // the source's stable reference, e.g. a GitHub issue URL; becomes source_ref
	Repo   string // repository the issue lives in, "owner/name"
	Number int
	Title  string
	Body   string
	Author string
	Labels []string
	// SourceType is the source's own issue type (e.g. a GitHub issue type), if any.
	SourceType string
	Open       bool
	// SubIssues counts the issue's sub-issues, open or closed; OpenBlockers counts
	// the open issues it is blocked by. Zero means there is nothing to fetch.
	SubIssues    int
	OpenBlockers int
}

// SourceIssueType reads an issue's type from its source (D60): a label or source
// type named epic, story, task or bug (any case) decides; ok is false when none does.
func SourceIssueType(e ExternalIssue) (t IssueType, ok bool) {
	for _, name := range append([]string{e.SourceType}, e.Labels...) {
		if t := IssueType(strings.ToLower(strings.TrimSpace(name))); t.Valid() {
			return t, true
		}
	}
	return "", false
}

// InferIssueType is the type of an issue its source does not type (D60), from
// its place in the tree: an issue with grandchildren is an epic, one with
// children a story, any other a task.
func InferIssueType(hasChildren, hasGrandchildren bool) IssueType {
	switch {
	case hasGrandchildren:
		return IssueEpic
	case hasChildren:
		return IssueStory
	}
	return IssueTask
}
