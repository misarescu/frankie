package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/auth"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

type createUserRequest struct {
	Username    string      `json:"username"`
	DisplayName string      `json:"display_name"`
	Email       string      `json:"email"`
	Role        domain.Role `json:"role"`
	Password    string      `json:"password"`
}

type updateUserRequest struct {
	Role     *domain.Role `json:"role,omitempty"`
	Disabled *bool        `json:"disabled,omitempty"`
}

type inviteJSON struct {
	ID            uuid.UUID   `json:"id"`
	Email         string      `json:"email,omitempty"`
	Role          domain.Role `json:"role"`
	InvitedByName string      `json:"invited_by,omitempty"`
	ExpiresAt     time.Time   `json:"expires_at"`
	AcceptedAt    *time.Time  `json:"accepted_at,omitempty"`
	CreatedAt     time.Time   `json:"created_at"`
}

func toInviteJSON(in domain.Invite) inviteJSON {
	return inviteJSON{
		ID:            in.ID,
		Email:         in.Email,
		Role:          in.Role,
		InvitedByName: in.InvitedByName,
		ExpiresAt:     in.ExpiresAt,
		AcceptedAt:    in.AcceptedAt,
		CreatedAt:     in.CreatedAt,
	}
}

// listUsers returns all users and their linked SSO identities (admin only, D146).
func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.Store.ListUsers(r.Context())
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	identMap, err := s.Store.ListAllUserIdentities(r.Context())
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	out := make([]userJSON, len(users))
	for i, u := range users {
		out[i] = toUserJSON(u, identMap[u.ID])
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// createUser lets an administrator create a user with a local password directly.
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Role == "" {
		req.Role = domain.RoleMember
	}
	if !domain.ValidRole(req.Role) {
		http.Error(w, "role must be admin, member or viewer", http.StatusBadRequest)
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	u, err := s.Store.CreateUser(r.Context(), store.NewUser{
		Username:     req.Username,
		DisplayName:  req.DisplayName,
		Email:        req.Email,
		Role:         req.Role,
		PasswordHash: hash,
	})
	switch {
	case errors.Is(err, store.ErrConflict):
		http.Error(w, "username or email already exists", http.StatusConflict)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.Log.Info("user created", "user", u.Username, "role", u.Role, "by", actorName(r))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(toUserJSON(u, nil))
}

// updateUser lets an administrator change a user's role or disable/enable them.
func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}
	var req updateUserRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Role == nil && req.Disabled == nil {
		http.Error(w, "role or disabled is required", http.StatusBadRequest)
		return
	}
	var u domain.User
	if req.Role != nil {
		if !domain.ValidRole(*req.Role) {
			http.Error(w, "role must be admin, member or viewer", http.StatusBadRequest)
			return
		}
		u, err = s.Store.UpdateUserRole(r.Context(), id, *req.Role)
		if handleUserUpdateErr(w, r, err) {
			return
		}
	}
	if req.Disabled != nil {
		u, err = s.Store.SetUserDisabled(r.Context(), id, *req.Disabled)
		if handleUserUpdateErr(w, r, err) {
			return
		}
	}
	ids, _ := s.Store.ListUserIdentities(r.Context(), u.ID)
	s.Log.Info("user updated", "user", u.Username, "role", u.Role, "disabled", !u.Active(), "by", actorName(r))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(toUserJSON(u, ids))
}

func handleUserUpdateErr(w http.ResponseWriter, r *http.Request, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, store.ErrLastAdmin):
		http.Error(w, "cannot demote or disable the last active administrator", http.StatusConflict)
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	default:
		http.Error(w, "store error", http.StatusInternalServerError)
	}
	return true
}

