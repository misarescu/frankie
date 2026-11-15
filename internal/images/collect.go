package images

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
)

// Record is an image on record: the digest repository:tag was built as.
type Record struct {
	Repository string
	Tag        string
	Digest     string
	BuiltAt    time.Time
}

// Records lists and forgets the images on record (toolchain_images).
type Records interface {
	List(ctx context.Context) ([]Record, error)
	Forget(ctx context.Context, repository, tag string) error
}

// RegistryHost is where the registry runs: it restarts the registry, which
// collects its garbage before it serves again (D84), and knows whether a
// build is running.
type RegistryHost interface {
	// LastCollected returns when the registry was last restarted to collect;
	// zero if never.
	LastCollected(ctx context.Context) (time.Time, error)
	// BuildsActive reports whether a build Job is running, including one
	// started by an earlier control plane.
	BuildsActive(ctx context.Context) (bool, error)
	// Restart restarts the registry, recording at as the collection's time,
	// and returns once it serves again.
	Restart(ctx context.Context, at time.Time) error
}

// Collector deletes the images nothing needs from the in-cluster registry and
// has the registry collect its garbage (D84, open question 17). It manages
// only the repositories Frankie builds into: toolchain and dependency images,
// and the base images mirrored for them.
type Collector struct {
	Catalog     Catalog
	Records     Records
	Host        RegistryHost
	Maintenance *Maintenance
	// Base is the platform base image builds start from; its mirror is kept.
	Base string
	// Keep is how many of the newest images on record each repository keeps.
	Keep int
	// MinAge keeps every image built more recently, e.g. by a run in flight.
	MinAge time.Duration
	// Interval is the time between collections.
	Interval time.Duration
	// Check is how often Run looks whether a collection is due; zero means 10 minutes.
	Check time.Duration
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	Log *slog.Logger
}

// managedPrefixes are the registry repositories Frankie builds and mirrors into.
var managedPrefixes = []string{"toolchains/", "dependencies/", "base/"}

// CollectReport says what a collection did.
type CollectReport struct {
	// Skipped is why the collection did not run, e.g. a build in progress.
	Skipped   string
	Deleted   int // manifests deleted
	Forgotten int // records removed
	Restarted bool
}

