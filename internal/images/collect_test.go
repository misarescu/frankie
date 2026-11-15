package images

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"
)

// memCatalog is a registry with registry:2's delete semantics: deleting a
// manifest removes every tag pointing to it.
type memCatalog struct {
	manifests map[string]map[string]bool // repository -> digests
	tags      map[string]map[string]string
	deleted   []string // repository@digest
	failOn    string   // repository whose deletes fail
}

func newMemCatalog() *memCatalog {
	return &memCatalog{manifests: map[string]map[string]bool{}, tags: map[string]map[string]string{}}
}

func (m *memCatalog) push(repo, tag, digest string) {
	if m.manifests[repo] == nil {
		m.manifests[repo], m.tags[repo] = map[string]bool{}, map[string]string{}
	}
	m.manifests[repo][digest] = true
	if tag != "" {
		m.tags[repo][tag] = digest
	}
}

func (m *memCatalog) Repositories(context.Context) ([]string, error) {
	var repos []string
	for r := range m.manifests {
		repos = append(repos, r)
	}
	slices.Sort(repos)
	return repos, nil
}

func (m *memCatalog) Tags(_ context.Context, repo string) ([]string, error) {
	var tags []string
	for t := range m.tags[repo] {
		tags = append(tags, t)
	}
	slices.Sort(tags)
	return tags, nil
}

func (m *memCatalog) Resolve(_ context.Context, repo, tag string) (string, bool, error) {
	d, ok := m.tags[repo][tag]
	return d, ok, nil
}

func (m *memCatalog) Delete(_ context.Context, repo, digest string) error {
	if repo == m.failOn {
		return errors.New("delete refused")
	}
	m.deleted = append(m.deleted, repo+"@"+digest)
	delete(m.manifests[repo], digest)
	for t, d := range m.tags[repo] {
		if d == digest {
			delete(m.tags[repo], t)
		}
	}
	return nil
}

type memRecords struct{ records []Record }

func (m *memRecords) List(context.Context) ([]Record, error) { return slices.Clone(m.records), nil }

func (m *memRecords) Forget(_ context.Context, repo, tag string) error {
	m.records = slices.DeleteFunc(m.records, func(r Record) bool { return r.Repository == repo && r.Tag == tag })
	return nil
}

type fakeHost struct {
	last     time.Time
	active   bool
	restarts []time.Time
}

func (f *fakeHost) LastCollected(context.Context) (time.Time, error) { return f.last, nil }
func (f *fakeHost) BuildsActive(context.Context) (bool, error)       { return f.active, nil }
func (f *fakeHost) Restart(_ context.Context, at time.Time) error {
	f.restarts = append(f.restarts, at)
	f.last = at
	return nil
}

func dg(n int) string { return fmt.Sprintf("sha256:%064x", n) }

var collectNow = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

func daysAgo(n int) time.Time { return collectNow.Add(-time.Duration(n) * 24 * time.Hour) }

func TestRetain(t *testing.T) {
	records := []Record{
		{"toolchains/o/a", "tc-1", dg(1), daysAgo(30)},
		{"toolchains/o/a", "tc-2", dg(2), daysAgo(20)},
		{"toolchains/o/a", "tc-3", dg(3), daysAgo(10)},
		{"toolchains/o/a", "tc-4", dg(4), daysAgo(0)},
		{"dependencies/o/a", "dep-1", dg(5), daysAgo(40)},
	}
	tests := []struct {
		name   string
		keep   int
		minAge time.Duration
		want   []string // dropped tags
	}{
		{"keep two newest", 2, 0, []string{"tc-1", "tc-2"}},
		{"each repository keeps its own newest", 1, 0, []string{"tc-1", "tc-2", "tc-3"}},
		{"young images are kept beyond keep", 0, 15 * 24 * time.Hour, []string{"dep-1", "tc-1", "tc-2"}},
		{"keep everything", 10, 0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kept, dropped := Retain(records, tt.keep, tt.minAge, collectNow)
			var got []string
			for _, r := range dropped {
				got = append(got, r.Tag)
			}
			slices.Sort(got)
			if !slices.Equal(got, tt.want) {
				t.Errorf("dropped %v, want %v", got, tt.want)
			}
			if len(kept)+len(dropped) != len(records) {
				t.Errorf("kept %d + dropped %d != %d", len(kept), len(dropped), len(records))
			}
		})
	}
}

const testBase = "ghcr.io/misarescu/frankie-runner:sha-new"

