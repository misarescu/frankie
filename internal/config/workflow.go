package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/misarescu/frankie/internal/domain"
)

// Workflow is a workflow definition (docs/spec/03-workflows-and-gates.md): the
// steps the generic interpreter runs for an issue. ParseWorkflow fills in every
// default, so the definition snapshotted into a run's input (invariant 4) is
// explicit and does not depend on the defaults of a later control plane.
//
// The JSON form of these types is part of recorded workflow histories: renaming
// or retyping a field breaks replay of runs that snapshotted the old form.
type Workflow struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
	Steps   []Step `json:"steps"`
}

// StepKind is what a step does.
type StepKind string

// Step kinds.
const (
	StepAgent    StepKind = "agent"
	StepGate     StepKind = "gate"
	StepParallel StepKind = "parallel"
	StepAction   StepKind = "action"
)

// Step is one entry of a workflow. Exactly one of Agent, Parallel, Action or a
// standalone Gate is set; a parallel group may also carry a trailing Gate that
// is evaluated after all its branches finish.
type Step struct {
	ID        string   `json:"id,omitempty"`
	Agent     *Agent   `json:"agent,omitempty"`
	Inputs    []string `json:"inputs,omitempty"`
	Timeout   Duration `json:"timeout,omitempty"`
	BudgetUSD float64  `json:"budgetUSD,omitempty"`
	Parallel  []Step   `json:"parallel,omitempty"`
	Gate      *Gate    `json:"gate,omitempty"`
	Action    string   `json:"action,omitempty"`
}

// Kind reports the step's kind, or "" when the fields do not form exactly one kind.
func (s Step) Kind() StepKind {
	var kinds []StepKind
	if s.Agent != nil {
		kinds = append(kinds, StepAgent)
	}
	if s.Parallel != nil {
		kinds = append(kinds, StepParallel)
	}
	if s.Action != "" {
		kinds = append(kinds, StepAction)
	}
	if s.Gate != nil && s.Parallel == nil {
		kinds = append(kinds, StepGate)
	}
	if len(kinds) != 1 {
		return ""
	}
	return kinds[0]
}

// Agent says which role runs a step, with which harness and model.
type Agent struct {
	// Role is a role name, optionally pinned as "name@version".
	Role    string `json:"role"`
	Harness string `json:"harness,omitempty"`
	// Model is a model ref "<provider>/<model>". Empty means the role's default
	// model, then the platform's.
	Model string `json:"model,omitempty"`
}

// RoleName is the role without its version pin.
func (a Agent) RoleName() string {
	name, _, _ := strings.Cut(a.Role, "@")
	return name
}

// Gate is a gate's definition. Which fields apply depends on Type.
type Gate struct {
	Type   domain.GateType   `json:"type"`
	Policy domain.GatePolicy `json:"policy,omitempty"`
	// OnFail is what a failed blocking gate does; advisory gates have none.
	OnFail *OnFail `json:"onFail,omitempty"`
	// Run lists the commands of a command gate, run in order in one fresh pod.
	Run []string `json:"run,omitempty"`
	// Require is how an agent-verdict gate combines its reviewers' verdicts.
	Require Require `json:"require,omitempty"`
	// Timeout bounds a command gate's pod or a human gate's wait.
	Timeout Duration `json:"timeout,omitempty"`
	// OnTimeout is what a human gate does when nobody decided in time.
	OnTimeout OnTimeout `json:"onTimeout,omitempty"`
}

// Require combines several reviewers' verdicts.
type Require string

// Ways to combine verdicts.
const (
	RequireAll      Require = "all"
	RequireAny      Require = "any"
	RequireMajority Require = "majority"
)

// OnTimeout is what a human gate does when its timeout passes.
type OnTimeout string

// Human gate timeout behaviors. Escalating a human gate needs a notification
// channel the platform does not have yet, so it is not accepted.
const (
	OnTimeoutFail   OnTimeout = "fail"
	OnTimeoutExtend OnTimeout = "extend"
)

// OnFailAction is what a failed blocking gate does.
type OnFailAction string

// onFail actions.
const (
	OnFailFail     OnFailAction = "fail"
	OnFailEscalate OnFailAction = "escalate" // route to a human gate
	OnFailGoto     OnFailAction = "goto"     // loop back to an earlier step
)

// OnFail is a gate's failure handling. In YAML it is "fail", "escalate" or
// {goto: <step-id>, maxLoops: N}.
type OnFail struct {
	Action   OnFailAction `json:"action"`
	Goto     string       `json:"goto,omitempty"`
	MaxLoops int          `json:"maxLoops,omitempty"`
}

