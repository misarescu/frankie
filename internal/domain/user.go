package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Role is a deployment-wide permission level for a signed-in person (D146).
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
	RoleViewer Role = "viewer"
)

// ValidRole reports whether r is a known role.
func ValidRole(r Role) bool {
	switch r {
	case RoleAdmin, RoleMember, RoleViewer:
		return true
	}
	return false
}

// AtLeast reports whether r grants at least the permissions of min
// (admin >= member >= viewer).
func (r Role) AtLeast(min Role) bool {
	return roleRank(r) >= roleRank(min) && roleRank(min) > 0
}

func roleRank(r Role) int {
	switch r {
	case RoleViewer:
		return 1
	case RoleMember:
		return 2
	case RoleAdmin:
		return 3
	default:
		return 0
	}
}

// SSOProvider is a supported external sign-in provider (D145).
type SSOProvider string

const (
	ProviderGoogle SSOProvider = "google"
	ProviderGitLab SSOProvider = "gitlab"
	ProviderGitHub SSOProvider = "github"
)

// ValidSSOProvider reports whether p is a supported SSO provider.
func ValidSSOProvider(p SSOProvider) bool {
	switch p {
	case ProviderGoogle, ProviderGitLab, ProviderGitHub:
		return true
	}
	return false
}

// User is a person who can sign in to Frankie (D144).
type User struct {
	ID           uuid.UUID
	Username     string
	DisplayName  string
	Email        string
	Role         Role
	PasswordHash string // argon2id PHC string; empty for SSO-only accounts
	DisabledAt   *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Active reports whether the user is allowed to sign in and use the API.
func (u User) Active() bool { return u.DisabledAt == nil }

// HasPassword reports whether the user has a local password configured.
func (u User) HasPassword() bool { return u.PasswordHash != "" }

// ActorName is the identity recorded on decisions, answers, steers and start
// requests (D146): display name when set, otherwise username.
func (u User) ActorName() string {
	name := strings.TrimSpace(u.DisplayName)
	if name == "" {
		name = u.Username
	}
	if len(name) > 100 {
		name = strings.TrimSpace(name[:100])
	}
	return name
}

// UserIdentity links an external SSO subject to a Frankie user (D144).
type UserIdentity struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Provider  SSOProvider
	Subject   string
	Email     string
	Login     string
	CreatedAt time.Time
}

// Session is an active browser session (D144); only the SHA-256 hash of its
// cookie secret is stored in Postgres.
type Session struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	ExpiresAt  time.Time
	LastSeenAt time.Time
	CreatedAt  time.Time
}

// APIToken is a per-person bearer token for scripts and the CLI (D144, D146).
type APIToken struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	Name        string
	TokenPrefix string
	LastUsedAt  *time.Time
	ExpiresAt   *time.Time
	RevokedAt   *time.Time
	CreatedAt   time.Time
}

// Invite is a single-use administrator invitation to join Frankie (D145).
type Invite struct {
	ID            uuid.UUID
	Email         string
	Role          Role
	InvitedBy     *uuid.UUID
	InvitedByName string
	ExpiresAt     time.Time
	AcceptedAt    *time.Time
	AcceptedBy    *uuid.UUID
	CreatedAt     time.Time
}

// Usable reports whether the invite has not been accepted and has not expired.
func (in Invite) Usable(now time.Time) bool {
	return in.AcceptedAt == nil && now.Before(in.ExpiresAt)
}

var usernameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// NormalizeUsername trims, lowercases and validates a username.
func NormalizeUsername(raw string) (string, error) {
	u := strings.ToLower(strings.TrimSpace(raw))
	if !usernameRE.MatchString(u) {
		return "", fmt.Errorf("username %q must be 1-64 lowercase letters, digits, '.', '_' or '-', starting with a letter or digit", raw)
	}
	return u, nil
}

// NormalizeEmail trims and lowercases an optional email address, rejecting
// obviously malformed values. Empty is valid (email is optional).
func NormalizeEmail(raw string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(raw))
	if e == "" {
		return "", nil
	}
	at := strings.IndexByte(e, '@')
	if at <= 0 || at != strings.LastIndexByte(e, '@') || at == len(e)-1 || strings.ContainsAny(e, " \t\r\n") || !strings.Contains(e[at+1:], ".") {
		return "", fmt.Errorf("email %q is invalid", raw)
	}
	if len(e) > 254 {
		return "", errors.New("email is too long")
	}
	return e, nil
}

const (
	// MinPasswordLen is the shortest local password accepted (D145).
	MinPasswordLen = 12
	// MaxPasswordLen bounds argon2id input size.
	MaxPasswordLen = 128
)

// ValidatePassword checks password length bounds.
func ValidatePassword(password string) error {
	if len(password) < MinPasswordLen {
		return fmt.Errorf("password must be at least %d characters", MinPasswordLen)
	}
	if len(password) > MaxPasswordLen {
		return fmt.Errorf("password must be at most %d characters", MaxPasswordLen)
	}
	return nil
}

// SuggestUsername turns an external login or email into a valid username base.
func SuggestUsername(login, email string) string {
	src := strings.TrimSpace(login)
	if src == "" {
		if before, _, ok := strings.Cut(strings.TrimSpace(email), "@"); ok {
			src = before
		}
	}
	src = strings.ToLower(src)
	var b strings.Builder
	for _, r := range src {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case r == '.' || r == '_' || r == '-':
			if b.Len() > 0 {
				b.WriteRune(r)
			}
		default:
			if b.Len() > 0 {
				b.WriteByte('-')
			}
		}
		if b.Len() >= 48 {
			break
		}
	}
	s := strings.TrimRight(b.String(), "._-")
	if s == "" || !usernameRE.MatchString(s) {
		return "user"
	}
	return s
}
