package intake

import (
	"context"
	"fmt"
	"strings"

	"github.com/misarescu/frankie/internal/domain"
)

// IssueSource is the part of the IssueSource connector (docs/spec/02) intake reads.
type IssueSource interface {
	// ListLabeledIssues returns a repo's open issues carrying label.
	ListLabeledIssues(ctx context.Context, repo, label string) ([]domain.ExternalIssue, error)
	// SubIssues returns an issue's sub-issues, open or closed.
	SubIssues(ctx context.Context, repo string, number int) ([]domain.ExternalIssue, error)
	// BlockedBy returns the issues an issue is blocked by, open or closed.
	BlockedBy(ctx context.Context, repo string, number int) ([]domain.ExternalIssue, error)
}

// maxTreeIssues bounds one project's tree per poll.
const maxTreeIssues = 500

// treeNode is one issue of the tree with its relations inside the tree.
type treeNode struct {
	issue     domain.ExternalIssue
	labeled   bool
	parent    string   // ref of the issue that listed this one as a sub-issue
	children  []string // refs
	blockedBy []string // refs
}

// tree is what intake syncs for a project (D60): the open issues carrying the
// label, their open sub-issues all the way down, and the open issues of the same
// repo any of them is blocked by (with their sub-issues and blockers in turn), so
// no dependency points outside the factory's store.
type tree struct {
	order []string // refs in discovery order
	nodes map[string]*treeNode
}

// collectTree walks the source from the labeled issues. It fails if any listing
// fails: a partial tree would drop parents and dependencies the source still has.
func collectTree(ctx context.Context, src IssueSource, repo, label string) (*tree, error) {
	labeled, err := src.ListLabeledIssues(ctx, repo, label)
	if err != nil {
		return nil, fmt.Errorf("list %s issues labeled %s: %w", repo, label, err)
	}
	t := &tree{nodes: map[string]*treeNode{}}
	var queue []string
	add := func(e domain.ExternalIssue) (string, error) {
		if _, ok := t.nodes[e.Ref]; !ok {
			if len(t.nodes) >= maxTreeIssues {
				return "", fmt.Errorf("%s: more than %d issues to sync", repo, maxTreeIssues)
			}
			t.nodes[e.Ref] = &treeNode{issue: e}
			t.order = append(t.order, e.Ref)
			queue = append(queue, e.Ref)
		}
		return e.Ref, nil
	}
	for _, e := range labeled {
		if _, err := add(e); err != nil {
			return nil, err
		}
		t.nodes[e.Ref].labeled = true
	}
	for len(queue) > 0 {
		n := t.nodes[queue[0]]
		queue = queue[1:]
		if n.issue.SubIssues > 0 {
			subs, err := src.SubIssues(ctx, repo, n.issue.Number)
			if err != nil {
				return nil, fmt.Errorf("sub-issues of %s: %w", n.issue.Ref, err)
			}
			for _, sub := range subs {
				if !sub.Open || !sameRepo(sub.Repo, repo) {
					continue
				}
				ref, err := add(sub)
				if err != nil {
					return nil, err
				}
				t.nodes[ref].parent = n.issue.Ref
				n.children = append(n.children, ref)
			}
		}
		if n.issue.OpenBlockers > 0 {
			blockers, err := src.BlockedBy(ctx, repo, n.issue.Number)
			if err != nil {
				return nil, fmt.Errorf("blockers of %s: %w", n.issue.Ref, err)
			}
			for _, b := range blockers {
				// A closed blocker is done; one in another repo is not this project's.
				if !b.Open || !sameRepo(b.Repo, repo) {
					continue
				}
				ref, err := add(b)
				if err != nil {
					return nil, err
				}
				n.blockedBy = append(n.blockedBy, ref)
			}
		}
	}
	return t, nil
}

// issueType is the node's type: from the source, else from its place in the tree (D60).
func (t *tree) issueType(n *treeNode) domain.IssueType {
	if typ, ok := domain.SourceIssueType(n.issue); ok {
		return typ
	}
	grandchildren := false
	for _, c := range n.children {
		grandchildren = grandchildren || t.nodes[c].issue.SubIssues > 0
	}
	return domain.InferIssueType(n.issue.SubIssues > 0, grandchildren)
}

func sameRepo(a, b string) bool { return strings.EqualFold(a, b) }
