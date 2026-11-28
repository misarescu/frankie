package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/auth"
	"github.com/misarescu/frankie/internal/store"
)

type apiTokenJSON struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	TokenPrefix string     `json:"token_prefix"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	actor, _ := actorFromContext(r.Context())
	tokens, err := s.Store.ListAPITokens(r.Context(), actor.User.ID)
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	out := make([]apiTokenJSON, len(tokens))
	for i, t := range tokens {
		out[i] = apiTokenJSON{
			ID: t.ID, Name: t.Name, TokenPrefix: t.TokenPrefix,
			LastUsedAt: t.LastUsedAt, ExpiresAt: t.ExpiresAt, RevokedAt: t.RevokedAt, CreatedAt: t.CreatedAt,
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

type createTokenRequest struct {
	Name          string `json:"name"`
	ExpiresInDays int    `json:"expires_in_days"`
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	actor, _ := actorFromContext(r.Context())
	var req createTokenRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 100 {
		http.Error(w, "name must be 1 to 100 characters", http.StatusBadRequest)
		return
	}
	var expiresAt *time.Time
	if req.ExpiresInDays < 0 || req.ExpiresInDays > 365 {
		http.Error(w, "expires_in_days must be between 0 and 365", http.StatusBadRequest)
		return
	}
	if req.ExpiresInDays > 0 {
		t := time.Now().UTC().Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
		expiresAt = &t
	}
	raw, prefix, hash, err := auth.GenerateAPIToken()
	if err != nil {
		http.Error(w, "could not generate token", http.StatusInternalServerError)
		return
	}
	tok, err := s.Store.CreateAPIToken(r.Context(), store.NewAPIToken{
		UserID:      actor.User.ID,
		Name:        name,
		TokenPrefix: prefix,
		TokenHash:   hash,
		ExpiresAt:   expiresAt,
	})
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	s.Log.Info("api token created", "user", actor.User.Username, "token", tok.TokenPrefix)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"token": apiTokenJSON{
			ID: tok.ID, Name: tok.Name, TokenPrefix: tok.TokenPrefix,
			LastUsedAt: tok.LastUsedAt, ExpiresAt: tok.ExpiresAt, RevokedAt: tok.RevokedAt, CreatedAt: tok.CreatedAt,
		},
		"secret": raw,
	})
}

func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request) {
	actor, _ := actorFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid token id", http.StatusBadRequest)
		return
	}
	err = s.Store.RevokeAPIToken(r.Context(), actor.User.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	s.Log.Info("api token revoked", "user", actor.User.Username, "token_id", id)
	w.WriteHeader(http.StatusNoContent)
}
