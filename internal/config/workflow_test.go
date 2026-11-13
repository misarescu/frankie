package config

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/domain"
)

// The spec's example (docs/spec/03), with the harnesses available in M3.
const specExample = `
name: default-build
version: 1
steps:
  - parallel:
      - id: implement
        agent: { role: coder, harness: claude-code, model: anthropic/claude-opus-5-5 }
      - id: e2e-tests
        agent: { role: e2e-test-writer, model: anthropic/claude-sonnet-5-5 }
        inputs: [spec, acceptance_criteria]
  - id: checks
    gate:
      type: command
      run: ["mise run lint", "mise run test"]
      policy: blocking
      onFail: { goto: implement, maxLoops: 3 }
  - id: review
    parallel:
      - agent: { role: security-reviewer }
      - agent: { role: code-reviewer@3 }
    gate:
      type: agent-verdict
      require: all
      policy: blocking
      onFail: { goto: implement, maxLoops: 2 }
  - id: approve
    gate: { type: human, timeout: 24h }
  - id: open
    action: change-request.open
`

func TestParseWorkflowSpecExample(t *testing.T) {
	w, err := ParseWorkflow([]byte(specExample))
	if err != nil {
		t.Fatal(err)
	}
	if w.Name != "default-build" || w.Version != 1 || len(w.Steps) != 5 {
		t.Fatalf("w = %+v", w)
	}
	t.Run("kinds", func(t *testing.T) {
		var kinds []StepKind
		for _, s := range w.Steps {
			kinds = append(kinds, s.Kind())
		}
		want := []StepKind{StepParallel, StepGate, StepParallel, StepGate, StepAction}
		if !slices.Equal(kinds, want) {
			t.Errorf("kinds = %v, want %v", kinds, want)
		}
	})
	t.Run("branches without an id are named after their role", func(t *testing.T) {
		b := w.Steps[2].Parallel
		if b[0].ID != "security-reviewer" || b[1].ID != "code-reviewer" {
			t.Errorf("branch ids = %q, %q", b[0].ID, b[1].ID)
		}
	})
	t.Run("goto into a parallel group", func(t *testing.T) {
		if o := *w.Steps[1].Gate.OnFail; o != (OnFail{Action: OnFailGoto, Goto: "implement", MaxLoops: 3}) {
			t.Errorf("onFail = %+v", o)
		}
	})
	t.Run("defaults", func(t *testing.T) {
		impl := w.Steps[0].Parallel[0]
		if impl.Agent.Harness != DefaultHarness || !slices.Equal(impl.Inputs, stepInputs) {
			t.Errorf("implement = %+v inputs %v", impl.Agent, impl.Inputs)
		}
		if e2e := w.Steps[0].Parallel[1]; !slices.Equal(e2e.Inputs, []string{"spec", "acceptance_criteria"}) {
			t.Errorf("e2e-tests inputs = %v (explicit inputs must be kept)", e2e.Inputs)
		}
		human := w.Steps[3].Gate
		if human.Policy != domain.GateBlocking || human.OnTimeout != OnTimeoutFail || human.Timeout != Duration(24*time.Hour) ||
			human.OnFail.Action != OnFailFail {
			t.Errorf("human gate = %+v", human)
		}
	})
}

func TestParseWorkflowDefaults(t *testing.T) {
	w, err := ParseWorkflow([]byte(`
name: minimal
version: 2
steps:
  - id: implement
    agent: { role: coder }
  - id: checks
    gate: { type: command }
  - id: approve
    gate: { type: human }
  - id: review
    parallel: [{agent: {role: code-reviewer}}]
    gate: { type: agent-verdict, policy: advisory }
`))
	if err != nil {
		t.Fatal(err)
	}
	checks, human, review := w.Steps[1].Gate, w.Steps[2].Gate, w.Steps[3].Gate
	if !slices.Equal(checks.Run, []string{"mise run lint", "mise run test"}) || checks.Policy != domain.GateBlocking ||
		checks.OnFail == nil || checks.OnFail.Action != OnFailFail {
		t.Errorf("command gate = %+v", checks)
	}
	if human.Timeout != DefaultHumanTimeout || human.OnTimeout != OnTimeoutFail {
		t.Errorf("human gate = %+v", human)
	}
	if review.Require != RequireAll || review.OnFail != nil {
		t.Errorf("advisory agent-verdict gate = %+v (no onFail default for advisory gates)", review)
	}
}

