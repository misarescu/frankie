package activities

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/connectors/github"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	runnerv1 "github.com/misarescu/frankie/gen/frankie/runner/v1"
	"github.com/misarescu/frankie/internal/domain"
)

func TestEveryRunnerEventTypeMaps(t *testing.T) {
	for v := range runnerv1.EventType_name {
		typ := runnerv1.EventType(v)
		if typ == runnerv1.EventType_EVENT_TYPE_UNSPECIFIED {
			continue
		}
		if _, ok := domainEventTypes[typ]; !ok {
			t.Errorf("%s has no domain event type", typ)
		}
	}
	if domainEventType(runnerv1.EventType_EVENT_TYPE_UNSPECIFIED) != domain.EventError {
		t.Error("unknown types should be recorded as errors")
	}
}

func TestIsPrecondition(t *testing.T) {
	already := status.Error(codes.FailedPrecondition, "a session was already started on this runner")
	prepare := status.Error(codes.FailedPrecondition, "prepare workspace: workspace: git clone: exit status 128")
	if !isPrecondition(already, "already started") {
		t.Error("an earlier attempt's session must count as started")
	}
	if isPrecondition(prepare, "already started") {
		t.Error("a failed workspace clone must not be mistaken for an earlier attempt")
	}
	if isPrecondition(status.Error(codes.Internal, "already started"), "already started") {
		t.Error("only FailedPrecondition counts")
	}
}

// A reviewer's session can read the issue branch but never push it: its token
// grant and its workspace are both read-only, and its tools are the role's.
func TestReviewerSessionIsReadOnly(t *testing.T) {
	a := &Activities{Agent: AgentConfig{Model: "claude-sonnet-5-5", SessionTimeout: time.Hour, GitProxyURL: "http://cp:8081/",
		GatewayURL: "http://gw:4000", AuthorName: "frankie", AuthorEmail: "agent@frankie.invalid"}}
	start := StartPoint{CloneRef: "agent/gc-1/add-search", Continuing: true}

	reviewer := testRun
	reviewer.Step = &AgentStep{StepID: "code-reviewer", SystemPrompt: "review", Contract: "review-verdict",
		AllowedTools: []string{"read", "bash:readonly"}, Model: "claude-opus-5-5"}
	set := a.sessionSettings(reviewer)
	if g := a.sessionGrant(testInput, reviewer, set); !g.ReadOnly || g.IssueKey != "gc-1" {
		t.Errorf("reviewer grant = %+v", g)
	}
	req := a.startRequest(testInput, reviewer, set, runnerv1.Harness_HARNESS_CLAUDE_CODE, start, "tok", "vkey")
	w := req.GetWorkspace()
	if !w.GetReadOnly() || w.GetCompareRef() != "main" || w.GetBaseRef() != "agent/gc-1/add-search" ||
		!slices.Equal(req.GetAllowedTools(), []string{"read", "bash:readonly"}) || req.GetModel() != "claude-opus-5-5" || req.GetSystemPrompt() != "review" {
		t.Errorf("reviewer start = %+v", req)
	}

	coder := testRun
	coder.Step = &AgentStep{StepID: "implement", SystemPrompt: "code"}
	set = a.sessionSettings(coder)
	if g := a.sessionGrant(testInput, coder, set); g.ReadOnly {
		t.Errorf("coder grant = %+v", g)
	}
	req = a.startRequest(testInput, coder, set, runnerv1.Harness_HARNESS_CLAUDE_CODE, start, "tok", "vkey")
	if req.GetWorkspace().GetReadOnly() || len(req.GetAllowedTools()) != 0 || req.GetModel() != "claude-sonnet-5-5" {
		t.Errorf("coder start = %+v", req)
	}

	// IssueRun's step keeps the M2 settings.
	if set := a.sessionSettings(testRun); set.readOnly || set.systemPrompt != SystemPrompt() || set.timeout != time.Hour {
		t.Errorf("IssueRun settings = %+v", set)
	}
}

