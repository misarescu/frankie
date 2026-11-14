// Package mergequeue will hold the per-repo merge queue (M6, docs/spec/06). Until
// then it watches change requests that people merge on the provider (D61), so an
// issue whose change request is merged reaches merged and its dependents can start.
package mergequeue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

// ChangeRequestSource reads a change request's state from its provider.
type ChangeRequestSource interface {
	ChangeRequestStatus(ctx context.Context, repo string, number int) (domain.ChangeRequestStatus, error)
}

// Watcher polls the open change requests of issues waiting for a person.
type Watcher struct {
	Store    *store.Store
	Source   ChangeRequestSource
	Interval time.Duration
	Log      *slog.Logger
}

// Run polls until ctx ends.
func (w *Watcher) Run(ctx context.Context) {
	for {
		if err := w.Poll(ctx); err != nil {
			w.Log.Warn("change request watch", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(w.Interval):
		}
	}
}

// Poll checks each watched change request once: a merged one moves its issue to
// merged; one closed without a merge is recorded and its issue left as it is
// (open question 11). Nothing is watched, and nothing asked of the provider,
// unless an issue waits in awaiting_human with an open change request.
func (w *Watcher) Poll(ctx context.Context) error {
	crs, err := w.Store.ListChangeRequestsAwaitingMerge(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, cr := range crs {
		status, err := w.Source.ChangeRequestStatus(ctx, cr.Repo, cr.Number)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s#%d: %w", cr.Repo, cr.Number, err))
			continue
		}
		switch status {
		case domain.ChangeRequestMerged:
			is, err := w.Store.RecordChangeRequestMerged(ctx, cr.ID)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			w.Log.Info("change request merged", "issue", is.Key, "change_request", cr.ProviderRef, "status", is.Status)
		case domain.ChangeRequestClosed:
			if err := w.Store.RecordChangeRequestClosed(ctx, cr.ID); err != nil {
				errs = append(errs, err)
				continue
			}
			w.Log.Info("change request closed without merging", "change_request", cr.ProviderRef)
		}
	}
	return errors.Join(errs...)
}
