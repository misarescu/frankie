package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

// eventJSON is an event as the API returns it.
type eventJSON struct {
	Seq       int64           `json:"seq"`
	Time      time.Time       `json:"time"`
	Type      string          `json:"type"`
	StepRunID *uuid.UUID      `json:"step_run_id,omitempty"`
	Payload   json.RawMessage `json:"payload"`
}

func toJSON(e domain.Event) eventJSON {
	return eventJSON{Seq: e.Seq, Time: e.Ts, Type: string(e.Type), StepRunID: e.StepRunID, Payload: e.Payload}
}

func (s *Server) runID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid run id", http.StatusBadRequest)
		return uuid.UUID{}, false
	}
	if _, err := s.Store.GetRun(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
		} else {
			http.Error(w, "store error", http.StatusInternalServerError)
		}
		return uuid.UUID{}, false
	}
	return id, true
}

// listEvents replays a run's transcript after ?after=<seq> (FR-OB-2).
func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	id, ok := s.runID(w, r)
	if !ok {
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = 500
	}
	events, err := s.Store.ListRunEvents(r.Context(), id, after, limit)
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	out := struct {
		Events []eventJSON `json:"events"`
		Next   int64       `json:"next"`
	}{Events: []eventJSON{}, Next: after}
	for _, e := range events {
		out.Events = append(out.Events, toJSON(e))
		out.Next = e.Seq
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// streamEvents is a server-sent event stream of a run's transcript: stored
// events first, then live ones, resuming after Last-Event-ID (FR-OB-1). It ends
// with an "end" event once the run is finished and every event was sent.
func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request) {
	id, ok := s.runID(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	after, _ := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)
	if q := r.URL.Query().Get("after"); q != "" {
		after, _ = strconv.ParseInt(q, 10, 64)
	}
	wake, cancel := s.Hub.Subscribe(id.String()) // before the first read: no event can slip between
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	ctx := r.Context()
	for {
		for {
			events, err := s.Store.ListRunEvents(ctx, id, after, 500)
			if err != nil {
				return
			}
			for _, e := range events {
				data, _ := json.Marshal(toJSON(e))
				_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.Seq, e.Type, data)
				after = e.Seq
			}
			if len(events) < 500 {
				break
			}
		}
		flusher.Flush()
		run, err := s.Store.GetRun(ctx, id)
		if err != nil {
			return
		}
		if run.Status.Terminal() {
			// Final check: events committed with the terminal transition are already read.
			if more, err := s.Store.ListRunEvents(ctx, id, after, 1); err == nil && len(more) == 0 {
				_, _ = fmt.Fprintf(w, "event: end\ndata: {\"status\":%q}\n\n", run.Status)
				flusher.Flush()
				return
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-time.After(15 * time.Second):
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

const (
	defaultRunsLimit = 50
	maxRunsLimit     = 200
)

// listRuns returns recent runs, newest first. The optional limit query
// parameter is a positive integer up to maxRunsLimit (default 50).
func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	limit := defaultRunsLimit
	if v, ok := r.URL.Query()["limit"]; ok {
		n, err := strconv.Atoi(v[0])
		if err != nil || n < 1 || n > maxRunsLimit {
			http.Error(w, "limit must be an integer from 1 to 200", http.StatusBadRequest)
			return
		}
		limit = n
	}
	runs, err := s.Store.ListRecentRuns(r.Context(), limit)
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(runs)
}
