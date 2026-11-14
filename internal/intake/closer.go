package intake

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

// IssueWriter is the part of the IssueSource connector (docs/spec/02) the
// write-back uses (D71).
type IssueWriter interface {
	// Get reads an issue; domain.ErrExternalIssueGone when the source no longer has it.
	Get(ctx context.Context, ref string) (domain.ExternalIssue, error)
	// UpdateStatus mirrors a factory status: merged closes as completed,
	// canceled as not planned.
	UpdateStatus(ctx context.Context, ref string, status domain.IssueStatus) error
	Comment(ctx context.Context, ref, body string) error
}

// closeBatch bounds the provider calls of one pass.
const closeBatch = 20

// Closer closes the provider issues of issues a person completed or canceled
// in the factory (D71). It is an outbox: the store lists what is still to
// close, and an issue is marked once its provider issue is closed, so a failed
// call is retried on the next pass.
type Closer struct {
	Store    *store.Store
	Source   IssueWriter
	Interval time.Duration
	Log      *slog.Logger
}

// Run closes what is due until ctx ends.
func (c *Closer) Run(ctx context.Context) {
	for {
		if err := c.Poll(ctx); err != nil {
			c.Log.Warn("close issues at the source", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(c.Interval):
		}
	}
}

// Poll closes at the source what people closed since the last pass.
func (c *Closer) Poll(ctx context.Context) error {
	due, err := c.Store.ListIssuesToCloseAtSource(ctx, closeBatch)
	if err != nil {
		return err
	}
	var errs []error
	for _, is := range due {
		if err := c.close(ctx, is); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", is.Key, err))
		}
	}
	return errors.Join(errs...)
}

// close comments on an open provider issue saying who closed it and why, then
// closes it as completed or not planned. A provider issue already closed (by
// an earlier pass, or by a person) or gone is left as it is.
func (c *Closer) close(ctx context.Context, is domain.Issue) error {
	ext, err := c.Source.Get(ctx, is.SourceRef)
	switch {
	case errors.Is(err, domain.ErrExternalIssueGone):
		c.Log.Warn("source issue is gone; nothing to close", "issue", is.Key, "ref", is.SourceRef)
	case err != nil:
		return err
	case ext.Open:
		if err := c.Source.Comment(ctx, is.SourceRef, CloseComment(is)); err != nil {
			return err
		}
		if err := c.Source.UpdateStatus(ctx, is.SourceRef, is.Status); err != nil {
			return err
		}
		c.Log.Info("source issue closed", "issue", is.Key, "ref", is.SourceRef, "status", is.Status, "by", is.ClosedBy)
	}
	return c.Store.MarkSourceClosed(ctx, is.ID)
}

// CloseComment is what the provider issue says about its closing: who closed it
// in the factory, and why.
func CloseComment(is domain.Issue) string {
	by := is.ClosedBy
	if by == "" {
		by = "someone"
	}
	if is.Status == domain.IssueCanceled {
		return fmt.Sprintf("Canceled in Frankie by %s (%s): %s", by, is.Key, strings.TrimSpace(is.CloseReason))
	}
	return fmt.Sprintf("Completed in Frankie by %s (%s): all its sub-issues are finished.", by, is.Key)
}