func (s *Server) revokeUserSessions(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}
	if err := s.Store.DeleteUserSessions(r.Context(), id); err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	s.Log.Info("user sessions revoked", "target_user_id", id, "by", actorName(r))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listInvites(w http.ResponseWriter, r *http.Request) {
	invites, err := s.Store.ListInvites(r.Context())
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	out := make([]inviteJSON, len(invites))
	for i, in := range invites {
		out[i] = toInviteJSON(in)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

type createInviteRequest struct {
	Email          string      `json:"email"`
	Role           domain.Role `json:"role"`
	ExpiresInHours int         `json:"expires_in_hours"`
}

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request) {
	actor, _ := actorFromContext(r.Context())
	var req createInviteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Role == "" {
		req.Role = domain.RoleMember
	}
	if !domain.ValidRole(req.Role) {
		http.Error(w, "role must be admin, member or viewer", http.StatusBadRequest)
		return
	}
	hours := req.ExpiresInHours
	if hours <= 0 {
		hours = 72
	}
	if hours > 24*30 {
		http.Error(w, "expires_in_hours cannot exceed 720 (30 days)", http.StatusBadRequest)
		return
	}
	raw, hash, err := auth.GenerateInviteToken()
	if err != nil {
		http.Error(w, "could not generate invite", http.StatusInternalServerError)
		return
	}
	inv, err := s.Store.CreateInvite(r.Context(), store.NewInvite{
		TokenHash: hash,
		Email:     req.Email,
		Role:      req.Role,
		InvitedBy: &actor.User.ID,
		ExpiresAt: time.Now().UTC().Add(time.Duration(hours) * time.Hour),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	inv.InvitedByName = actor.User.ActorName()
	s.Log.Info("invite created", "invite", inv.ID, "role", inv.Role, "by", actorName(r))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"invite": toInviteJSON(inv),
		"token":  raw,
		"path":   "/invite/" + raw,
	})
}

func (s *Server) deleteInvite(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid invite id", http.StatusBadRequest)
		return
	}
	err = s.Store.DeleteInvite(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getInvitePreview returns non-sensitive details of an invite token so the
// /invite/<token> page can show the role and optional email before acceptance.
func (s *Server) getInvitePreview(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimSpace(r.PathValue("token"))
	if tok == "" {
		http.NotFound(w, r)
		return
	}
	inv, err := s.Store.GetInviteByHash(r.Context(), auth.HashToken(tok))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	if !inv.Usable(time.Now().UTC()) {
		http.Error(w, "this invite has expired or was already used", http.StatusGone)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"role":       inv.Role,
		"email":      inv.Email,
		"expires_at": inv.ExpiresAt,
	})
}

type acceptInviteRequest struct {
	Token       string `json:"token"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Password    string `json:"password"`
}

// acceptInvite creates a password account from a valid invite and signs in.
func (s *Server) acceptInvite(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "" && !s.validOrigin(r) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	ip := clientIP(r)
	if ok, retry := s.Auth.LoginIPLimiter.Allow(ip); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(max(int(retry.Seconds()), 1)))
		http.Error(w, "too many attempts; try again later", http.StatusTooManyRequests)
		return
	}
	var req acceptInviteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	userKey := strings.ToLower(strings.TrimSpace(req.Username))
	if userKey != "" {
		if ok, retry := s.Auth.LoginUserLimiter.Allow(userKey); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(max(int(retry.Seconds()), 1)))
			http.Error(w, "too many attempts; try again later", http.StatusTooManyRequests)
			return
		}
	}
	tok := strings.TrimSpace(req.Token)
	if tok == "" {
		http.Error(w, "invite token is required", http.StatusBadRequest)
		return
	}
	tokHash := auth.HashToken(tok)
	inv, err := s.Store.GetInviteByHash(r.Context(), tokHash)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "invite is invalid, expired, or does not match the email", http.StatusGone)
		return
	}
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	if !inv.Usable(time.Now().UTC()) {
		http.Error(w, "invite is invalid, expired, or does not match the email", http.StatusGone)
		return
	}
	if inv.Email != "" && strings.TrimSpace(req.Email) != "" {
		email, err := domain.NormalizeEmail(req.Email)
		if err != nil || email != strings.ToLower(inv.Email) {
			http.Error(w, "invite is invalid, expired, or does not match the email", http.StatusGone)
			return
		}
	}
	if _, err := domain.NormalizeUsername(req.Username); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := domain.NormalizeEmail(req.Email); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	hash, err := s.hashPassword(req.Password)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	u, err := s.Store.AcceptInvite(r.Context(), tokHash, store.NewUser{
		Username:     req.Username,
		DisplayName:  req.DisplayName,
		Email:        req.Email,
		PasswordHash: hash,
	}, nil)
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrInviteInvalid):
		http.Error(w, "invite is invalid, expired, or does not match the email", http.StatusGone)
		return
	case errors.Is(err, store.ErrConflict):
		http.Error(w, "username or email already exists", http.StatusConflict)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if userKey != "" {
		s.Auth.LoginUserLimiter.Reset(userKey)
	}

	raw, sessHash, err := auth.GenerateSessionToken()
	if err != nil {
		http.Error(w, "could not create session", http.StatusInternalServerError)
		return
	}
	if _, err := s.Store.CreateSession(r.Context(), u.ID, sessHash, time.Now().UTC().Add(s.Auth.SessionTTL)); err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	s.setSessionCookie(w, r, raw)
	s.Log.Info("invite accepted", "user", u.Username, "role", u.Role)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"user": toUserJSON(u, nil)})
}
