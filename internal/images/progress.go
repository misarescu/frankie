package images

import "time"

// Progress hears about an image build a request waits for, so a person sees
// why a step has not started and what the build prints (D93). Calls come
// from the request's goroutine; an implementation must not block for long.
type Progress interface {
	// Building is called when the request starts a build, or joins one
	// another request started.
	Building(repository, tag string)
	// Output is new build log output, in order, without repeats.
	Output(text string)
	// Built is called when the build ended, with its error if it failed.
	Built(repository, tag string, took time.Duration, err error)
}
