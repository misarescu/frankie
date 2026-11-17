package workflow

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"go.temporal.io/sdk/workflow"

	"github.com/misarescu/frankie/internal/config"
)

// sentBack is a failed gate that sent the change back to an agent step (D128).
type sentBack struct {
	pc       int
	step     config.Step
	summary  string
	feedback string
}

// agentReport is the final message of an agent step that changed nothing.
type agentReport struct {
	step, role, text string
}

// dispute is the agents a failed gate sent the change back to that changed
// nothing, saying why (D128).
type dispute struct {
	reports []agentReport
}

// maxReport caps an agent's report passed to another agent or a person.
const maxReport = 6000

// canDispute reports whether an agent step that changed nothing disputes the
// failure that sent the change back, instead of failing (D128): only after a
// failed gate looped back, in runs recorded since.
func (it *interpreter) canDispute(ctx workflow.Context) bool {
	return it.back != nil && workflow.GetVersion(ctx, "disputed-changes", workflow.DefaultVersion, 1) >= 1
}

// escalateDispute asks a person about the gate whose failure the agents it
// went back to disputed. Approving continues after that gate on the commit it
// failed, which nothing has changed since; rejecting fails the run.
func (it *interpreter) escalateDispute(ctx workflow.Context) (int, string) {
	b, d := it.back, it.dispute
	it.back, it.dispute = nil, nil
	var who []string
	var reports strings.Builder
	for _, r := range d.reports {
		who = append(who, fmt.Sprintf("`%s` (%s)", r.step, r.role))
		fmt.Fprintf(&reports, "\n\n**%s** (%s):\n\n%s", r.step, r.role, clip(r.text))
	}
	failure := fmt.Sprintf("%s; sent back, %s changed nothing", b.summary, strings.Join(who, " and "))
	reason := fmt.Sprintf("The `%s` gate failed: %s. It sent the change back, and %s changed nothing, reporting why:%s\n\n"+
		"Approve to accept the change as it is and continue after the gate, or reject to fail the run.",
		b.step.ID, b.summary, strings.Join(who, " and "), reports.String())
	return it.askToContinue(ctx, b.pc, b.step, failure, reason)
}

// disputeFeedback tells a group's other writers that the coder found the
// failure in their work: what failed, and the coder's report (D128).
func disputeFeedback(b *sentBack, coder agentReport) string {
	return fmt.Sprintf("The `%s` check failed on the change with your work merged in: %s\n\n%s\n\n"+
		"It went back to the %s, which changed nothing and reported that the failure is in your work:\n\n%s\n\n"+
		"Check the report against the issue's spec. Fix what is wrong on your branch and run it again. "+
		"If your work is right, change nothing and explain why in your final message; a person then decides.",
		b.step.ID, b.summary, strings.TrimSpace(b.feedback), coder.role, clip(coder.text))
}

// clip shortens an agent's report to maxReport bytes, at a line break when
// it can.
func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxReport {
		return s
	}
	n := maxReport
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	cut := s[:n]
	if i := strings.LastIndexByte(cut, '\n'); i > maxReport/2 {
		cut = cut[:i]
	}
	return cut + "\n[...]"
}
