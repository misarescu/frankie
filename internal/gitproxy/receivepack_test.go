package gitproxy

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

const (
	zeroOID = "0000000000000000000000000000000000000000"
	oidA    = "1111111111111111111111111111111111111111"
	oidB    = "2222222222222222222222222222222222222222"
)

func pushBody(lines ...string) string {
	var b strings.Builder
	for _, l := range lines {
		b.Write(pktLine([]byte(l)))
	}
	b.Write(pktFlush)
	return b.String()
}

func TestParsePushCommands(t *testing.T) {
	body := pushBody(
		"shallow "+oidA+"\n",
		oidA+" "+oidB+" refs/heads/agent/frk-42/x\x00report-status side-band-64k quiet agent=git/2.56\n",
		zeroOID+" "+oidA+" refs/heads/main\n",
	)
	pack := "PACK-DATA-THAT-MUST-NOT-BE-READ"
	r := strings.NewReader(body + pack)
	p, err := parsePushCommands(r)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(p.updates) != 2 || p.updates[0] != (refUpdate{oidA, oidB, "refs/heads/agent/frk-42/x"}) || p.updates[1].Ref != "refs/heads/main" {
		t.Errorf("updates = %+v", p.updates)
	}
	if !p.caps["report-status"] || !p.caps["side-band-64k"] || !p.wantsReport() {
		t.Errorf("caps = %v", p.caps)
	}
	if string(p.raw) != body {
		t.Errorf("raw = %q, want exactly the command section", p.raw)
	}
	rest, _ := io.ReadAll(r)
	if string(rest) != pack {
		t.Errorf("pack data consumed: remaining %q", rest)
	}
}

func TestParsePushCommandsErrors(t *testing.T) {
	if _, err := parsePushCommands(strings.NewReader(pushBody("push-cert\x00report-status\n"))); !errors.Is(err, errPushCert) {
		t.Errorf("push cert: err = %v", err)
	}
	for name, body := range map[string]string{
		"two fields":  pushBody(oidA + " refs/heads/x\n"),
		"short oid":   pushBody("abc " + oidB + " refs/heads/x\n"),
		"no flush":    string(pktLine([]byte(oidA + " " + oidB + " refs/heads/x\n"))),
		"upper hex":   pushBody("ABCDEF1111111111111111111111111111111111 " + oidB + " refs/heads/x\n"),
		"bad pktline": "zzzz",
	} {
		if _, err := parsePushCommands(strings.NewReader(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if p, err := parsePushCommands(strings.NewReader("0000")); err != nil || len(p.updates) != 0 {
		t.Errorf("probe (flush only) = %+v, %v", p, err)
	}
}

// D21: agents create and update branches of their own issue only.
func TestCheckUpdates(t *testing.T) {
	tests := []struct {
		name   string
		update refUpdate
		reason string // empty means allowed
	}{
		{"create own branch", refUpdate{zeroOID, oidA, "refs/heads/agent/frk-42/fix"}, ""},
		{"update own branch", refUpdate{oidA, oidB, "refs/heads/agent/frk-42/fix"}, ""},
		{"nested own branch", refUpdate{zeroOID, oidA, "refs/heads/agent/frk-42/a/b"}, ""},
		{"main", refUpdate{oidA, oidB, "refs/heads/main"}, "agents may only push refs/heads/agent/frk-42/*"},
		{"other issue", refUpdate{zeroOID, oidA, "refs/heads/agent/frk-43/fix"}, "agents may only push"},
		{"issue key prefix", refUpdate{zeroOID, oidA, "refs/heads/agent/frk-421/fix"}, "agents may only push"},
		{"namespace root", refUpdate{zeroOID, oidA, "refs/heads/agent/frk-42"}, "agents may only push"},
		{"tag", refUpdate{zeroOID, oidA, "refs/tags/v1"}, "only branches"},
		{"notes", refUpdate{zeroOID, oidA, "refs/notes/commits"}, "only branches"},
		{"delete own branch", refUpdate{oidA, zeroOID, "refs/heads/agent/frk-42/fix"}, "deleting branches"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			denied := checkUpdates([]refUpdate{tt.update}, "frk-42")
			reason, isDenied := denied[tt.update.Ref]
			if tt.reason == "" && isDenied {
				t.Errorf("denied: %s", reason)
			}
			if tt.reason != "" && !strings.Contains(reason, tt.reason) {
				t.Errorf("reason = %q, want it to contain %q", reason, tt.reason)
			}
		})
	}
}

func TestRejectionReport(t *testing.T) {
	p := pushCommands{
		updates: []refUpdate{{oidA, oidB, "refs/heads/agent/frk-42/x"}, {oidA, oidB, "refs/heads/main"}},
		caps:    map[string]bool{"report-status": true},
	}
	denied := map[string]string{"refs/heads/main": "agents may only push refs/heads/agent/frk-42/*"}
	plain := rejectionReport(p, denied)
	want := "000eunpack ok\n" +
		string(pktLine([]byte("ng refs/heads/agent/frk-42/x rejected because other refs in this push were denied\n"))) +
		string(pktLine([]byte("ng refs/heads/main agents may only push refs/heads/agent/frk-42/*\n"))) + "0000"
	if string(plain) != want {
		t.Errorf("report =\n%q\nwant\n%q", plain, want)
	}

	p.caps["side-band-64k"] = true
	banded := bytes.NewReader(rejectionReport(p, denied))
	var inner []byte
	for {
		pl, flush, err := readPktLine(banded)
		if err != nil {
			t.Fatal(err)
		}
		if flush {
			break
		}
		inner = append(inner, pl[1:]...)
	}
	if string(inner) != want {
		t.Errorf("side-band payload = %q, want %q", inner, want)
	}
}