func (c *Collector) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Run collects whenever the last collection is Interval old, until ctx ends.
// A collection that cannot start (a build runs) is tried at the next check.
func (c *Collector) Run(ctx context.Context) {
	check := cmp.Or(c.Check, 10*time.Minute)
	for {
		if err := c.collectIfDue(ctx); err != nil {
			c.Log.Warn("registry collection", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(check):
		}
	}
}

func (c *Collector) collectIfDue(ctx context.Context) error {
	last, err := c.Host.LastCollected(ctx)
	if err != nil {
		return err
	}
	if !last.IsZero() && c.now().Sub(last) < c.Interval {
		return nil
	}
	r, err := c.Collect(ctx)
	if r.Skipped != "" {
		c.Log.Info("registry collection postponed", "reason", r.Skipped)
	} else {
		c.Log.Info("registry collected", "manifests_deleted", r.Deleted, "records_forgotten", r.Forgotten, "restarted", r.Restarted)
	}
	return err
}

// Collect runs one collection now, unless an image is in use or a build runs:
// it deletes the manifests no kept record or the deployed base needs, forgets
// the records it deleted, then restarts the registry so it collects the blobs
// and untagged manifests left behind.
func (c *Collector) Collect(ctx context.Context) (CollectReport, error) {
	release, ok := c.Maintenance.TryCollect()
	if !ok {
		return CollectReport{Skipped: "an image is being resolved or built"}, nil
	}
	defer release()
	active, err := c.Host.BuildsActive(ctx)
	if err != nil {
		return CollectReport{}, fmt.Errorf("collect: check builds: %w", err)
	}
	if active {
		return CollectReport{Skipped: "a build job is running"}, nil
	}

	records, err := c.Records.List(ctx)
	if err != nil {
		return CollectReport{}, fmt.Errorf("collect: list records: %w", err)
	}
	keep, drop := Retain(records, c.Keep, c.MinAge, c.now())
	repos, err := c.Catalog.Repositories(ctx)
	if err != nil {
		return CollectReport{}, fmt.Errorf("collect: list repositories: %w", err)
	}

	var report CollectReport
	var errs []error
	failed := map[string]bool{} // repositories with a failed delete keep their records
	for _, repo := range repos {
		if !managed(repo) {
			continue
		}
		n, err := c.collectRepository(ctx, repo, keep, drop)
		report.Deleted += n
		if err != nil {
			failed[repo] = true
			errs = append(errs, err)
		}
	}
	for _, r := range drop {
		if failed[r.Repository] {
			continue
		}
		if err := c.Records.Forget(ctx, r.Repository, r.Tag); err != nil {
			errs = append(errs, fmt.Errorf("collect: forget %s:%s: %w", r.Repository, r.Tag, err))
			continue
		}
		report.Forgotten++
	}
	if err := c.Host.Restart(ctx, c.now()); err != nil {
		errs = append(errs, fmt.Errorf("collect: restart registry: %w", err))
	} else {
		report.Restarted = true
	}
	return report, errors.Join(errs...)
}

// collectRepository deletes repo's manifests that no kept image needs: for a
// base mirror, every one but the deployed base's; otherwise every one but the
// kept records' digests, including the dropped records' own digests, which
// a moved tag may no longer show.
func (c *Collector) collectRepository(ctx context.Context, repo string, keep, drop []Record) (int, error) {
	kept := map[string]bool{}
	var doomed []string
	if strings.HasPrefix(repo, "base/") {
		mirror, tag := MirrorOf(c.Base)
		if repo == mirror {
			d, found, err := c.Catalog.Resolve(ctx, repo, tag)
			if err != nil {
				return 0, fmt.Errorf("collect: %s: %w", repo, err)
			}
			if found {
				kept[d] = true
			}
		}
	} else {
		for _, r := range keep {
			if r.Repository == repo {
				kept[r.Digest] = true
			}
		}
		for _, r := range drop {
			if r.Repository == repo {
				doomed = append(doomed, r.Digest)
			}
		}
	}
	tags, err := c.Catalog.Tags(ctx, repo)
	if err != nil {
		return 0, fmt.Errorf("collect: %s: %w", repo, err)
	}
	for _, tag := range tags {
		d, found, err := c.Catalog.Resolve(ctx, repo, tag)
		if err != nil {
			return 0, fmt.Errorf("collect: %s:%s: %w", repo, tag, err)
		}
		if found {
			doomed = append(doomed, d)
		}
	}
	deleted := 0
	var errs []error
	seen := map[string]bool{}
	for _, d := range doomed {
		if kept[d] || seen[d] {
			continue
		}
		seen[d] = true
		if err := c.Catalog.Delete(ctx, repo, d); err != nil {
			errs = append(errs, fmt.Errorf("collect: %w", err))
			continue
		}
		deleted++
	}
	return deleted, errors.Join(errs...)
}

func managed(repo string) bool {
	return slices.ContainsFunc(managedPrefixes, func(p string) bool { return strings.HasPrefix(repo, p) })
}

// Retain splits the records into those kept and those dropped: each
// repository keeps its keep newest images and every image built less than
// minAge before now.
func Retain(records []Record, keep int, minAge time.Duration, now time.Time) (kept, dropped []Record) {
	sorted := slices.Clone(records)
	slices.SortStableFunc(sorted, func(a, b Record) int {
		return cmp.Or(strings.Compare(a.Repository, b.Repository), b.BuiltAt.Compare(a.BuiltAt), strings.Compare(a.Tag, b.Tag))
	})
	newer := map[string]int{} // repository -> records already seen (newer ones)
	for _, r := range sorted {
		n := newer[r.Repository]
		newer[r.Repository] = n + 1
		if n < keep || now.Sub(r.BuiltAt) < minAge {
			kept = append(kept, r)
		} else {
			dropped = append(dropped, r)
		}
	}
	return kept, dropped
}