func newCollector() (*Collector, *memCatalog, *memRecords, *fakeHost) {
	cat, rec, host := newMemCatalog(), &memRecords{}, &fakeHost{}
	return &Collector{
		Catalog: cat, Records: rec, Host: host, Maintenance: &Maintenance{},
		Base: testBase, Keep: 1, MinAge: time.Hour, Interval: 24 * time.Hour,
		Now: func() time.Time { return collectNow }, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, cat, rec, host
}

// M6 (D84, open question 17): a collection deletes the manifests no kept
// record and no deployed base needs, forgets their records, leaves
// repositories Frankie does not build into alone, and restarts the registry
// so it collects the blobs.
func TestCollectDeletesWhatNothingNeeds(t *testing.T) {
	c, cat, rec, host := newCollector()
	rec.records = []Record{
		{"toolchains/o/a", "tc-old", dg(1), daysAgo(9)},
		{"toolchains/o/a", "tc-new", dg(2), daysAgo(1)},
		// The registry lost this one's tag; its manifest goes anyway.
		{"dependencies/o/a", "dep-old", dg(3), daysAgo(9)},
		{"dependencies/o/a", "dep-new", dg(4), daysAgo(2)},
	}
	cat.push("toolchains/o/a", "tc-old", dg(1))
	cat.push("toolchains/o/a", "tc-new", dg(2))
	cat.push("toolchains/o/a", "tc-stray", dg(9)) // a tag nothing recorded
	cat.push("dependencies/o/a", "", dg(3))
	cat.push("dependencies/o/a", "dep-new", dg(4))
	cat.push("base/frankie-runner", "sha-old", dg(5))
	cat.push("base/frankie-runner", "sha-new", dg(6))
	cat.push("base/other-runner", "v1", dg(7))
	cat.push("experiments/x", "v1", dg(8))

	r, err := c.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(cat.deleted)
	want := []string{
		"base/frankie-runner@" + dg(5), "base/other-runner@" + dg(7),
		"dependencies/o/a@" + dg(3),
		"toolchains/o/a@" + dg(1), "toolchains/o/a@" + dg(9),
	}
	if !slices.Equal(cat.deleted, want) {
		t.Errorf("deleted %v,\nwant %v", cat.deleted, want)
	}
	var tags []string
	for _, r := range rec.records {
		tags = append(tags, r.Tag)
	}
	if strings.Join(tags, ",") != "tc-new,dep-new" {
		t.Errorf("records left %v", tags)
	}
	if r.Deleted != 5 || r.Forgotten != 2 || !r.Restarted || len(host.restarts) != 1 || !host.restarts[0].Equal(collectNow) {
		t.Errorf("report %+v, restarts %v", r, host.restarts)
	}
}

// A shared digest stays when a kept record needs it, and a failed delete keeps
// its repository's records so the next collection tries again.
func TestCollectKeepsSharedDigestsAndRecordsOnFailure(t *testing.T) {
	c, cat, rec, _ := newCollector()
	rec.records = []Record{
		{"toolchains/o/a", "tc-old", dg(1), daysAgo(9)},
		{"toolchains/o/a", "tc-new", dg(1), daysAgo(1)},
		{"toolchains/o/b", "tc-old", dg(2), daysAgo(9)},
		{"toolchains/o/b", "tc-new", dg(3), daysAgo(1)},
	}
	cat.push("toolchains/o/a", "tc-old", dg(1))
	cat.push("toolchains/o/a", "tc-new", dg(1))
	cat.push("toolchains/o/b", "tc-old", dg(2))
	cat.push("toolchains/o/b", "tc-new", dg(3))
	cat.failOn = "toolchains/o/b"

	if _, err := c.Collect(t.Context()); err == nil {
		t.Fatal("a failed delete was not reported")
	}
	if len(cat.deleted) != 0 {
		t.Errorf("deleted %v; the shared digest is kept", cat.deleted)
	}
	if len(rec.records) != 3 || !slices.ContainsFunc(rec.records, func(r Record) bool { return r.Repository == "toolchains/o/b" && r.Tag == "tc-old" }) {
		t.Errorf("records %v: o/b's must stay after its delete failed", rec.records)
	}
}

// Nothing is collected while an image is resolved or built, or a build Job
// runs; Run collects only when the last collection is Interval old.
func TestCollectWaitsForBuilds(t *testing.T) {
	c, cat, _, host := newCollector()
	cat.push("base/frankie-runner", "sha-old", dg(5))

	release, _ := c.Maintenance.Use(t.Context(), nil)
	if r, err := c.Collect(t.Context()); err != nil || r.Skipped == "" {
		t.Errorf("collect during image use = %+v, %v", r, err)
	}
	release()
	host.active = true
	if r, err := c.Collect(t.Context()); err != nil || r.Skipped == "" {
		t.Errorf("collect during a build = %+v, %v", r, err)
	}
	if len(cat.deleted) != 0 || len(host.restarts) != 0 {
		t.Fatalf("collected while builds ran: deleted %v, restarts %v", cat.deleted, host.restarts)
	}
	host.active = false

	host.last = collectNow.Add(-time.Hour)
	if err := c.collectIfDue(t.Context()); err != nil || len(host.restarts) != 0 {
		t.Fatalf("collected an hour after the last one: %v, %v", err, host.restarts)
	}
	host.last = collectNow.Add(-25 * time.Hour)
	if err := c.collectIfDue(t.Context()); err != nil || len(host.restarts) != 1 {
		t.Fatalf("not collected after 25 hours: %v, %v", err, host.restarts)
	}
	host.last, host.restarts = time.Time{}, nil
	if err := c.collectIfDue(t.Context()); err != nil || len(host.restarts) != 1 {
		t.Fatalf("never collected before, yet not collected: %v, %v", err, host.restarts)
	}
}
