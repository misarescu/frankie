package auth

import (
	"strings"
	"testing"
)

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=1$") {
		t.Fatalf("unexpected PHC format: %q", hash)
	}
	ok, err := VerifyPassword("correct-horse-battery", hash)
	if err != nil || !ok {
		t.Fatalf("VerifyPassword valid = %v, %v; want true", ok, err)
	}
	ok, err = VerifyPassword("wrong-horse-battery", hash)
	if err != nil || ok {
		t.Fatalf("VerifyPassword wrong = %v, %v; want false", ok, err)
	}
	// Two hashes of the same password use different salts.
	hash2, err := HashPassword("correct-horse-battery")
	if err != nil || hash2 == hash {
		t.Fatalf("expected distinct salted hashes, err=%v", err)
	}
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("expected error for short password")
	}
	if _, err := VerifyPassword("correct-horse-battery", "$bcrypt$invalid"); err == nil {
		t.Fatal("expected error for invalid format")
	}
}
