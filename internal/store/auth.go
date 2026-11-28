package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store/db"
)

var (
	// ErrLastAdmin is returned when a role change or disable would leave zero
	// active administrators.
	ErrLastAdmin = errors.New("store: cannot demote or disable the last active administrator")
	// ErrLastAuthMethod is returned when unlinking an SSO identity would leave
	// a user with no password and no other linked identity.
	ErrLastAuthMethod = errors.New("store: cannot unlink the last sign-in method")
	// ErrInviteInvalid is returned when an invite is expired, already used, or
	// restricted to a different email address.
	ErrInviteInvalid = errors.New("store: invite is no longer valid")
)

// NewUser is the input for CreateUser, BootstrapAdmin and AcceptInvite.
type NewUser struct {
	Username     string
	DisplayName  string
	Email        string
	Role         domain.Role
	PasswordHash string
}

// NewIdentity links an external SSO account to a Frankie user (D144).
type NewIdentity struct {
	UserID   uuid.UUID
	Provider domain.SSOProvider
	Subject  string
	Email    string
	Login    string
}

// NewAPIToken creates a per-person API token (D144, D146).
type NewAPIToken struct {
	UserID      uuid.UUID
	Name        string
	TokenPrefix string
	TokenHash   []byte
	ExpiresAt   *time.Time
}

// NewInvite creates a single-use invitation (D145).
type NewInvite struct {
	TokenHash []byte
	Email     string
	Role      domain.Role
	InvitedBy *uuid.UUID
	ExpiresAt time.Time
}

// CountUsers returns the total number of users (including disabled ones).
func (s *Store) CountUsers(ctx context.Context) (int64, error) {
	n, err := s.q.CountUsers(ctx)
	if err != nil {
		return 0, mapErr("count users", err)
	}
	return n, nil
}

