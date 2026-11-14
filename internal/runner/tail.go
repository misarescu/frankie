package runner

import "sync"

// TailBuffer keeps the last bytes written to it, e.g. a harness's stderr for
// the error event of a failed session.
type TailBuffer struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

// NewTailBuffer keeps the last limit bytes.
func NewTailBuffer(limit int) *TailBuffer { return &TailBuffer{limit: limit} }

func (b *TailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if over := len(b.buf) - b.limit; over > 0 {
		b.buf = b.buf[over:]
	}
	return len(p), nil
}

func (b *TailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
