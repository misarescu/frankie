// Command runner runs inside every agent pod and wraps the agent harness behind the runner gRPC interface.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "runner: not implemented yet (docs/spec/10-roadmap.md, M1)")
	os.Exit(1)
}