// BootstrapAdmin creates the first administrator only when the users table is
// empty (D145). It returns created=false without modifying anything once any
// user exists, so a leftover bootstrap secret never overwrites an admin.
func (s *Store) BootstrapAdmin(ctx context.Context, in NewUser) (domain.User, bool, error) {
	in.Role = domain.RoleAdmin
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.User{}, false, fmt.Errorf("store: bootstrap begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(7104210)"); err != nil {
		return domain.User{}, false, fmt.Errorf("store: bootstrap lock: %w", err)
	}
	q := s.q.WithTx(tx)
	n, err := q.CountUsers(ctx)
	if err != nil {
		return domain.User{}, false, mapErr("count users", err)
	}
	if n > 0 {
		return domain.User{}, false, nil
	}
	u, err := insertUser(ctx, q, in)
	if err != nil {
		return domain.User{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, false, fmt.Errorf("store: bootstrap commit: %w", err)
	}
	return u, true, nil
}

// CreateUser inserts a new user.
func (s *Store) CreateUser(ctx context.Context, in NewUser) (domain.User, error) {
	return insertUser(ctx, s.q, in)
}

// CreateUserWithIdentity creates a user and links an SSO identity in one
// transaction (allowlist auto-provisioning, D145). If the requested username is
// already taken, it suffixes a short numeric counter so SSO sign-ins do not
// collide on common prefixes.
func (s *Store) CreateUserWithIdentity(ctx context.Context, in NewUser, ident NewIdentity) (domain.User, domain.UserIdentity, error) {
	var user domain.User
	var outIdent domain.UserIdentity
	err := s.inTx(ctx, func(q *db.Queries) error {
		base := domain.SuggestUsername(in.Username, in.Email)
		username := base
		for attempt := 0; attempt < 20; attempt++ {
			if attempt > 0 {
				suffix := fmt.Sprintf("-%d", attempt+1)
				trim := min(len(base), 64-len(suffix))
				username = base[:trim] + suffix
			}
			if _, err := q.GetUserByUsername(ctx, username); err != nil {
				if errors.Is(mapErr("check username", err), ErrNotFound) {
					break
				}
				return mapErr("check username", err)
			}
		}
		in.Username = username
		u, err := insertUser(ctx, q, in)
		if err != nil {
			return err
		}
		ident.UserID = u.ID
		idRow, err := insertIdentity(ctx, q, ident)
		if err != nil {
			return err
		}
		user, outIdent = u, idRow
		return nil
	})
	return user, outIdent, err
}

func insertUser(ctx context.Context, q *db.Queries, in NewUser) (domain.User, error) {
	username, err := domain.NormalizeUsername(in.Username)
	if err != nil {
		return domain.User{}, fmt.Errorf("store: create user: %w", err)
	}
	email, err := domain.NormalizeEmail(in.Email)
	if err != nil {
		return domain.User{}, fmt.Errorf("store: create user: %w", err)
	}
	if !domain.ValidRole(in.Role) {
		return domain.User{}, fmt.Errorf("store: create user: invalid role %q", in.Role)
	}
	display := strings.TrimSpace(in.DisplayName)
	if display == "" {
		display = username
	}
	if len(display) > 100 {
		display = strings.TrimSpace(display[:100])
	}
	row, err := q.CreateUser(ctx, db.CreateUserParams{
		ID:           domain.NewID(),
		Username:     username,
		DisplayName:  display,
		Email:        nonEmpty(email),
		Role:         string(in.Role),
		PasswordHash: in.PasswordHash,
	})
	if err != nil {
		return domain.User{}, mapErr("create user", err)
	}
	return userFromRow(row), nil
}

// GetUser looks up a user by ID.
func (s *Store) GetUser(ctx context.Context, id uuid.UUID) (domain.User, error) {
	row, err := s.q.GetUser(ctx, id)
	if err != nil {
		return domain.User{}, mapErr("get user", err)
	}
	return userFromRow(row), nil
}

// GetUserByLogin looks up a user by normalized username, or by email if the
// login string contains '@'.
func (s *Store) GetUserByLogin(ctx context.Context, login string) (domain.User, error) {
	login = strings.TrimSpace(login)
	if login == "" {
		return domain.User{}, fmt.Errorf("store: get user by login: %w", ErrNotFound)
	}
	if strings.Contains(login, "@") {
		row, err := s.q.GetUserByEmail(ctx, strings.ToLower(login))
		if err != nil {
			return domain.User{}, mapErr("get user by email", err)
		}
		return userFromRow(row), nil
	}
	row, err := s.q.GetUserByUsername(ctx, strings.ToLower(login))
	if err != nil {
		return domain.User{}, mapErr("get user by username", err)
	}
	return userFromRow(row), nil
}

// ListUsers returns every user ordered by creation time.
func (s *Store) ListUsers(ctx context.Context) ([]domain.User, error) {
	rows, err := s.q.ListUsers(ctx)
	if err != nil {
		return nil, mapErr("list users", err)
	}
	out := make([]domain.User, len(rows))
	for i, r := range rows {
		out[i] = userFromRow(r)
	}
	return out, nil
}

// UpdateUserRole changes a user's role, refusing to demote the last active admin.
func (s *Store) UpdateUserRole(ctx context.Context, id uuid.UUID, role domain.Role) (domain.User, error) {
	if !domain.ValidRole(role) {
		return domain.User{}, fmt.Errorf("store: update user role: invalid role %q", role)
	}
	var out domain.User
	err := s.inTx(ctx, func(q *db.Queries) error {
		cur, err := q.GetUser(ctx, id)
		if err != nil {
			return mapErr("get user", err)
		}
		if domain.Role(cur.Role) == domain.RoleAdmin && role != domain.RoleAdmin && cur.DisabledAt == nil {
			admins, err := q.LockActiveAdmins(ctx)
			if err != nil {
				return mapErr("lock admins", err)
			}
			if len(admins) <= 1 {
				return ErrLastAdmin
			}
		}
		row, err := q.UpdateUserRole(ctx, db.UpdateUserRoleParams{ID: id, Role: string(role)})
		if err != nil {
			return mapErr("update user role", err)
		}
		out = userFromRow(row)
		return nil
	})
	return out, err
}

// SetUserDisabled enables or disables a user, refusing to disable the last
// active admin. Disabling also revokes all active browser sessions for the user.
func (s *Store) SetUserDisabled(ctx context.Context, id uuid.UUID, disabled bool) (domain.User, error) {
	var out domain.User
	err := s.inTx(ctx, func(q *db.Queries) error {
		cur, err := q.GetUser(ctx, id)
		if err != nil {
			return mapErr("get user", err)
		}
		if disabled && domain.Role(cur.Role) == domain.RoleAdmin && cur.DisabledAt == nil {
			admins, err := q.LockActiveAdmins(ctx)
			if err != nil {
				return mapErr("lock admins", err)
			}
			if len(admins) <= 1 {
				return ErrLastAdmin
			}
		}
		var at *time.Time
		if disabled {
			now := time.Now().UTC()
			at = &now
		}
		row, err := q.UpdateUserDisabled(ctx, db.UpdateUserDisabledParams{ID: id, DisabledAt: at})
		if err != nil {
			return mapErr("update user disabled", err)
		}
		if disabled {
			if err := q.DeleteUserSessions(ctx, id); err != nil {
				return mapErr("delete user sessions", err)
			}
		}
		out = userFromRow(row)
		return nil
	})
	return out, err
}

// UpdateUserPassword sets a user's argon2id password hash and revokes any other
// sessions when keepSessionHash is non-empty.
func (s *Store) UpdateUserPassword(ctx context.Context, id uuid.UUID, passwordHash string, keepSessionHash []byte) error {
	return s.inTx(ctx, func(q *db.Queries) error {
		n, err := q.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{ID: id, PasswordHash: passwordHash})
		if err != nil {
			return mapErr("update password", err)
		}
		if n == 0 {
			return fmt.Errorf("store: update password: %w", ErrNotFound)
		}
		if len(keepSessionHash) > 0 {
			if err := q.DeleteOtherUserSessions(ctx, db.DeleteOtherUserSessionsParams{UserID: id, TokenHash: keepSessionHash}); err != nil {
				return mapErr("delete other sessions", err)
			}
		}
		return nil
	})
}

