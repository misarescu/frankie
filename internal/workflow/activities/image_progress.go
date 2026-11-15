package activities

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/images"
	"github.com/misarescu/frankie/internal/store"
)

// stepProgress records an image build a step waits for on the step's stream
// (D93): that it builds, its log, and how it ended. Recording is best effort:
// a build does not fail because its progress could not be written.
type stepProgress struct {
	ctx       context.Context
	a         *Activities
	runID     uuid.UUID
	stepRunID uuid.UUID
}

var _ images.Progress = stepProgress{}

// progressFor is the Progress of a step, or nil when there is none to record on.
func (a *Activities) progressFor(ctx context.Context, runID, stepRunID uuid.UUID) images.Progress {
	if runID == uuid.Nil || stepRunID == uuid.Nil {
		return nil
	}
	return stepProgress{ctx: ctx, a: a, runID: runID, stepRunID: stepRunID}
}

func (p stepProgress) Building(repository, tag string) {
	p.record(domain.EventImageBuild, domain.ImageBuild{Image: imageKind(repository), Repository: repository, Tag: tag, Status: domain.ImageBuilding})
}

func (p stepProgress) Output(text string) {
	p.record(domain.EventBuildLog, map[string]string{"output": text})
}

func (p stepProgress) Built(repository, tag string, took time.Duration, err error) {
	b := domain.ImageBuild{Image: imageKind(repository), Repository: repository, Tag: tag, Status: domain.ImageBuilt, Seconds: took.Round(time.Second).Seconds()}
	if err != nil {
		b.Status, b.Error = domain.ImageFailed, err.Error()
	}
	p.record(domain.EventImageBuild, b)
}

func (p stepProgress) record(typ domain.EventType, payload any) {
	raw, err := json.Marshal(payload)
	if err == nil {
		_, err = p.a.Store.AppendEvent(context.WithoutCancel(p.ctx), store.NewEvent{RunID: &p.runID, StepRunID: &p.stepRunID,
			Ts: time.Now(), Type: typ, Payload: raw})
	}
	if err != nil {
		p.a.Log.Warn("image build progress not recorded", "step", p.stepRunID, "event", typ, "error", err)
	}
}

// imageKind names the image a registry repository holds.
func imageKind(repository string) string {
	if strings.HasPrefix(repository, "dependencies/") {
		return "dependencies"
	}
	return "toolchain"
}
