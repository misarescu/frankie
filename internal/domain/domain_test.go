package domain

import (
	"testing"
)

func TestNewIDIsTimeOrderedV7(t *testing.T) {
	a, b := NewID(), NewID()
	if a.Version() != 7 || b.Version() != 7 {
		t.Fatalf("versions = %d, %d; want 7", a.Version(), b.Version())
	}
	if a.String() >= b.String() {
		t.Errorf("IDs not increasing: %s then %s", a, b)
	}
}