// A step's harness and model reach the runner with the gateway and the API
// the model is served with; only Claude Code still gets its legacy variables.
func TestStartRequestCarriesHarnessAndGateway(t *testing.T) {
	a := &Activities{Agent: AgentConfig{Model: "claude-sonnet-5-5", SessionTimeout: time.Hour, GatewayURL: "http://gw:4000",
		ModelAPIs: map[string]config.API{"claude-sonnet-5-5": config.APIAnthropic, "openai/gpt-5.6-terra": config.APIOpenAI}}}
	start := StartPoint{CloneRef: "main"}
	tests := []struct {
		name       string
		step       AgentStep
		harness    runnerv1.Harness
		api        runnerv1.ModelAPI
		anthropEnv bool
	}{
		{"default harness", AgentStep{StepID: "implement"}, runnerv1.Harness_HARNESS_CLAUDE_CODE, runnerv1.ModelAPI_MODEL_API_ANTHROPIC, true},
		{"opencode on OpenRouter", AgentStep{StepID: "implement", Harness: "opencode", Model: "openai/gpt-5.6-terra"},
			runnerv1.Harness_HARNESS_OPENCODE, runnerv1.ModelAPI_MODEL_API_OPENAI, false},
		{"opencode on Anthropic", AgentStep{StepID: "implement", Harness: "opencode"},
			runnerv1.Harness_HARNESS_OPENCODE, runnerv1.ModelAPI_MODEL_API_ANTHROPIC, false},
		{"unknown model", AgentStep{StepID: "implement", Harness: "opencode", Model: "gone"},
			runnerv1.Harness_HARNESS_OPENCODE, runnerv1.ModelAPI_MODEL_API_UNSPECIFIED, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := testRun
			run.Step = &tt.step
			set := a.sessionSettings(run)
			h, err := runnerHarness(set.harness)
			if err != nil || h != tt.harness {
				t.Fatalf("harness = %v, %v", h, err)
			}
			req := a.startRequest(testInput, run, set, h, start, "tok", "vkey")
			g := req.GetGateway()
			if req.GetHarness() != tt.harness || g.GetBaseUrl() != "http://gw:4000" || g.GetApiKey() != "vkey" || g.GetApi() != tt.api {
				t.Errorf("start = %+v", req)
			}
			if _, ok := req.GetEnv()["ANTHROPIC_AUTH_TOKEN"]; ok != tt.anthropEnv {
				t.Errorf("env = %v", req.GetEnv())
			}
		})
	}
	if h, err := runnerHarness("pi"); err != nil || h != runnerv1.Harness_HARNESS_PI {
		t.Errorf("pi = %v, %v", h, err)
	}
	if _, err := runnerHarness("aider"); err == nil {
		t.Error("an unknown harness was accepted")
	}
}

// D55: the ask-human tool is offered exactly when the workflow says it relays
// answers.
func TestStartRequestOffersAskHuman(t *testing.T) {
	a := &Activities{Agent: AgentConfig{Model: "claude-sonnet-5-5", SessionTimeout: time.Hour}}
	for _, ask := range []bool{false, true} {
		run := testRun
		run.Step = &AgentStep{StepID: "implement", AskHuman: ask}
		set := a.sessionSettings(run)
		if got := a.startRequest(testInput, run, set, runnerv1.Harness_HARNESS_CLAUDE_CODE, StartPoint{CloneRef: "main"}, "tok", "vkey").GetAskHuman(); got != ask {
			t.Errorf("AskHuman %v: start request asks %v", ask, got)
		}
	}
}

// D55: a question event becomes a pending question of the step; a malformed
// one is skipped rather than failing the session.
func TestOpenQuestionRecordsTheQuestion(t *testing.T) {
	st, in := readyIssue(t)
	a := &Activities{Store: st, Log: quietLog()}
	run := startedRun(t, a, in)
	step := uuid.MustParse(runningAgentStep(t, a, run))
	for _, payload := range []string{`{"question_id":"q1","question":"Which port?"}`, `{"question_id":"q1","question":"Which port?"}`,
		`{"question":"no id"}`, `not json`} {
		if err := a.openQuestion(t.Context(), step, []byte(payload)); err != nil {
			t.Fatalf("%s: %v", payload, err)
		}
	}
	pending, err := st.ListPendingQuestions(t.Context())
	if err != nil || len(pending) != 1 || pending[0].QuestionID != "q1" || pending[0].Text != "Which port?" || pending[0].StepRunID != step {
		t.Errorf("pending = %+v, %v", pending, err)
	}
}

