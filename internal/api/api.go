// Package api is the control plane's HTTP API for the web UI (web/) and the CLI,
// on the API port, which agent pods cannot reach. M1: runs, transcript replay and
// live transcript streaming (docs/spec/07-observability.md, FR-OB-1, FR-OB-2).
// M3: the approvals inbox and decisions on human gates (docs/spec/03, FR-WF-3).
// M4: a run's steps, and steering or aborting a running agent step (docs/spec/04).
// M5: projects, issues, scheduling and spec approval (D59, D62, D64).
// M7: releases (D130). M7.1: the build commit, for previews (D131).
// M10: users, sessions, SSO, roles and per-person API tokens; every API request
// is authenticated and writes record the signed-in person (D144-D147).
package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/misarescu/frankie/internal/auth"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

// AuthConfig configures M10 authentication and session cookies (D144-D147).
type AuthConfig struct {
	// SessionTTL is how long a browser session lasts; default 7 days.
	SessionTTL time.Duration
	// PublicURL is the external origin (e.g. https://frankie.example.com) used
	// for SSO callback URLs, Origin checks and Secure cookies.
	PublicURL string
	// AllowedOrigins are additional trusted origins for cookie-authenticated writes.
	AllowedOrigins []string
	// SecureCookie overrides automatic Secure cookie detection when non-nil.
	SecureCookie *bool
	// StateSigner signs OAuth/OIDC state cookies (required when SSO is enabled).
	StateSigner *auth.StateSigner
	// SSO handles Google, GitLab and GitHub sign-in; nil disables SSO.
	SSO *auth.SSOService
	// LoginIPLimiter and LoginUserLimiter rate-limit password sign-in and invite
	// acceptance; defaults are initialized when nil.
	LoginIPLimiter   *auth.RateLimiter
	LoginUserLimiter *auth.RateLimiter
	// PreviewMode is enabled ONLY in values-preview.yaml (D146 (4)): it keeps
	// the preview judge's read endpoints open and accepts PreviewToken for the
	// preview setup Job. Never enabled in production.
	PreviewMode  bool
	PreviewToken string
}

// Server holds the API's dependencies.
type Server struct {
	Store *store.Store
	Hub   *Hub
	Log   *slog.Logger
	// Signaler delivers human gate decisions, steers and aborts to
	// workflows; nil when the control plane runs without Temporal.
	Signaler Signaler
	// Auth configures sessions, SSO, roles and rate limits (M10, D144-D147).
	Auth AuthConfig
	// Version is the commit the control plane was built from, for GET
	// /api/version and GET /version (D131); empty when the build stamped none.
	Version string
	// MaxConcurrentIssues is the scheduler's limit, for the plans the start
	// endpoint returns (D62).
	MaxConcurrentIssues int
	// hashPasswordFn overrides auth.HashPassword in unit tests.
	hashPasswordFn func(string) (string, error)
}

func (s *Server) hashPassword(password string) (string, error) {
	if s.hashPasswordFn != nil {
		return s.hashPasswordFn(password)
	}
	return auth.HashPassword(password)
}

