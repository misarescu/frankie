package gitproxy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
)

// maxPktLen is the largest pkt-line git sends, including the 4-byte length.
const maxPktLen = 65520

var errBadPktLine = errors.New("gitproxy: malformed pkt-line")

// readPktLine reads one pkt-line. It returns flush=true for "0000" and the
// payload (without the length prefix) otherwise.
func readPktLine(r io.Reader) (payload []byte, flush bool, err error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, false, err
	}
	n, err := strconv.ParseUint(string(hdr[:]), 16, 16)
	if err != nil {
		return nil, false, fmt.Errorf("%w: length %q", errBadPktLine, hdr[:])
	}
	switch {
	case n == 0:
		return nil, true, nil
	case n < 4 || n > maxPktLen:
		return nil, false, fmt.Errorf("%w: length %d", errBadPktLine, n)
	}
	payload = make([]byte, n-4)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, false, fmt.Errorf("%w: %w", errBadPktLine, err)
	}
	return payload, false, nil
}

// pktLine encodes payload as a pkt-line.
func pktLine(payload []byte) []byte {
	return append(fmt.Appendf(nil, "%04x", len(payload)+4), payload...)
}

// pktFlush is the flush packet.
var pktFlush = []byte("0000")

// sideband wraps data in side-band channel 1 packets, splitting as needed.
func sideband(data []byte) []byte {
	var out bytes.Buffer
	const chunk = maxPktLen - 5 // 4-byte length + 1-byte channel
	for len(data) > 0 {
		n := min(len(data), chunk)
		out.Write(pktLine(append([]byte{1}, data[:n]...)))
		data = data[n:]
	}
	return out.Bytes()
}