// UpdateUserProfile updates a user's display name and optional email.
func (s *Store) UpdateUserProfile(ctx context.Context, id uuid.UUID, displayName, email string) (domain.User, error) {
	email, err := domain.NormalizeEmail(email)
	if err != nil {
		return domain.User{}, fmt.Errorf("store: update profile: %w", err)
	}
	display := strings.TrimSpace(displayName)
	if len(display) > 100 {
		display = strings.TrimSpace(display[:100])
	}
	row, err := s.q.UpdateUserProfile(ctx, db.UpdateUserProfileParams{
		ID:          id,
		DisplayName: display,
		Email:       nonEmpty(email),
	})
	if err != nil {
		return domain.User{}, mapErr("update profile", err)
	}
	return userFromRow(row), nil
}

// LinkIdentity attaches an external SSO identity to an existing user (D145).
func (s *Store) LinkIdentity(ctx context.Context, in NewIdentity) (domain.UserIdentity, error) {
	return insertIdentity(ctx, s.q, in)
}

func insertIdentity(ctx context.Context, q *db.Queries, in NewIdentity) (domain.UserIdentity, error) {
	if !domain.ValidSSOProvider(in.Provider) {
		return domain.UserIdentity{}, fmt.Errorf("store: link identity: invalid provider %q", in.Provider)
	}
	if strings.TrimSpace(in.Subject) == "" {
		return domain.UserIdentity{}, errors.New("store: link identity: empty subject")
	}
	row, err := q.CreateUserIdentity(ctx, db.CreateUserIdentityParams{
		ID:       domain.NewID(),
		UserID:   in.UserID,
		Provider: string(in.Provider),
		Subject:  in.Subject,
		Email:    strings.TrimSpace(in.Email),
		Login:    strings.TrimSpace(in.Login),
	})
	if err != nil {
		return domain.UserIdentity{}, mapErr("link identity", err)
	}
	return identityFromRow(row), nil
}

// GetIdentity looks up an SSO identity by provider and immutable subject.
func (s *Store) GetIdentity(ctx context.Context, provider domain.SSOProvider, subject string) (domain.UserIdentity, error) {
	row, err := s.q.GetUserIdentityBySubject(ctx, db.GetUserIdentityBySubjectParams{
		Provider: string(provider),
		Subject:  subject,
	})
	if err != nil {
		return domain.UserIdentity{}, mapErr("get identity", err)
	}
	return identityFromRow(row), nil
}

// ListUserIdentities returns all SSO identities linked to userID.
func (s *Store) ListUserIdentities(ctx context.Context, userID uuid.UUID) ([]domain.UserIdentity, error) {
	rows, err := s.q.ListUserIdentities(ctx, userID)
	if err != nil {
		return nil, mapErr("list identities", err)
	}
	out := make([]domain.UserIdentity, len(rows))
	for i, r := range rows {
		out[i] = identityFromRow(r)
	}
	return out, nil
}