// UnmarshalJSON accepts the string forms and the strict goto object.
func (o *OnFail) UnmarshalJSON(b []byte) error {
	var short string
	if err := json.Unmarshal(b, &short); err == nil {
		*o = OnFail{Action: OnFailAction(short)}
		return nil
	}
	type plain OnFail
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var p plain
	if err := dec.Decode(&p); err != nil {
		return fmt.Errorf("onFail: %w", err)
	}
	if p.Action == "" && p.Goto != "" {
		p.Action = OnFailGoto
	}
	*o = OnFail(p)
	return nil
}

// Duration is a time.Duration written as a Go duration string ("90m").
type Duration time.Duration

// UnmarshalJSON parses a duration string.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be a string like \"30m\": %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// MarshalJSON writes the duration string, so a snapshot round-trips.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// Defaults filled in by ParseWorkflow.
const (
	DefaultHarness        = "claude-code"
	DefaultHumanTimeout   = Duration(72 * time.Hour)
	MaxLoopsLimit         = 10
	defaultCommandLint    = "mise run lint"
	defaultCommandTest    = "mise run test"
	maxIdentifierLength   = 40
	maxCommandsPerGate    = 10
	maxBranchesPerGroup   = 8
	maxStepsPerDefinition = 50
)

// Harnesses the definition may name: whether the runner supports it, the
// gateway APIs it speaks, which decide the providers it can use
// (providerAPIs), and the role tools it lacks.
var harnesses = map[string]struct {
	available bool
	apis      []API
	lacks     []string // role tools the harness cannot provide
}{
	"claude-code": {available: true, apis: []API{APIAnthropic}},
	"opencode":    {available: true, apis: []API{APIAnthropic, APIOpenAI}},
	"pi":          {available: true, apis: []API{APIAnthropic, APIOpenAI}, lacks: []string{"web-fetch"}},
}

// Actions the control plane knows; the value says whether it is implemented yet.
var actions = map[string]bool{
	"change-request.open": true,
	"merge-queue.enqueue": false, // M6
	"release.create":      false, // M7
	"issue.comment":       false,
}

// Step inputs an agent may be given. Until the native issue store (M5)
// separates them, spec and acceptance_criteria both come from the issue body.
var stepInputs = []string{"spec", "acceptance_criteria", "diff", "feedback"}

var (
	identifierRE = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	roleRefRE    = regexp.MustCompile(`^[a-z][a-z0-9-]*(@[1-9][0-9]*)?$`)
	// A model ref is <provider>/<model id>; the id may itself contain slashes
	// (OpenRouter's "openai/gpt-5").
	modelRefRE = regexp.MustCompile(`^[a-z][a-z0-9-]*/[A-Za-z0-9][A-Za-z0-9._:-]*(/[A-Za-z0-9][A-Za-z0-9._:-]*)*$`)
)

// ParseWorkflow parses a workflow definition, fills in defaults and validates
// it (FR-WF-5). Cross-checks against roles and the repo come from the caller.
func ParseWorkflow(raw []byte) (Workflow, error) {
	var w Workflow
	if err := yaml.UnmarshalStrict(raw, &w); err != nil {
		return Workflow{}, fmt.Errorf("workflow: %w", err)
	}
	w.applyDefaults()
	if err := w.Validate(); err != nil {
		return Workflow{}, err
	}
	return w, nil
}

func (w *Workflow) applyDefaults() {
	for i := range w.Steps {
		s := &w.Steps[i]
		s.applyDefaults()
		for j := range s.Parallel {
			b := &s.Parallel[j]
			b.applyDefaults()
			if b.ID == "" && b.Agent != nil {
				b.ID = b.Agent.RoleName()
			}
		}
	}
}

func (s *Step) applyDefaults() {
	if s.Agent != nil {
		if s.Agent.Harness == "" {
			s.Agent.Harness = DefaultHarness
		}
		if s.Inputs == nil {
			s.Inputs = slices.Clone(stepInputs)
		}
	}
	g := s.Gate
	if g == nil {
		return
	}
	if g.Policy == "" {
		g.Policy = domain.GateBlocking
	}
	if g.OnFail == nil && g.Policy == domain.GateBlocking {
		g.OnFail = &OnFail{Action: OnFailFail}
	}
	switch g.Type {
	case domain.GateCommand:
		if len(g.Run) == 0 {
			g.Run = []string{defaultCommandLint, defaultCommandTest}
		}
	case domain.GateAgentVerdict:
		if g.Require == "" {
			g.Require = RequireAll
		}
	case domain.GateHuman:
		if g.Timeout == 0 {
			g.Timeout = DefaultHumanTimeout
		}
		if g.OnTimeout == "" {
			g.OnTimeout = OnTimeoutFail
		}
	}
}

