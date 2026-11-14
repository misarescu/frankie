package domain

import (
	"errors"
	"fmt"
	"strings"
)

// ErrCannotClose reports an issue that cannot be completed or canceled as asked.
var ErrCannotClose = errors.New("cannot close issue")

// maxCloseReason bounds a cancel reason; it is also posted on the provider issue.
const maxCloseReason = 2000

// Finished reports whether an issue is done with: on the base branch, released,
// or dropped.
func (s IssueStatus) Finished() bool {
	return s == IssueMerged || s == IssueReleased || s == IssueCanceled
}

// CheckComplete says whether a person may complete a parent (D70): it is a
// draft or ready (parents do not run, so they stay there), it has children,
// every child is finished, and at least one was not canceled. Completing moves
// it to merged, the one way a parent gets there; a parent with only canceled
// children is canceled instead.
func CheckComplete(parent Issue, children []Issue) error {
	if parent.Status != IssueDraft && parent.Status != IssueReady {
		return fmt.Errorf("%w: %s is %s", ErrCannotClose, parent.Key, parent.Status)
	}
	if len(children) == 0 {
		return fmt.Errorf("%w: %s has no sub-issues; only a parent is completed, a leaf is merged by its change request", ErrCannotClose, parent.Key)
	}
	var open []string
	allCanceled := true
	for _, c := range children {
		if !c.Status.Finished() {
			open = append(open, c.Key+" ("+string(c.Status)+")")
		}
		if c.Status != IssueCanceled {
			allCanceled = false
		}
	}
	if len(open) > 0 {
		return fmt.Errorf("%w: %s still has unfinished sub-issues: %s", ErrCannotClose, parent.Key, strings.Join(open, ", "))
	}
	if allCanceled {
		return fmt.Errorf("%w: every sub-issue of %s was canceled; cancel it instead", ErrCannotClose, parent.Key)
	}
	return nil
}

// CheckCancel says whether a person may cancel an issue (D70): it is not
// finished, and a reason says why.
func CheckCancel(is Issue, reason string) error {
	switch r := strings.TrimSpace(reason); {
	case is.Status.Finished():
		return fmt.Errorf("%w: %s is already %s", ErrCannotClose, is.Key, is.Status)
	case r == "":
		return fmt.Errorf("%w: say why %s is canceled", ErrCannotClose, is.Key)
	case len(r) > maxCloseReason:
		return fmt.Errorf("%w: the reason is longer than %d bytes", ErrCannotClose, maxCloseReason)
	}
	return nil
}

// ToCancel picks what canceling an issue cancels (D70): the issue and every
// unfinished issue in its subtree, which is listed root first. Finished
// descendants keep their status.
func ToCancel(subtree []Issue) []Issue {
	var out []Issue
	for _, is := range subtree {
		if !is.Status.Finished() {
			out = append(out, is)
		}
	}
	return out
}