// ListAllUserIdentities returns every user's linked identities keyed by user ID.
func (s *Store) ListAllUserIdentities(ctx context.Context) (map[uuid.UUID][]domain.UserIdentity, error) {
	rows, err := s.q.ListAllUserIdentities(ctx)
	if err != nil {
		return nil, mapErr("list all identities", err)
	}
	out := make(map[uuid.UUID][]domain.UserIdentity)
	for _, r := range rows {
		id := identityFromRow(r)
		out[id.UserID] = append(out[id.UserID], id)
	}
	return out, nil
}

// DeleteIdentity unlinks one SSO identity from userID, refusing to remove the
// user's last remaining sign-in method (no password and only one identity).
func (s *Store) DeleteIdentity(ctx context.Context, userID, identityID uuid.UUID) error {
	return s.inTx(ctx, func(q *db.Queries) error {
		u, err := q.GetUser(ctx, userID)
		if err != nil {
			return mapErr("get user", err)
		}
		if u.PasswordHash == "" {
			n, err := q.CountUserIdentities(ctx, userID)
			if err != nil {
				return mapErr("count identities", err)
			}
			if n <= 1 {
				return ErrLastAuthMethod
			}
		}
		n, err := q.DeleteUserIdentity(ctx, db.DeleteUserIdentityParams{ID: identityID, UserID: userID})
		if err != nil {
			return mapErr("delete identity", err)
		}
		if n == 0 {
			return fmt.Errorf("store: delete identity: %w", ErrNotFound)
		}
		return nil
	})
}

// CreateSession stores a hashed browser session token (D144).
func (s *Store) CreateSession(ctx context.Context, userID uuid.UUID, tokenHash []byte, expiresAt time.Time) (domain.Session, error) {
	row, err := s.q.CreateSession(ctx, db.CreateSessionParams{
		ID:        domain.NewID(),
		UserID:    userID,
		TokenHash: tokenHash,
		ExpiresAt: expiresAt.UTC(),
	})
	if err != nil {
		return domain.Session{}, mapErr("create session", err)
	}
	return domain.Session{
		ID: row.ID, UserID: row.UserID, ExpiresAt: row.ExpiresAt, LastSeenAt: row.LastSeenAt, CreatedAt: row.CreatedAt,
	}, nil
}

const touchInterval = 5 * time.Minute

// GetSessionByHash resolves an active session and its active user, updating
// last_seen_at periodically.
func (s *Store) GetSessionByHash(ctx context.Context, tokenHash []byte) (domain.Session, domain.User, error) {
	row, err := s.q.GetSessionByHash(ctx, tokenHash)
	if err != nil {
		return domain.Session{}, domain.User{}, mapErr("get session", err)
	}
	sess := domain.Session{
		ID: row.SessionID, UserID: row.SessionUserID, ExpiresAt: row.SessionExpiresAt,
		LastSeenAt: row.SessionLastSeenAt, CreatedAt: row.SessionCreatedAt,
	}
	user := domain.User{
		ID: row.ID, Username: row.Username, DisplayName: row.DisplayName, Email: deref(row.Email),
		Role: domain.Role(row.Role), PasswordHash: row.PasswordHash, DisabledAt: row.DisabledAt,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if time.Since(sess.LastSeenAt) > touchInterval {
		_ = s.q.TouchSession(ctx, sess.ID)
	}
	return sess, user, nil
}

// DeleteSessionByHash deletes one browser session (logout).
func (s *Store) DeleteSessionByHash(ctx context.Context, tokenHash []byte) error {
	return mapErr("delete session", s.q.DeleteSessionByHash(ctx, tokenHash))
}

// DeleteUserSessions revokes every active browser session for userID.
func (s *Store) DeleteUserSessions(ctx context.Context, userID uuid.UUID) error {
	return mapErr("delete user sessions", s.q.DeleteUserSessions(ctx, userID))
}

// PruneExpiredSessions removes expired session rows.
func (s *Store) PruneExpiredSessions(ctx context.Context) (int64, error) {
	n, err := s.q.PruneExpiredSessions(ctx)
	return n, mapErr("prune sessions", err)
}

// CreateAPIToken inserts a per-person API token (D144, D146).
func (s *Store) CreateAPIToken(ctx context.Context, in NewAPIToken) (domain.APIToken, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return domain.APIToken{}, errors.New("store: create api token: empty name")
	}
	if len(name) > 100 {
		name = strings.TrimSpace(name[:100])
	}
	row, err := s.q.CreateAPIToken(ctx, db.CreateAPITokenParams{
		ID:          domain.NewID(),
		UserID:      in.UserID,
		Name:        name,
		TokenPrefix: in.TokenPrefix,
		TokenHash:   in.TokenHash,
		ExpiresAt:   in.ExpiresAt,
	})
	if err != nil {
		return domain.APIToken{}, mapErr("create api token", err)
	}
	return apiTokenFromRow(row), nil
}