// Register adds the API routes to mux.
func (s *Server) Register(mux *http.ServeMux) {
	if s.Auth.SessionTTL <= 0 {
		s.Auth.SessionTTL = 7 * 24 * time.Hour
	}
	if s.Auth.LoginIPLimiter == nil {
		s.Auth.LoginIPLimiter = auth.NewRateLimiter(15, 5*time.Minute)
	}
	if s.Auth.LoginUserLimiter == nil {
		s.Auth.LoginUserLimiter = auth.NewRateLimiter(5, 5*time.Minute)
	}

	// Public sign-in surface (D145, D146).
	mux.HandleFunc("GET /api/auth/providers", s.authProviders)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	mux.HandleFunc("GET /api/auth/invites/{token}", s.getInvitePreview)
	mux.HandleFunc("POST /api/auth/invites/accept", s.acceptInvite)
	mux.HandleFunc("GET /api/auth/sso/{provider}/start", s.ssoStart)
	mux.HandleFunc("GET /api/auth/sso/{provider}/callback", s.ssoCallback)

	// Authenticated self-service account & token routes (viewer+).
	mux.HandleFunc("GET /api/auth/me", s.requireAuth(domain.RoleViewer, s.authMe))
	mux.HandleFunc("PATCH /api/auth/me", s.requireAuth(domain.RoleViewer, s.updateMe))
	mux.HandleFunc("POST /api/auth/password", s.requireAuth(domain.RoleViewer, s.changePassword))
	mux.HandleFunc("GET /api/auth/sso/{provider}/link", s.requireAuth(domain.RoleViewer, s.ssoLink))
	mux.HandleFunc("DELETE /api/auth/identities/{id}", s.requireAuth(domain.RoleViewer, s.unlinkIdentity))
	mux.HandleFunc("GET /api/tokens", s.requireAuth(domain.RoleViewer, s.listTokens))
	mux.HandleFunc("POST /api/tokens", s.requireAuth(domain.RoleViewer, s.createToken))
	mux.HandleFunc("DELETE /api/tokens/{id}", s.requireAuth(domain.RoleViewer, s.revokeToken))

	// Administrator user & invite management (admin only, D146).
	mux.HandleFunc("GET /api/users", s.requireAuth(domain.RoleAdmin, s.listUsers))
	mux.HandleFunc("POST /api/users", s.requireAuth(domain.RoleAdmin, s.createUser))
	mux.HandleFunc("PATCH /api/users/{id}", s.requireAuth(domain.RoleAdmin, s.updateUser))
	mux.HandleFunc("DELETE /api/users/{id}/sessions", s.requireAuth(domain.RoleAdmin, s.revokeUserSessions))
	mux.HandleFunc("GET /api/invites", s.requireAuth(domain.RoleAdmin, s.listInvites))
	mux.HandleFunc("POST /api/invites", s.requireAuth(domain.RoleAdmin, s.createInvite))
	mux.HandleFunc("DELETE /api/invites/{id}", s.requireAuth(domain.RoleAdmin, s.deleteInvite))

	// Core reads (viewer+; selected preview-judge reads open only in PreviewMode, D146).
	mux.HandleFunc("GET /api/runs", s.requireAuth(domain.RoleViewer, s.listRuns))
	mux.HandleFunc("GET /api/runs/{id}", s.requireAuth(domain.RoleViewer, s.getRun))
	mux.HandleFunc("GET /api/runs/{id}/events", s.requireAuth(domain.RoleViewer, s.listEvents))
	mux.HandleFunc("GET /api/runs/{id}/stream", s.requireAuth(domain.RoleViewer, s.streamEvents))
	mux.HandleFunc("GET /api/approvals", s.requireAuth(domain.RoleViewer, s.listApprovals))
	mux.HandleFunc("GET /api/runs/{id}/steps", s.requireRead(true, s.listSteps))
	mux.HandleFunc("GET /api/runs/{id}/plan", s.requireAuth(domain.RoleViewer, s.listPlan))
	mux.HandleFunc("GET /api/questions", s.requireAuth(domain.RoleViewer, s.listQuestions))
	mux.HandleFunc("GET /api/projects", s.requireAuth(domain.RoleViewer, s.listProjects))
	mux.HandleFunc("GET /api/issues", s.requireRead(true, s.listIssues))
	mux.HandleFunc("GET /api/issues/{key}", s.requireRead(true, s.getIssue))
	mux.HandleFunc("GET /api/specs", s.requireAuth(domain.RoleViewer, s.listSpecs))
	mux.HandleFunc("GET /api/releases", s.requireAuth(domain.RoleViewer, s.listReleases))
	mux.HandleFunc("GET /api/releases/{id}", s.requireAuth(domain.RoleViewer, s.getRelease))
	mux.HandleFunc("GET /api/version", s.requireRead(true, s.GetVersion))

	// Operational writes (member+, D146).
	mux.HandleFunc("POST /api/gates/{id}/decision", s.requireAuth(domain.RoleMember, s.decideGate))
	mux.HandleFunc("POST /api/steps/{id}/steer", s.requireAuth(domain.RoleMember, s.steerStep))
	mux.HandleFunc("POST /api/steps/{id}/abort", s.requireAuth(domain.RoleMember, s.abortStep))
	mux.HandleFunc("POST /api/issues/start", s.requireAuth(domain.RoleMember, s.startIssues))
	mux.HandleFunc("POST /api/issues/{key}/retry", s.requireAuth(domain.RoleMember, s.retryIssue))
	mux.HandleFunc("POST /api/issues/{key}/complete", s.requireAuth(domain.RoleMember, s.completeIssue))
	mux.HandleFunc("POST /api/issues/{key}/cancel", s.requireAuth(domain.RoleMember, s.cancelIssue))
	mux.HandleFunc("POST /api/issues/{key}/spec", s.requireAuth(domain.RoleMember, s.decideSpec))
	mux.HandleFunc("POST /api/questions/{id}/answer", s.requireAuth(domain.RoleMember, s.answerQuestion))
}
