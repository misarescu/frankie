package gitproxy

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestReadPktLine(t *testing.T) {
	r := strings.NewReader("000ahello\n0000" + "0004")
	p, flush, err := readPktLine(r)
	if err != nil || flush || string(p) != "hello\n" {
		t.Fatalf("first = %q, %v, %v", p, flush, err)
	}
	if _, flush, err := readPktLine(r); err != nil || !flush {
		t.Fatalf("flush = %v, %v", flush, err)
	}
	if p, flush, err := readPktLine(r); err != nil || flush || len(p) != 0 {
		t.Fatalf("empty packet = %q, %v, %v", p, flush, err)
	}
	if _, _, err := readPktLine(r); !errors.Is(err, io.EOF) {
		t.Fatalf("at end: %v, want EOF", err)
	}
}

func TestReadPktLineMalformed(t *testing.T) {
	for name, in := range map[string]string{
		"non-hex length": "zzzzhello",
		"length below 4": "0003",
		"length too big": "fff1" + strings.Repeat("x", 10),
		"truncated":      "0010short",
	} {
		if _, _, err := readPktLine(strings.NewReader(in)); err == nil {
			t.Errorf("%s: accepted %q", name, in)
		}
	}
}

func TestPktLineEncoding(t *testing.T) {
	if got := string(pktLine([]byte("unpack ok\n"))); got != "000eunpack ok\n" {
		t.Errorf("pktLine = %q", got)
	}
}

func TestSidebandSplitsLargeData(t *testing.T) {
	data := bytes.Repeat([]byte("a"), maxPktLen*2)
	r := bytes.NewReader(sideband(data))
	var got []byte
	for r.Len() > 0 {
		p, flush, err := readPktLine(r)
		if err != nil || flush {
			t.Fatalf("readPktLine: %v, flush %v", err, flush)
		}
		if p[0] != 1 {
			t.Fatalf("channel %d, want 1", p[0])
		}
		got = append(got, p[1:]...)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("reassembled %d bytes, want %d", len(got), len(data))
	}
}
