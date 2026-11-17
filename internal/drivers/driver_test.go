package drivers_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/drivers"
)

// releaseRequest is a valid request deploying a release to staging.
func releaseRequest() drivers.Request {
	return drivers.Request{
		Stage:      drivers.Stage{Name: "staging", Config: json.RawMessage(`{"path":"staging"}`)},
		Subject:    drivers.Subject{Repo: "misarescu/toy", SHA: "abc123", ReleaseID: uuid.New()},
		Deployment: drivers.Deployment{ID: uuid.New(), State: drivers.EmptyState},
	}
}

// previewRequest is a valid request deploying change request 7 to a preview.
func previewRequest() drivers.Request {
	r := releaseRequest()
	r.Stage.Name = "preview"
	r.Subject.ReleaseID, r.Subject.ChangeRequest = uuid.Nil, 7
	return r
}

// M7, D112.
func TestCapabilitiesMissing(t *testing.T) {
	all := drivers.Capabilities{Rollback: true, Progressive: true, Health: true, Preview: true}
	tests := []struct {
		have, need drivers.Capabilities
		want       []string
	}{
		{drivers.Capabilities{}, drivers.Capabilities{}, nil},
		{all, all, nil},
		{drivers.Capabilities{}, all, []string{"rollback", "progressive", "health", "preview"}},
		{drivers.Capabilities{Rollback: true, Health: true}, drivers.Capabilities{Rollback: true, Progressive: true}, []string{"progressive"}},
	}
	for _, tt := range tests {
		if got := tt.have.Missing(tt.need); !slices.Equal(got, tt.want) {
			t.Errorf("%+v.Missing(%+v) = %v, want %v", tt.have, tt.need, got, tt.want)
		}
	}
}

func TestRequestValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*drivers.Request)
		ok     bool
	}{
		{"release", func(*drivers.Request) {}, true},
		{"preview", func(r *drivers.Request) { *r = previewRequest() }, true},
		{"no stage", func(r *drivers.Request) { r.Stage.Name = " " }, false},
		{"config not an object", func(r *drivers.Request) { r.Stage.Config = json.RawMessage(`[]`) }, false},
		{"no config", func(r *drivers.Request) { r.Stage.Config = nil }, false},
		{"no deployment", func(r *drivers.Request) { r.Deployment.ID = uuid.Nil }, false},
		{"state not an object", func(r *drivers.Request) { r.Deployment.State = json.RawMessage(`null`) }, false},
		{"no repo", func(r *drivers.Request) { r.Subject.Repo = "" }, false},
		{"no commit", func(r *drivers.Request) { r.Subject.SHA = "" }, false},
		{"neither release nor change request", func(r *drivers.Request) { r.Subject.ReleaseID = uuid.Nil }, false},
		{"both", func(r *drivers.Request) { r.Subject.ChangeRequest = 3 }, false},
		{"negative change request", func(r *drivers.Request) { r.Subject.ReleaseID, r.Subject.ChangeRequest = uuid.Nil, -1 }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := releaseRequest()
			tt.mutate(&r)
			if err := r.Validate(); (err == nil) != tt.ok {
				t.Errorf("Validate() = %v, want ok %v", err, tt.ok)
			}
		})
	}
}

func TestPhaseValid(t *testing.T) {
	for p, want := range map[drivers.Phase]bool{
		drivers.PhaseProgressing: true, drivers.PhaseReady: true, drivers.PhaseFailed: true, "": false, "done": false,
	} {
		if got := p.Valid(); got != want {
			t.Errorf("%q.Valid() = %v, want %v", p, got, want)
		}
	}
}

func TestValidateObject(t *testing.T) {
	for raw, ok := range map[string]bool{`{}`: true, `{"a":1}`: true, `[]`: false, `null`: false, `"x"`: false, `{`: false, ``: false} {
		if err := drivers.ValidateObject(json.RawMessage(raw)); (err == nil) != ok {
			t.Errorf("ValidateObject(%q) = %v, want ok %v", raw, err, ok)
		}
	}
}