// Invariant 4: the parsed definition is what gets snapshotted, so it must
// survive a JSON round trip unchanged.
func TestWorkflowSnapshotRoundTrip(t *testing.T) {
	w, err := ParseWorkflow([]byte(specExample))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	var back Workflow
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(w, back) {
		t.Fatalf("round trip changed the definition:\n%s", b)
	}
	if err := back.Validate(); err != nil {
		t.Fatal(err)
	}
}

// FR-WF-5: invalid definitions are rejected at save time.
func TestParseWorkflowRejects(t *testing.T) {
	const head = "name: w\nversion: 1\nsteps:\n"
	const impl = "  - id: implement\n    agent: {role: coder}\n"
	const checks = "  - id: checks\n    gate: {type: command}\n"
	tests := []struct {
		name, yaml, want string
	}{
		{"unknown field", head + impl + "    retries: 3\n", "unknown field"},
		{"bad name", "name: Default_Build\nversion: 1\nsteps:\n" + impl, "name:"},
		{"no version", "name: w\nsteps:\n" + impl, "version: must be at least 1"},
		{"no steps", head, "at least one step"},
		{"two kinds", head + "  - id: x\n    agent: {role: coder}\n    action: change-request.open\n", "exactly one of"},
		{"agent with gate", head + "  - id: x\n    agent: {role: coder}\n    gate: {type: command}\n", "exactly one of"},
		{"missing id", head + "  - agent: {role: coder}\n", "steps[0].id: is required"},
		{"bad id", head + "  - id: Implement\n    agent: {role: coder}\n", "must be lowercase"},
		{"duplicate id", head + impl + impl, `duplicate step id "implement"`},
		{"branch duplicates earlier step", head + impl + "  - parallel: [{agent: {role: implement}}]\n", `duplicate step id "implement"`},
		{"duplicate branches", head + "  - parallel: [{agent: {role: coder}}, {agent: {role: coder}}]\n", `duplicate step id "coder"`},
		{"gated group without id", head + impl + checks + "  - parallel: [{agent: {role: code-reviewer}}]\n    gate: {type: agent-verdict}\n", "steps[2].id: is required"},
		{"empty parallel", head + "  - id: p\n    parallel: []\n", "at least one branch"},
		{"non-agent branch", head + "  - parallel: [{id: g, gate: {type: command}}]\n", "branches must be agent steps"},
		{"bad role", head + "  - id: x\n    agent: {role: coder@latest}\n", "role name"},
		{"unknown harness", head + "  - id: x\n    agent: {role: coder, harness: aider}\n", `unknown harness "aider"`},
		{"harness not yet available", head + "  - id: x\n    agent: {role: coder, harness: opencode}\n", `"opencode" is not available yet`},
		{"model without provider", head + "  - id: x\n    agent: {role: coder, model: opus}\n", "model ref"},
		{"provider the harness cannot reach", head + "  - id: x\n    agent: {role: coder, model: openai/gpt-x}\n", `harness claude-code cannot use provider "openai"`},
		{"unknown input", head + "  - id: x\n    agent: {role: coder}\n    inputs: [secrets]\n", `unknown input "secrets"`},
		{"inputs on a gate", head + "  - id: g\n    inputs: [spec]\n    gate: {type: command}\n", "apply to agent steps only"},
		{"bad timeout", head + "  - id: x\n    agent: {role: coder}\n    timeout: 90\n", "duration"},
		{"unknown action", head + "  - id: x\n    action: deploy.prod\n", `unknown action "deploy.prod"`},
		{"action not yet available", head + "  - id: x\n    action: merge-queue.enqueue\n", `"merge-queue.enqueue" is not available yet`},
		{"unknown gate type", head + "  - id: g\n    gate: {type: vibes}\n", `unknown gate type "vibes"`},
		{"unknown policy", head + "  - id: g\n    gate: {type: command, policy: maybe}\n", `unknown policy "maybe"`},
		{"run on a human gate", head + "  - id: g\n    gate: {type: human, run: [make]}\n", "run: applies to command gates only"},
		{"empty command", head + "  - id: g\n    gate: {type: command, run: [\"mise run lint\", \" \"]}\n", "run[1]: is empty"},
		{"require on a command gate", head + "  - id: g\n    gate: {type: command, require: any}\n", "require: applies to agent-verdict"},
		{"bad require", head + checks + "  - id: r\n    parallel: [{agent: {role: code-reviewer}}]\n    gate: {type: agent-verdict, require: most}\n", "must be all, any or majority"},
		{"standalone agent-verdict", head + checks + "  - id: r\n    gate: {type: agent-verdict}\n", "put it on a parallel step"},
		{"LLM gate before deterministic gate", head + impl + "  - id: r\n    parallel: [{agent: {role: code-reviewer}}]\n    gate: {type: agent-verdict}\n" + checks, "add a command gate before it"},
		{"onTimeout on a command gate", head + "  - id: g\n    gate: {type: command, onTimeout: fail}\n", "onTimeout: applies to human gates only"},
		{"escalating human timeout", head + "  - id: g\n    gate: {type: human, onTimeout: escalate}\n", "must be fail or extend"},
		{"advisory human gate", head + "  - id: g\n    gate: {type: human, policy: advisory}\n", "always blocking"},
		{"human gate loops", head + impl + "  - id: g\n    gate: {type: human, onFail: {goto: implement, maxLoops: 1}}\n", "a rejected human gate fails the run"},
		{"advisory gate with onFail", head + "  - id: g\n    gate: {type: command, policy: advisory, onFail: fail}\n", "advisory gate does not stop the run"},
		{"unknown onFail", head + "  - id: g\n    gate: {type: command, onFail: retry}\n", "must be fail, escalate or {goto, maxLoops}"},
		{"onFail object with unknown field", head + impl + "  - id: g\n    gate: {type: command, onFail: {goto: implement, max: 2}}\n", "unknown field"},
		{"goto without bound", head + impl + "  - id: g\n    gate: {type: command, onFail: {goto: implement}}\n", "maxLoops: must be between 1 and 10"},
		{"goto with huge bound", head + impl + "  - id: g\n    gate: {type: command, onFail: {goto: implement, maxLoops: 50}}\n", "maxLoops: must be between 1 and 10"},
		{"goto unknown step", head + impl + "  - id: g\n    gate: {type: command, onFail: {goto: nowhere, maxLoops: 1}}\n", `"nowhere" is not an earlier step`},
		{"goto later step", head + "  - id: g\n    gate: {type: command, onFail: {goto: implement, maxLoops: 1}}\n" + impl, `"implement" is not an earlier step`},
		{"goto itself", head + "  - id: g\n    gate: {type: command, onFail: {goto: g, maxLoops: 1}}\n", `"g" is not an earlier step`},
		{"goto own group", head + checks + "  - id: r\n    parallel: [{agent: {role: code-reviewer}}]\n    gate: {type: agent-verdict, onFail: {goto: code-reviewer, maxLoops: 1}}\n", `"code-reviewer" is not an earlier step`},
		{"goto a gate", head + impl + checks + "  - id: g\n    gate: {type: command, onFail: {goto: checks, maxLoops: 1}}\n", "loops go back to an agent or parallel step"},
		{"fail with goto fields", head + impl + "  - id: g\n    gate: {type: command, onFail: {action: fail, goto: implement}}\n", "goto and maxLoops apply to loops only"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseWorkflow([]byte(tt.yaml))
			if err == nil {
				t.Fatalf("accepted:\n%s", tt.yaml)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v\nwant it to contain %q", err, tt.want)
			}
		})
	}
}

func TestParseWorkflowReportsEveryError(t *testing.T) {
	_, err := ParseWorkflow([]byte("name: w\nversion: 0\nsteps:\n  - id: a\n    action: nope\n  - id: b\n    agent: {role: coder, harness: pi}\n"))
	if err == nil {
		t.Fatal("accepted")
	}
	for _, want := range []string{"version:", "steps[0].action", "steps[1].agent.harness"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v\nmissing %q", err, want)
		}
	}
}