// Validate checks the definition's structure: step kinds, unique IDs, gate
// fields per type, loops that go back to earlier steps with a bound, harness
// and model compatibility, and that deterministic gates come before LLM gates.
func (w Workflow) Validate() error {
	v := validator{seen: map[string]StepKind{}}
	if !identifierRE.MatchString(w.Name) || len(w.Name) > maxIdentifierLength {
		v.errf("name", "%q must be lowercase letters, digits and dashes, at most %d characters", w.Name, maxIdentifierLength)
	}
	if w.Version < 1 {
		v.errf("version", "must be at least 1")
	}
	switch {
	case len(w.Steps) == 0:
		v.errf("steps", "at least one step is required")
	case len(w.Steps) > maxStepsPerDefinition:
		v.errf("steps", "%d steps, at most %d", len(w.Steps), maxStepsPerDefinition)
	}
	for i, s := range w.Steps {
		v.step(fmt.Sprintf("steps[%d]", i), s, false)
	}
	if err := errors.Join(v.errs...); err != nil {
		return fmt.Errorf("workflow %q: %w", w.Name, err)
	}
	return nil
}

type validator struct {
	errs []error
	// seen holds the IDs of the steps validated so far (the earlier steps) and
	// their kinds, for goto targets.
	seen         map[string]StepKind
	commandGates int
}

func (v *validator) errf(path, format string, args ...any) {
	v.errs = append(v.errs, fmt.Errorf("%s: %s", path, fmt.Sprintf(format, args...)))
}

func (v *validator) step(path string, s Step, branch bool) {
	kind := s.Kind()
	if kind == "" {
		v.errf(path, "a step has exactly one of agent, parallel, action or gate (a parallel group may add a gate)")
		return
	}
	if branch && kind != StepAgent {
		v.errf(path, "parallel branches must be agent steps")
		return
	}
	if kind != StepAgent && (s.Inputs != nil || s.BudgetUSD != 0 || s.Timeout != 0) {
		v.errf(path, "inputs, timeout and budgetUSD apply to agent steps only")
	}

	// The step's own ID becomes a goto target only after its gate is checked:
	// a gate may not loop back to its own group.
	needsID := kind != StepParallel || s.Gate != nil
	switch {
	case s.ID == "" && needsID:
		v.errf(path+".id", "is required")
	case s.ID == "":
	case !identifierRE.MatchString(s.ID) || len(s.ID) > maxIdentifierLength:
		v.errf(path+".id", "%q must be lowercase letters, digits and dashes, at most %d characters", s.ID, maxIdentifierLength)
	case v.seen[s.ID] != "":
		v.errf(path+".id", "duplicate step id %q", s.ID)
	}

	switch kind {
	case StepAgent:
		v.agent(path, s)
	case StepAction:
		implemented, known := actions[s.Action]
		switch {
		case !known:
			v.errf(path+".action", "unknown action %q", s.Action)
		case !implemented:
			v.errf(path+".action", "%q is not available yet", s.Action)
		}
	case StepGate:
		v.gate(path+".gate", *s.Gate, false)
	case StepParallel:
		switch {
		case len(s.Parallel) == 0:
			v.errf(path+".parallel", "needs at least one branch")
		case len(s.Parallel) > maxBranchesPerGroup:
			v.errf(path+".parallel", "%d branches, at most %d", len(s.Parallel), maxBranchesPerGroup)
		}
		// Branches become goto targets with the group, after its gate.
		var branchIDs []string
		for j, b := range s.Parallel {
			bp := fmt.Sprintf("%s.parallel[%d]", path, j)
			v.step(bp, b, true)
			if b.ID != "" && (slices.Contains(branchIDs, b.ID) || b.ID == s.ID) {
				v.errf(bp+".id", "duplicate step id %q (a branch without an id is named after its role)", b.ID)
			}
			branchIDs = append(branchIDs, b.ID)
		}
		if s.Gate != nil {
			v.gate(path+".gate", *s.Gate, true)
		}
		for _, id := range branchIDs {
			if id != "" && v.seen[id] == "" {
				v.seen[id] = StepAgent
			}
		}
	}
	if !branch && s.ID != "" && v.seen[s.ID] == "" {
		v.seen[s.ID] = kind
	}
}

