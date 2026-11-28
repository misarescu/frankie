package controlplane

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/misarescu/frankie/internal/api"
	"github.com/misarescu/frankie/internal/auth"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

// BootstrapAdminConfig seeds the first admin account on an empty database
// (D145). Ignored once any user row exists.
type BootstrapAdminConfig struct {
	Username    string
	Password    string
	DisplayName string
	Email       string
}

// AuthConfig configures M10 sign-in, sessions, SSO and preview mode (D144-D147).
type AuthConfig struct {
	PublicURL      string
	AllowedOrigins []string
	SessionTTL     time.Duration
	SecureCookie   *bool
	StateKey       []byte
	PreviewMode    bool
	BootstrapAdmin BootstrapAdminConfig
	Allowlist      auth.Allowlist

	GoogleEnabled      bool
	GoogleIssuerURL    string
	GoogleClientID     string
	GoogleClientSecret string

	GitLabEnabled      bool
	GitLabIssuerURL    string
	GitLabClientID     string
	GitLabClientSecret string

	GitHubEnabled      bool
	GitHubClientID     string
	GitHubClientSecret string
}

func validateAuthConfig(cfg Config) error {
	a := cfg.Auth
	if a.PublicURL != "" {
		u, err := url.Parse(strings.TrimSpace(a.PublicURL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("controlplane: auth.publicURL %q must be an http(s) URL", a.PublicURL)
		}
	}
	if a.Allowlist.DefaultRole != "" && a.Allowlist.DefaultRole != domain.RoleViewer && a.Allowlist.DefaultRole != domain.RoleMember {
		return fmt.Errorf("controlplane: auth.allowlist.defaultRole %q must be viewer or member", a.Allowlist.DefaultRole)
	}
	if a.PreviewMode {
		if a.PublicURL != "" {
			return errors.New("controlplane: auth.previewMode cannot be enabled with auth.publicURL")
		}
		if len(cfg.APIToken) < api.MinTokenLen {
			return fmt.Errorf("controlplane: API token must be at least %d bytes when auth.previewMode is enabled", api.MinTokenLen)
		}
	}
	if len(a.StateKey) > 0 && len(a.StateKey) < auth.MinStateKeyLen {
		return fmt.Errorf("controlplane: FRANKIE_AUTH_STATE_KEY must be at least %d bytes", auth.MinStateKeyLen)
	}
	hasSSO := a.GoogleEnabled || a.GitLabEnabled || a.GitHubEnabled
	if hasSSO && len(a.StateKey) < auth.MinStateKeyLen {
		return fmt.Errorf("controlplane: FRANKIE_AUTH_STATE_KEY must be at least %d bytes when SSO is enabled", auth.MinStateKeyLen)
	}
	if a.GoogleEnabled && (a.GoogleClientID == "" || a.GoogleClientSecret == "") {
		return errors.New("controlplane: Google SSO is enabled but FRANKIE_SSO_GOOGLE_CLIENT_ID/SECRET are not set")
	}
	if a.GitLabEnabled && (a.GitLabClientID == "" || a.GitLabClientSecret == "") {
		return errors.New("controlplane: GitLab SSO is enabled but FRANKIE_SSO_GITLAB_CLIENT_ID/SECRET are not set")
	}
	if a.GitHubEnabled && (a.GitHubClientID == "" || a.GitHubClientSecret == "") {
		return errors.New("controlplane: GitHub SSO is enabled but FRANKIE_SSO_GITHUB_CLIENT_ID/SECRET are not set")
	}
	return nil
}

func buildAuthConfig(cfg Config) (api.AuthConfig, error) {
	a := cfg.Auth
	var providers []auth.ProviderConfig
	if a.GoogleEnabled {
		issuer := a.GoogleIssuerURL
		if issuer == "" {
			issuer = "https://accounts.google.com"
		}
		providers = append(providers, auth.ProviderConfig{
			ID:           domain.ProviderGoogle,
			Name:         "Google",
			ClientID:     a.GoogleClientID,
			ClientSecret: a.GoogleClientSecret,
			IssuerURL:    issuer,
		})
	}
	if a.GitLabEnabled {
		issuer := a.GitLabIssuerURL
		if issuer == "" {
			issuer = "https://gitlab.com"
		}
		providers = append(providers, auth.ProviderConfig{
			ID:           domain.ProviderGitLab,
			Name:         "GitLab",
			ClientID:     a.GitLabClientID,
			ClientSecret: a.GitLabClientSecret,
			IssuerURL:    issuer,
		})
	}
	if a.GitHubEnabled {
		providers = append(providers, auth.ProviderConfig{
			ID:           domain.ProviderGitHub,
			Name:         "GitHub",
			ClientID:     a.GitHubClientID,
			ClientSecret: a.GitHubClientSecret,
		})
	}

	var stateSigner *auth.StateSigner
	if len(a.StateKey) >= auth.MinStateKeyLen {
		var err error
		stateSigner, err = auth.NewStateSigner(a.StateKey)
		if err != nil {
			return api.AuthConfig{}, err
		}
	}
	var ssoSvc *auth.SSOService
	if len(providers) > 0 {
		var err error
		ssoSvc, err = auth.NewSSOService(providers, a.Allowlist, nil)
		if err != nil {
			return api.AuthConfig{}, err
		}
	}
	return api.AuthConfig{
		SessionTTL:     a.SessionTTL,
		PublicURL:      a.PublicURL,
		AllowedOrigins: a.AllowedOrigins,
		SecureCookie:   a.SecureCookie,
		StateSigner:    stateSigner,
		SSO:            ssoSvc,
		PreviewMode:    a.PreviewMode,
		PreviewToken:   cfg.APIToken,
	}, nil
}

func runBootstrapAdmin(ctx context.Context, st *store.Store, b BootstrapAdminConfig, log *slog.Logger) error {
	if b.Password == "" {
		return nil
	}
	if n, err := st.CountUsers(ctx); err != nil {
		return fmt.Errorf("controlplane: bootstrap admin count users: %w", err)
	} else if n > 0 {
		log.Info("bootstrap admin skipped: users already exist")
		return nil
	}
	rawUsername := strings.TrimSpace(b.Username)
	if rawUsername == "" {
		rawUsername = "admin"
	}
	username, err := domain.NormalizeUsername(rawUsername)
	if err != nil {
		return fmt.Errorf("controlplane: bootstrap admin username: %w", err)
	}
	if err := domain.ValidatePassword(b.Password); err != nil {
		return fmt.Errorf("controlplane: bootstrap admin password: %w", err)
	}
	email, err := domain.NormalizeEmail(b.Email)
	if err != nil {
		return fmt.Errorf("controlplane: bootstrap admin email: %w", err)
	}
	displayName := strings.TrimSpace(b.DisplayName)
	if displayName == "" {
		displayName = username
	}
	hash, err := auth.HashPassword(b.Password)
	if err != nil {
		return fmt.Errorf("controlplane: hash bootstrap admin password: %w", err)
	}
	u, created, err := st.BootstrapAdmin(ctx, store.NewUser{
		Username:     username,
		DisplayName:  displayName,
		Email:        email,
		Role:         domain.RoleAdmin,
		PasswordHash: hash,
	})
	if err != nil {
		return fmt.Errorf("controlplane: bootstrap admin: %w", err)
	}
	if created {
		log.Info("bootstrap admin created", "user", u.Username)
	} else {
		log.Info("bootstrap admin skipped: users already exist")
	}
	return nil
}

func pruneSessionsLoop(ctx context.Context, st *store.Store, log *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := st.PruneExpiredSessions(ctx); err != nil {
				log.Warn("prune expired sessions", "error", err)
			} else if n > 0 {
				log.Info("pruned expired sessions", "count", n)
			}
		}
	}
}
