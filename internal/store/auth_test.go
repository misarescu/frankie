package store

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/domain"
)

func sha(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

func TestBootstrapAdminAndLastAdminGuards(t *testing.T) {
	st := newTestStore(t)
	ctx := t.Context()

	admin, created, err := st.BootstrapAdmin(ctx, NewUser{
		Username: "admin", DisplayName: "Admin", Email: "admin@example.com", PasswordHash: "hash1",
	})
	if err != nil || !created || admin.Role != domain.RoleAdmin {
		t.Fatalf("first BootstrapAdmin = %+v, created=%v, err=%v", admin, created, err)
	}

	// Second call is a no-op even with different credentials.
	_, created, err = st.BootstrapAdmin(ctx, NewUser{Username: "other", PasswordHash: "hash2"})
	if err != nil || created {
		t.Fatalf("second BootstrapAdmin created=%v err=%v, want false/nil", created, err)
	}

	// Cannot demote or disable the only active admin.
	if _, err := st.UpdateUserRole(ctx, admin.ID, domain.RoleMember); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demote last admin err = %v, want ErrLastAdmin", err)
	}
	if _, err := st.SetUserDisabled(ctx, admin.ID, true); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("disable last admin err = %v, want ErrLastAdmin", err)
	}

	// Adding a second admin allows demoting or disabling the first.
	admin2, err := st.CreateUser(ctx, NewUser{Username: "admin2", Role: domain.RoleAdmin, PasswordHash: "h"})
	if err != nil {
		t.Fatal(err)
	}
	sessHash := sha("sess-1")
	if _, err := st.CreateSession(ctx, admin.ID, sessHash, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	disabled, err := st.SetUserDisabled(ctx, admin.ID, true)
	if err != nil || disabled.DisabledAt == nil {
		t.Fatalf("disable first admin = %+v, %v", disabled, err)
	}
	// Disabling revokes sessions and rejects lookups.
	if _, _, err := st.GetSessionByHash(ctx, sessHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session lookup after disable = %v, want ErrNotFound", err)
	}
	// Now admin2 is the last active admin.
	if _, err := st.UpdateUserRole(ctx, admin2.ID, domain.RoleViewer); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demote remaining admin = %v, want ErrLastAdmin", err)
	}
}

func TestIdentitiesAndLastAuthMethod(t *testing.T) {
	st := newTestStore(t)
	ctx := t.Context()

	u, id1, err := st.CreateUserWithIdentity(ctx,
		NewUser{Username: "alice", DisplayName: "Alice", Email: "alice@example.com", Role: domain.RoleMember},
		NewIdentity{Provider: domain.ProviderGitHub, Subject: "gh-123", Email: "alice@example.com", Login: "alice"},
	)
	if err != nil {
		t.Fatal(err)
	}
	// SSO-only user cannot unlink their only identity.
	if err := st.DeleteIdentity(ctx, u.ID, id1.ID); !errors.Is(err, ErrLastAuthMethod) {
		t.Fatalf("delete sole identity = %v, want ErrLastAuthMethod", err)
	}
	// Linking a second identity allows unlinking the first.
	id2, err := st.LinkIdentity(ctx, NewIdentity{UserID: u.ID, Provider: domain.ProviderGoogle, Subject: "goog-99", Email: "alice@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	// Duplicate (provider, subject) conflicts.
	if _, err := st.LinkIdentity(ctx, NewIdentity{UserID: u.ID, Provider: domain.ProviderGoogle, Subject: "goog-99"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate identity = %v, want ErrConflict", err)
	}
	if err := st.DeleteIdentity(ctx, u.ID, id1.ID); err != nil {
		t.Fatalf("unlink first of two identities: %v", err)
	}
	// Setting a password lets the user unlink the remaining identity too.
	if err := st.UpdateUserPassword(ctx, u.ID, "argon2hash", nil); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteIdentity(ctx, u.ID, id2.ID); err != nil {
		t.Fatalf("unlink identity with password set: %v", err)
	}
}

func TestSessionsAndAPITokens(t *testing.T) {
	st := newTestStore(t)
	ctx := t.Context()

	u, err := st.CreateUser(ctx, NewUser{Username: "bob", DisplayName: "Bob", Role: domain.RoleViewer, PasswordHash: "h"})
	if err != nil {
		t.Fatal(err)
	}
	activeHash := sha("active")
	expiredHash := sha("expired")
	if _, err := st.CreateSession(ctx, u.ID, activeHash, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSession(ctx, u.ID, expiredHash, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, got, err := st.GetSessionByHash(ctx, activeHash); err != nil || got.ID != u.ID {
		t.Fatalf("active session lookup = %+v, %v", got, err)
	}
	if _, _, err := st.GetSessionByHash(ctx, expiredHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session lookup = %v, want ErrNotFound", err)
	}
	if n, err := st.PruneExpiredSessions(ctx); err != nil || n != 1 {
		t.Fatalf("PruneExpiredSessions = %d, %v; want 1", n, err)
	}

	tokHash := sha("frk_secret")
	tok, err := st.CreateAPIToken(ctx, NewAPIToken{UserID: u.ID, Name: "cli", TokenPrefix: "frk_12345678", TokenHash: tokHash})
	if err != nil {
		t.Fatal(err)
	}
	gotTok, gotUser, err := st.GetAPITokenByHash(ctx, tokHash)
	if err != nil || gotTok.ID != tok.ID || gotUser.ID != u.ID {
		t.Fatalf("GetAPITokenByHash = %+v, %+v, %v", gotTok, gotUser, err)
	}
	if err := st.RevokeAPIToken(ctx, u.ID, tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.GetAPITokenByHash(ctx, tokHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked token lookup = %v, want ErrNotFound", err)
	}
}

func TestInvites(t *testing.T) {
	st := newTestStore(t)
	ctx := t.Context()

	admin, _, err := st.BootstrapAdmin(ctx, NewUser{Username: "admin", PasswordHash: "h"})
	if err != nil {
		t.Fatal(err)
	}
	invHash := sha("inv-1")
	inv, err := st.CreateInvite(ctx, NewInvite{
		TokenHash: invHash, Email: "carol@example.com", Role: domain.RoleMember,
		InvitedBy: &admin.ID, ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil || inv.Role != domain.RoleMember {
		t.Fatalf("CreateInvite = %+v, %v", inv, err)
	}

	// Wrong email fails.
	if _, err := st.AcceptInvite(ctx, invHash, NewUser{Username: "carol", Email: "wrong@example.com", PasswordHash: "h"}, nil); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("accept invite wrong email = %v, want ErrInviteInvalid", err)
	}
	// Right email succeeds and grants the invite's role (ignoring caller-supplied RoleAdmin).
	u, err := st.AcceptInvite(ctx, invHash, NewUser{Username: "carol", Email: "CAROL@example.com", Role: domain.RoleAdmin, PasswordHash: "h"}, nil)
	if err != nil || u.Role != domain.RoleMember {
		t.Fatalf("AcceptInvite = %+v, %v", u, err)
	}
	// Reusing the invite fails.
	if _, err := st.AcceptInvite(ctx, invHash, NewUser{Username: "carol2", Email: "carol@example.com", PasswordHash: "h"}, nil); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("reuse invite = %v, want ErrInviteInvalid", err)
	}
}