func (v *validator) agent(path string, s Step) {
	a := s.Agent
	if !roleRefRE.MatchString(a.Role) {
		v.errf(path+".agent.role", "%q must be a role name, optionally pinned as name@version", a.Role)
	}
	h, ok := harnesses[a.Harness]
	switch {
	case !ok:
		v.errf(path+".agent.harness", "unknown harness %q", a.Harness)
	case !h.available:
		v.errf(path+".agent.harness", "%q is not available yet", a.Harness)
	}
	if a.Model != "" {
		switch {
		case !modelRefRE.MatchString(a.Model):
			v.errf(path+".agent.model", "%q must be a model ref <provider>/<model>", a.Model)
		case ok:
			if err := harnessCanUse(a.Harness, a.Model); err != nil {
				v.errf(path+".agent.model", "%s", err)
			}
		}
	}
	for _, in := range s.Inputs {
		if !slices.Contains(stepInputs, in) {
			v.errf(path+".inputs", "unknown input %q (known: %s)", in, strings.Join(stepInputs, ", "))
		}
	}
	if s.Timeout < 0 {
		v.errf(path+".timeout", "must be positive")
	}
	if s.BudgetUSD < 0 {
		v.errf(path+".budgetUSD", "must not be negative")
	}
}

func (v *validator) gate(path string, g Gate, trailing bool) {
	if !g.Type.Valid() {
		v.errf(path+".type", "unknown gate type %q", g.Type)
		return
	}
	if !g.Policy.Valid() {
		v.errf(path+".policy", "unknown policy %q", g.Policy)
	}
	if g.Type != domain.GateCommand && g.Run != nil {
		v.errf(path+".run", "applies to command gates only")
	}
	if g.Type != domain.GateAgentVerdict && g.Require != "" {
		v.errf(path+".require", "applies to agent-verdict gates only")
	}
	if g.Type != domain.GateHuman && g.OnTimeout != "" {
		v.errf(path+".onTimeout", "applies to human gates only")
	}
	if g.Type == domain.GateAgentVerdict && g.Timeout != 0 {
		v.errf(path+".timeout", "an agent-verdict gate is bounded by its reviewers' timeouts")
	}
	if g.Timeout < 0 {
		v.errf(path+".timeout", "must be positive")
	}

	switch g.Type {
	case domain.GateCommand:
		v.commandGates++
		if len(g.Run) > maxCommandsPerGate {
			v.errf(path+".run", "%d commands, at most %d", len(g.Run), maxCommandsPerGate)
		}
		for i, c := range g.Run {
			if strings.TrimSpace(c) == "" {
				v.errf(fmt.Sprintf("%s.run[%d]", path, i), "is empty")
			}
		}
	case domain.GateAgentVerdict:
		if !trailing {
			v.errf(path, "an agent-verdict gate judges its parallel group's reviewers; put it on a parallel step")
		}
		if v.commandGates == 0 {
			v.errf(path, "deterministic gates run before LLM gates: add a command gate before it")
		}
		if !slices.Contains([]Require{RequireAll, RequireAny, RequireMajority}, g.Require) {
			v.errf(path+".require", "%q must be all, any or majority", g.Require)
		}
	case domain.GateHuman:
		if g.Policy == domain.GateAdvisory {
			v.errf(path+".policy", "a human gate is always blocking")
		}
		if g.OnTimeout != OnTimeoutFail && g.OnTimeout != OnTimeoutExtend {
			v.errf(path+".onTimeout", "%q must be fail or extend", g.OnTimeout)
		}
	}

	if g.Policy == domain.GateAdvisory {
		if g.OnFail != nil {
			v.errf(path+".onFail", "an advisory gate does not stop the run, so it has no onFail")
		}
		return
	}
	if g.OnFail == nil {
		return
	}
	o := *g.OnFail
	if g.Type == domain.GateHuman && o.Action != OnFailFail {
		v.errf(path+".onFail", "a rejected human gate fails the run")
		return
	}
	switch o.Action {
	case OnFailFail, OnFailEscalate:
		if o.Goto != "" || o.MaxLoops != 0 {
			v.errf(path+".onFail", "goto and maxLoops apply to loops only")
		}
	case OnFailGoto:
		switch kind := v.seen[o.Goto]; {
		case o.Goto == "":
			v.errf(path+".onFail.goto", "is required")
		case kind == "":
			v.errf(path+".onFail.goto", "%q is not an earlier step", o.Goto)
		case kind != StepAgent && kind != StepParallel:
			v.errf(path+".onFail.goto", "%q is a %s step; loops go back to an agent or parallel step", o.Goto, kind)
		}
		if o.MaxLoops < 1 || o.MaxLoops > MaxLoopsLimit {
			v.errf(path+".onFail.maxLoops", "must be between 1 and %d", MaxLoopsLimit)
		}
	default:
		v.errf(path+".onFail", "%q must be fail, escalate or {goto, maxLoops}", o.Action)
	}
}
