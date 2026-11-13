package gitproxy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/misarescu/frankie/internal/domain"
)

var errPushCert = errors.New("gitproxy: signed pushes are not supported")

// refUpdate is one command of a push: move ref from Old to New.
type refUpdate struct {
	Old, New, Ref string
}

func (u refUpdate) deletes() bool { return strings.Trim(u.New, "0") == "" }

// pushCommands is the parsed command section of a receive-pack request.
type pushCommands struct {
	raw     []byte // the exact bytes read, replayed upstream when the push is allowed
	updates []refUpdate
	caps    map[string]bool
}

// parsePushCommands reads the command section of a git-receive-pack request body,
// up to and including its flush packet. The pack data that follows is not read.
func parsePushCommands(r io.Reader) (pushCommands, error) {
	var raw bytes.Buffer
	tee := io.TeeReader(r, &raw)
	out := pushCommands{caps: map[string]bool{}}
	first := true
	for {
		payload, flush, err := readPktLine(tee)
		if err != nil {
			return pushCommands{}, err
		}
		if flush {
			break
		}
		line := strings.TrimSuffix(string(payload), "\n")
		if strings.HasPrefix(line, "shallow ") {
			continue
		}
		if strings.HasPrefix(line, "push-cert") {
			return pushCommands{}, errPushCert
		}
		cmd := line
		if first {
			var caps string
			cmd, caps, _ = strings.Cut(line, "\x00")
			for _, c := range strings.Fields(caps) {
				out.caps[c] = true
			}
			first = false
		}
		f := strings.Fields(cmd)
		if len(f) != 3 || !isOID(f[0]) || !isOID(f[1]) {
			return pushCommands{}, fmt.Errorf("%w: command %q", errBadPktLine, cmd)
		}
		out.updates = append(out.updates, refUpdate{Old: f[0], New: f[1], Ref: f[2]})
	}
	out.raw = raw.Bytes()
	return out, nil
}

func isOID(s string) bool {
	if len(s) != 40 && len(s) != 64 { // SHA-1 or SHA-256
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// checkUpdates returns a rejection reason per denied ref; an empty map means the
// whole push is allowed. Agents may create or update branches under
// refs/heads/agent/<issue-key>/ and nothing else (D21).
func checkUpdates(updates []refUpdate, issueKey string) map[string]string {
	denied := map[string]string{}
	for _, u := range updates {
		branch, isBranch := strings.CutPrefix(u.Ref, "refs/heads/")
		switch {
		case !isBranch:
			denied[u.Ref] = "only branches may be pushed"
		case !domain.IsAgentBranchOf(branch, issueKey):
			denied[u.Ref] = "agents may only push refs/heads/" + domain.AgentBranchPrefix(issueKey) + "*"
		case u.deletes():
			denied[u.Ref] = "deleting branches is not allowed"
		}
	}
	return denied
}

// rejectionReport is a receive-pack result that rejects every ref of the push:
// denied refs with their reason, the rest because the push is all-or-nothing.
func rejectionReport(p pushCommands, denied map[string]string) []byte {
	var report bytes.Buffer
	report.Write(pktLine([]byte("unpack ok\n")))
	for _, u := range p.updates {
		reason, ok := denied[u.Ref]
		if !ok {
			reason = "rejected because other refs in this push were denied"
		}
		report.Write(pktLine([]byte("ng " + u.Ref + " " + reason + "\n")))
	}
	report.Write(pktFlush)
	if p.caps["side-band-64k"] || p.caps["side-band"] {
		return append(sideband(report.Bytes()), pktFlush...)
	}
	return report.Bytes()
}

// wantsReport reports whether the client can show per-ref results.
func (p pushCommands) wantsReport() bool {
	return p.caps["report-status"] || p.caps["report-status-v2"]
}
