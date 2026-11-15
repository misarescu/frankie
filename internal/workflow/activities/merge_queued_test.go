package activities

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/connectors/github"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

const (
	queuedHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	queuedBase = "1111111111111111111111111111111111111111"
)

// fakeProvider is a GitHub with one pull request (#9) and branch heads.
type fakeProvider struct {
	mu       sync.Mutex
	branches map[string]string
	pr       string // GET pulls/9
	mergeAns int    // PUT pulls/9/merge status
	merged   []map[string]any
	// onComment sees each comment on #9; closed counts the PATCHes that close it.
	onComment func(body string)
	closed    int
}

func (p *fakeProvider) client(t *testing.T) *github.Client {
	t.Helper()
	const prefix = "/repos/misarescu/Goodreads-Clone/"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		path := strings.TrimPrefix(r.URL.Path, prefix)
		switch {
		case strings.HasPrefix(path, "git/ref/heads/"):
			sha, ok := p.branches[strings.TrimPrefix(path, "git/ref/heads/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = fmt.Fprintf(w, `{"object":{"sha":%q}}`, sha)
		case path == "pulls/9" && r.Method == http.MethodGet:
			_, _ = io.WriteString(w, p.pr)
		case path == "issues/9/comments" && r.Method == http.MethodPost:
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if p.onComment != nil {
				p.onComment(body["body"])
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{}`)
		case path == "pulls/9" && r.Method == http.MethodPatch:
			p.closed++
			p.pr = `{"state":"closed","merged":false}`
			_, _ = io.WriteString(w, `{}`)
		case path == "pulls/9/merge" && r.Method == http.MethodPut:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			p.merged = append(p.merged, body)
			code := p.mergeAns
			if code == 0 {
				code = http.StatusOK
			}
			w.WriteHeader(code)
			if code == http.StatusOK {
				_, _ = io.WriteString(w, `{"sha":"m3rge","merged":true}`)
				p.pr = `{"state":"closed","merged":true,"merge_commit_sha":"m3rge","head":{"sha":"` + queuedHead + `"}}`
				return
			}
			_, _ = io.WriteString(w, `{"message":"refused"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return github.NewClient("pat", srv.URL)
}

type queueFixture struct {
	a     *Activities
	in    IssueRunInput
	run   StartRunResult
	cr    domain.ChangeRequest
	prov  *fakeProvider
	steps int
}

func newQueueActivityFixture(t *testing.T) *queueFixture {
	t.Helper()
	g := newGateFixture(t)
	prov := &fakeProvider{branches: map[string]string{"main": queuedBase, g.run.Branch: queuedHead}, pr: `{"state":"open","head":{"sha":"` + queuedHead + `"}}`}
	g.a.GitHub = prov.client(t)
	g.a.Agent.AuthorName, g.a.Agent.AuthorEmail = "frankie-agent", "agent@frankie.invalid"
	cr, err := g.a.Store.UpsertChangeRequest(t.Context(), store.NewChangeRequest{IssueID: uuid.MustParse(g.in.IssueID), RunID: uuid.MustParse(g.run.RunID),
		Repo: g.in.Repo, Number: 9, ProviderRef: "https://github.com/misarescu/Goodreads-Clone/pull/9", Branch: g.run.Branch, HeadSHA: queuedHead})
	if err != nil {
		t.Fatal(err)
	}
	return &queueFixture{a: g.a, in: g.in, run: g.run, cr: cr, prov: prov}
}

func (f *queueFixture) step(t *testing.T, id string, kind domain.StepKind) string {
	t.Helper()
	f.steps++
	return startStep(t, f.a, StepStart{RunID: f.run.RunID, StepID: id, Kind: kind, Attempt: f.steps})
}

func (f *queueFixture) merge(t *testing.T, base string) (QueueMergeResult, string) {
	t.Helper()
	id := f.step(t, "enqueue-merge", domain.StepAction)
	v, err := activityEnv(f.a).ExecuteActivity(f.a.MergeQueued, QueueMergeRequest{Issue: f.in, StepRunID: id, ChangeRequestID: f.cr.ID.String(),
		Number: 9, HeadSHA: queuedHead, BaseSHA: base, Method: domain.MergeSquash, Title: "gc-1: Add search (#9)"})
	if err != nil {
		t.Fatalf("MergeQueued: %v", err)
	}
	var out QueueMergeResult
	_ = v.Get(&out)
	return out, id
}

// M6 (D87): the queue merges at the checked head while the base branch is
// where the update left it, records the merge, and a retry reports it again.
func TestMergeQueued(t *testing.T) {
	f := newQueueActivityFixture(t)
	out, id := f.merge(t, queuedBase)
	if out.Outcome != QueueMerged || out.MergeSHA != "m3rge" {
		t.Fatalf("merge = %+v", out)
	}
	m := f.prov.merged[0]
	if m["sha"] != queuedHead || m["merge_method"] != "squash" || m["commit_title"] != "gc-1: Add search (#9)" {
		t.Errorf("merge request = %v", m)
	}
	step, _ := f.a.Store.GetStepRun(t.Context(), uuid.MustParse(id))
	if step.Status != domain.ExecSucceeded || step.CommitSHA != "m3rge" {
		t.Errorf("step = %s at %q", step.Status, step.CommitSHA)
	}
	crs, _ := f.a.Store.ListChangeRequestsForIssue(t.Context(), f.cr.IssueID)
	if len(crs) != 1 || crs[0].Status != string(domain.ChangeRequestMerged) {
		t.Errorf("change requests = %+v", crs)
	}
	// Retried after the merge: main moved to the merge, yet it is the same merge.
	f.prov.branches["main"] = "m3rge"
	if again, _ := f.merge(t, queuedBase); again.Outcome != QueueMerged || again.MergeSHA != "m3rge" {
		t.Errorf("retried merge = %+v", again)
	}
}

func TestMergeQueuedDoesNotMerge(t *testing.T) {
	t.Run("the base branch moved", func(t *testing.T) {
		f := newQueueActivityFixture(t)
		out, id := f.merge(t, "2222222222222222222222222222222222222222")
		if out.Outcome != QueueBaseMoved || len(f.prov.merged) != 0 {
			t.Fatalf("merge = %+v, sent %v", out, f.prov.merged)
		}
		if step, _ := f.a.Store.GetStepRun(t.Context(), uuid.MustParse(id)); step.Status != domain.ExecFailed {
			t.Errorf("step = %s", step.Status)
		}
	})
	for name, code := range map[string]int{"head moved": http.StatusConflict, "refused": http.StatusMethodNotAllowed} {
		t.Run(name, func(t *testing.T) {
			f := newQueueActivityFixture(t)
			f.prov.mergeAns = code
			out, _ := f.merge(t, queuedBase)
			want := map[int]string{http.StatusConflict: QueueHeadMoved, http.StatusMethodNotAllowed: QueueRefused}[code]
			if out.Outcome != want || out.Reason == "" {
				t.Fatalf("merge = %+v, want %s", out, want)
			}
			crs, _ := f.a.Store.ListChangeRequestsForIssue(t.Context(), f.cr.IssueID)
			if crs[0].Status != string(domain.ChangeRequestOpen) {
				t.Errorf("change request %s after a refused merge", crs[0].Status)
			}
		})
	}
}
