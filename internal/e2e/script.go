package e2e

import (
	"bytes"
	"strings"
)

// The markers around the results file in a verification pod's log.
const (
	resultsBegin = "<<<frankie-e2e-results>>>"
	resultsEnd   = "<<<frankie-e2e-results-end>>>"
	noResults    = "<<<frankie-e2e-no-results>>>"
)

// Script runs a project's e2e command and then prints its results file
// between markers, so the control plane reads the results from the pod's log;
// it exits with the command's exit code, which alone decides the verdict.
// resultsEnv names the variable holding the results file's path.
func Script(command, resultsEnv string) string {
	return "set +e\n" +
		"(\n" + command + "\n)\n" +
		"code=$?\n" +
		`if [ -f "$` + resultsEnv + `" ]; then echo '` + resultsBegin + `'; cat "$` + resultsEnv + `"; echo; echo '` + resultsEnd + `'; ` +
		`else echo '` + noResults + `'; fi` + "\n" +
		"exit $code\n"
}

// Results extracts the results file Script printed in log. found is false when
// the suite wrote none; the last printed copy wins.
func Results(log []byte) (results []byte, found bool) {
	i := bytes.LastIndex(log, []byte(resultsBegin+"\n"))
	if i < 0 {
		return nil, false
	}
	rest := log[i+len(resultsBegin)+1:]
	j := bytes.LastIndex(rest, []byte(resultsEnd))
	if j < 0 {
		return nil, false
	}
	return bytes.TrimSpace(rest[:j]), true
}

// Before returns the log up to the results, for showing what the suite printed.
func Before(log []byte) string {
	s := string(log)
	for _, m := range []string{resultsBegin, noResults} {
		if i := strings.LastIndex(s, m); i >= 0 {
			s = s[:i]
		}
	}
	return s
}