// GetAPITokenByHash resolves a non-revoked, unexpired API token and its active
// owner, updating last_used_at periodically.
func (s *Store) GetAPITokenByHash(ctx context.Context, tokenHash []byte) (domain.APIToken, domain.User, error) {
	row, err := s.q.GetAPITokenByHash(ctx, tokenHash)
	if err != nil {
		return domain.APIToken{}, domain.User{}, mapErr("get api token", err)
	}
	tok := domain.APIToken{
		ID: row.TokenID, UserID: row.TokenUserID, Name: row.TokenName, TokenPrefix: row.TokenPrefix,
		LastUsedAt: row.TokenLastUsedAt, ExpiresAt: row.TokenExpiresAt, RevokedAt: row.TokenRevokedAt,
		CreatedAt: row.TokenCreatedAt,
	}
	user := domain.User{
		ID: row.ID, Username: row.Username, DisplayName: row.DisplayName, Email: deref(row.Email),
		Role: domain.Role(row.Role), PasswordHash: row.PasswordHash, DisabledAt: row.DisabledAt,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if tok.LastUsedAt == nil || time.Since(*tok.LastUsedAt) > time.Minute {
		_ = s.q.TouchAPIToken(ctx, tok.ID)
	}
	return tok, user, nil
}

// ListAPITokens returns all tokens (active and revoked) belonging to userID.
func (s *Store) ListAPITokens(ctx context.Context, userID uuid.UUID) ([]domain.APIToken, error) {
	rows, err := s.q.ListAPITokens(ctx, userID)
	if err != nil {
		return nil, mapErr("list api tokens", err)
	}
	out := make([]domain.APIToken, len(rows))
	for i, r := range rows {
		out[i] = apiTokenFromRow(r)
	}
	return out, nil
}

// RevokeAPIToken revokes a user's API token.
func (s *Store) RevokeAPIToken(ctx context.Context, userID, tokenID uuid.UUID) error {
	n, err := s.q.RevokeAPIToken(ctx, db.RevokeAPITokenParams{ID: tokenID, UserID: userID})
	if err != nil {
		return mapErr("revoke api token", err)
	}
	if n == 0 {
		return fmt.Errorf("store: revoke api token: %w", ErrNotFound)
	}
	return nil
}

// CreateInvite creates a single-use invite (D145).
func (s *Store) CreateInvite(ctx context.Context, in NewInvite) (domain.Invite, error) {
	if !domain.ValidRole(in.Role) {
		return domain.Invite{}, fmt.Errorf("store: create invite: invalid role %q", in.Role)
	}
	email, err := domain.NormalizeEmail(in.Email)
	if err != nil {
		return domain.Invite{}, fmt.Errorf("store: create invite: %w", err)
	}
	row, err := s.q.CreateInvite(ctx, db.CreateInviteParams{
		ID:        domain.NewID(),
		TokenHash: in.TokenHash,
		Email:     email,
		Role:      string(in.Role),
		InvitedBy: in.InvitedBy,
		ExpiresAt: in.ExpiresAt.UTC(),
	})
	if err != nil {
		return domain.Invite{}, mapErr("create invite", err)
	}
	return domain.Invite{
		ID: row.ID, Email: row.Email, Role: domain.Role(row.Role), InvitedBy: row.InvitedBy,
		ExpiresAt: row.ExpiresAt, AcceptedAt: row.AcceptedAt, AcceptedBy: row.AcceptedBy, CreatedAt: row.CreatedAt,
	}, nil
}

// GetInviteByHash looks up an invite by its token hash.
func (s *Store) GetInviteByHash(ctx context.Context, tokenHash []byte) (domain.Invite, error) {
	row, err := s.q.GetInviteByHash(ctx, tokenHash)
	if err != nil {
		return domain.Invite{}, mapErr("get invite", err)
	}
	return domain.Invite{
		ID: row.ID, Email: row.Email, Role: domain.Role(row.Role), InvitedBy: row.InvitedBy,
		InvitedByName: row.InvitedByName, ExpiresAt: row.ExpiresAt, AcceptedAt: row.AcceptedAt,
		AcceptedBy: row.AcceptedBy, CreatedAt: row.CreatedAt,
	}, nil
}

// ListInvites returns every invite newest first.
func (s *Store) ListInvites(ctx context.Context) ([]domain.Invite, error) {
	rows, err := s.q.ListInvites(ctx)
	if err != nil {
		return nil, mapErr("list invites", err)
	}
	out := make([]domain.Invite, len(rows))
	for i, row := range rows {
		out[i] = domain.Invite{
			ID: row.ID, Email: row.Email, Role: domain.Role(row.Role), InvitedBy: row.InvitedBy,
			InvitedByName: row.InvitedByName, ExpiresAt: row.ExpiresAt, AcceptedAt: row.AcceptedAt,
			AcceptedBy: row.AcceptedBy, CreatedAt: row.CreatedAt,
		}
	}
	return out, nil
}

// DeleteInvite removes an invite by ID.
func (s *Store) DeleteInvite(ctx context.Context, id uuid.UUID) error {
	n, err := s.q.DeleteInvite(ctx, id)
	if err != nil {
		return mapErr("delete invite", err)
	}
	if n == 0 {
		return fmt.Errorf("store: delete invite: %w", ErrNotFound)
	}
	return nil
}

// AcceptInvite atomically consumes an unused, unexpired invite, creates the
// invited user with the invite's role, and optionally links an SSO identity.
func (s *Store) AcceptInvite(ctx context.Context, tokenHash []byte, in NewUser, ident *NewIdentity) (domain.User, error) {
	var out domain.User
	err := s.inTx(ctx, func(q *db.Queries) error {
		inv, err := q.GetInviteByHashForUpdate(ctx, tokenHash)
		if err != nil {
			return mapErr("get invite", err)
		}
		if inv.AcceptedAt != nil || !time.Now().UTC().Before(inv.ExpiresAt) {
			return ErrInviteInvalid
		}
		if inv.Email != "" {
			if ident == nil && strings.TrimSpace(in.Email) == "" {
				in.Email = strings.ToLower(inv.Email)
			} else {
				email, err := domain.NormalizeEmail(in.Email)
				if err != nil || email != strings.ToLower(inv.Email) {
					return ErrInviteInvalid
				}
				in.Email = email
			}
		}
		in.Role = domain.Role(inv.Role)
		if ident != nil && in.Username == "" {
			base := domain.SuggestUsername(ident.Login, in.Email)
			username := base
			for attempt := 0; attempt < 20; attempt++ {
				if attempt > 0 {
					suffix := fmt.Sprintf("-%d", attempt+1)
					trim := min(len(base), 64-len(suffix))
					username = base[:trim] + suffix
				}
				if _, err := q.GetUserByUsername(ctx, username); err != nil {
					if errors.Is(mapErr("check username", err), ErrNotFound) {
						break
					}
					return mapErr("check username", err)
				}
			}
			in.Username = username
		}
		u, err := insertUser(ctx, q, in)
		if err != nil {
			return err
		}
		if ident != nil {
			link := *ident
			link.UserID = u.ID
			if _, err := insertIdentity(ctx, q, link); err != nil {
				return err
			}
		}
		if _, err := q.AcceptInviteRow(ctx, db.AcceptInviteRowParams{ID: inv.ID, AcceptedBy: &u.ID}); err != nil {
			return mapErr("accept invite", err)
		}
		out = u
		return nil
	})
	return out, err
}

func userFromRow(r db.User) domain.User {
	return domain.User{
		ID: r.ID, Username: r.Username, DisplayName: r.DisplayName, Email: deref(r.Email),
		Role: domain.Role(r.Role), PasswordHash: r.PasswordHash, DisabledAt: r.DisabledAt,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func identityFromRow(r db.UserIdentity) domain.UserIdentity {
	return domain.UserIdentity{
		ID: r.ID, UserID: r.UserID, Provider: domain.SSOProvider(r.Provider),
		Subject: r.Subject, Email: r.Email, Login: r.Login, CreatedAt: r.CreatedAt,
	}
}

func apiTokenFromRow(r db.ApiToken) domain.APIToken {
	return domain.APIToken{
		ID: r.ID, UserID: r.UserID, Name: r.Name, TokenPrefix: r.TokenPrefix,
		LastUsedAt: r.LastUsedAt, ExpiresAt: r.ExpiresAt, RevokedAt: r.RevokedAt, CreatedAt: r.CreatedAt,
	}
}