// runningAgentStep starts the run's implement step and marks it running, as
// launching its pod does, and returns its StepRun ID.
func runningAgentStep(t *testing.T, a *Activities, run StartRunResult) string {
	t.Helper()
	val, err := activityEnv(a).ExecuteActivity(a.StartStep, StepStart{RunID: run.RunID, StepID: "implement", Attempt: 1, Kind: domain.StepAgent})
	if err != nil {
		t.Fatal(err)
	}
	var stepRunID string
	if err := val.Get(&stepRunID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.TransitionStepRun(t.Context(), uuid.MustParse(stepRunID), domain.ExecPending, domain.ExecRunning); err != nil {
		t.Fatal(err)
	}
	return stepRunID
}

// D56: a step's prompt gets the questions people answered for its issue.
func TestIssueAnswersReadsTheIssuesAnswers(t *testing.T) {
	st, in := readyIssue(t)
	a := &Activities{Store: st, Log: quietLog()}
	run := startedRun(t, a, in)
	step := uuid.MustParse(runningAgentStep(t, a, run))
	if err := a.openQuestion(t.Context(), step, []byte(`{"question_id":"q1","question":"Which field name?"}`)); err != nil {
		t.Fatal(err)
	}
	if got, err := a.issueAnswers(t.Context(), in.IssueID); err != nil || len(got) != 0 {
		t.Fatalf("before the answer: %+v, %v", got, err)
	}
	if _, err := st.AnswerQuestion(t.Context(), step, "q1", "book_count", "ana"); err != nil {
		t.Fatal(err)
	}
	got, err := a.issueAnswers(t.Context(), in.IssueID)
	if err != nil || len(got) != 1 || got[0].Text != "Which field name?" || got[0].Answer != "book_count" {
		t.Errorf("answers = %+v, %v", got, err)
	}
	if _, err := a.issueAnswers(t.Context(), "not-a-uuid"); err == nil {
		t.Error("a bad issue id was accepted")
	}
}

// M5.2: a retried run whose coder finds the branch's earlier work done goes on
// to review and gate it; a coder that adds nothing to its own run's push, or
// to the base branch, still made no changes.
func TestCarriedOver(t *testing.T) {
	const earlier = "1111111111111111111111111111111111111111"
	changed := []github.ChangedFile{{Filename: ".factory/config.yaml", Status: "modified", Patch: "+egress:"}}
	for name, tt := range map[string]struct {
		files       []github.ChangedFile
		thisRunHad  string // a commit an earlier step of this run recorded
		sha         string
		wantCarried bool
		wantCompare bool
	}{
		"an earlier run's work":         {files: changed, sha: earlier, wantCarried: true, wantCompare: true},
		"this run's own push, again":    {files: changed, thisRunHad: earlier, sha: earlier},
		"nothing over the base branch":  {sha: earlier, wantCompare: true},
		"no commit reported":            {files: changed},
		"another commit earlier in run": {files: changed, thisRunHad: pushedSHA, sha: earlier, wantCarried: true, wantCompare: true},
	} {
		t.Run(name, func(t *testing.T) {
			gh, asked := compareServer(t, tt.files)
			st, in := readyIssue(t)
			a := &Activities{GitHub: gh, Store: st, Log: quietLog()}
			run := startedRun(t, a, in)
			if tt.thisRunHad != "" {
				first := uuid.MustParse(runningAgentStep(t, a, run))
				if _, err := st.SetStepRunCommit(t.Context(), first, tt.thisRunHad); err != nil {
					t.Fatal(err)
				}
			}
			step := uuid.MustParse(startStep(t, a, StepStart{RunID: run.RunID, StepID: "implement", Attempt: 2, Kind: domain.StepAgent}))
			if tt.sha != "" {
				if _, err := st.SetStepRunCommit(t.Context(), step, tt.sha); err != nil {
					t.Fatal(err)
				}
			}
			got, err := a.carriedOver(t.Context(), in, uuid.MustParse(run.RunID), step, tt.sha)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.wantCarried {
				t.Errorf("carried over = %v, want %v", got, tt.wantCarried)
			}
			if wantPath := "/repos/" + in.Repo + "/compare/" + in.BaseBranch + "..." + tt.sha; tt.wantCompare && *asked != wantPath {
				t.Errorf("compared %q, want %q", *asked, wantPath)
			}
		})
	}
}
