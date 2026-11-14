package runner

import (
	"fmt"
	"testing"
)

func TestTailBufferKeepsTheLastBytes(t *testing.T) {
	b := NewTailBuffer(5)
	for _, s := range []string{"abc", "defg", "h"} {
		if n, err := fmt.Fprint(b, s); err != nil || n != len(s) {
			t.Fatalf("write %q: %d, %v", s, n, err)
		}
	}
	if got := b.String(); got != "defgh" {
		t.Errorf("tail = %q, want defgh", got)
	}
}
