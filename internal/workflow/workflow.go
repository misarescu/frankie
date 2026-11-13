// Package workflow holds the control plane's Temporal workflows, one workflow type per
// file (issue_run.go, later interpret.go, merge_queue.go, release.go); their
// Activities live in the activities subpackage
// (docs/spec/03-workflows-and-gates.md). M1 runs one hardcoded workflow per
// issue; the generic interpreter of workflow definitions replaces it in M3.
//
// Workflow code is deterministic (invariant 3): it only calls Activities.
package workflow

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// TaskQueue is where the control plane's worker polls.
const TaskQueue = "frankie"

// Activity options. The agent session is long and heartbeats so a restarted
// control plane resumes the event stream instead of starting over.
var (
	shortOptions = workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 5},
	}
	// Resolving the toolchain image may wait for a BuildKit Job (its own deadline
	// is 30 minutes); it heartbeats while it waits.
	toolchainOptions = workflow.ActivityOptions{
		StartToCloseTimeout: 40 * time.Minute,
		HeartbeatTimeout:    time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	}
	// The command gate may build the commit's toolchain image, then waits for
	// its pod (deadline from the gate config, 30 minutes by default).
	gateOptions = workflow.ActivityOptions{
		StartToCloseTimeout: 80 * time.Minute,
		HeartbeatTimeout:    time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	}
	// Launching heartbeats while it waits for the pod, so a worker that dies
	// mid-launch is noticed in seconds rather than at the 10-minute timeout.
	launchOptions = workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		HeartbeatTimeout:    30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	}
	sessionOptions = workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Hour,
		HeartbeatTimeout:    2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	}
)
