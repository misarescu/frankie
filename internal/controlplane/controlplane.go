// Package controlplane wires the control plane's components into one process
// (docs/spec/01-architecture.md): the store, the git proxy for agent pods, and
// the API. Temporal workers, connectors and the scheduler join as M1 proceeds.
package controlplane

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"k8s.io/client-go/kubernetes"

	"github.com/misarescu/frankie/internal/api"
	"github.com/misarescu/frankie/internal/connectors/github"
	"github.com/misarescu/frankie/internal/gitproxy"
	"github.com/misarescu/frankie/internal/store"
	"github.com/misarescu/frankie/internal/workflow/activities"
)

// Config is the control plane's configuration. Secrets come from the
// environment; the rest from a JSON file rendered by the chart (LoadConfigFile).
type Config struct {
	DatabaseURL string
	// GitProxyKey signs git proxy tokens (D21); at least 32 bytes.
	GitProxyKey []byte
	Repos       []RepoConfig
	// GitHubToken authenticates git traffic and the GitHub API (D26). Empty
	// works for public repos, read-only, and disables intake.
	GitHubToken string
	// DBWait bounds how long startup waits for Postgres (and Temporal); default 2 minutes.
	DBWait time.Duration

	// Workflows run only when TemporalAddress is set.
	TemporalAddress   string
	TemporalNamespace string
	GatewayAdminURL   string
	GatewayMasterKey  string
	Agent             activities.AgentConfig
	Gate              activities.GateConfig
	Services          activities.ServicesConfig
	Toolchain         ToolchainConfig
	Kube              kubernetes.Interface // where agent pods are launched
	IntakeLabel       string
	PollInterval      time.Duration
}

// Listeners are where the control plane serves.
type Listeners struct {
	API      net.Listener // health, readiness, later the API and UI
	GitProxy net.Listener // reachable from agent pods (NetworkPolicy)
}

// Run connects to Postgres, applies migrations and serves until ctx ends.
func Run(ctx context.Context, cfg Config, lis Listeners, log *slog.Logger) error {
	signer, err := gitproxy.NewSigner(cfg.GitProxyKey)
	if err != nil {
		return err
	}
	var authorize func(*http.Request)
	if cfg.GitHubToken != "" {
		authorize = github.TokenAuthorizer(cfg.GitHubToken)
	} else {
		log.Warn("no GitHub token configured: the git proxy can only read public repos")
	}
	upstreams := map[string]string{}
	for _, r := range cfg.Repos {
		upstreams[r.Name] = r.Upstream
	}
	resolver, err := gitproxy.StaticResolver(upstreams, authorize)
	if err != nil {
		return err
	}

	st, err := openStore(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := store.Migrate(ctx, st.Pool()); err != nil {
		return err
	}
	log.Info("database ready", "repos", len(cfg.Repos))
	projects, err := ensureProjects(ctx, st, cfg.Repos)
	if err != nil {
		return err
	}
	if cfg.TemporalAddress != "" {
		stop, err := startWorkflows(ctx, cfg, st, signer, projects, log)
		if err != nil {
			return err
		}
		defer stop()
	}

	hub := api.NewHub(cfg.DatabaseURL, log.With("component", "events"))
	go hub.Run(ctx)
	mux := http.NewServeMux()
	(&api.Server{Store: st, Hub: hub, Log: log}).Register(mux)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := st.Pool().Ping(r.Context()); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	servers := []*http.Server{
		{Handler: mux, ReadHeaderTimeout: 10 * time.Second},
		{Handler: gitproxy.New(signer, resolver, log.With("component", "gitproxy")), ReadHeaderTimeout: 10 * time.Second},
	}
	listeners := []net.Listener{lis.API, lis.GitProxy}
	errs := make(chan error, len(servers))
	for i, srv := range servers {
		go func() {
			if err := srv.Serve(listeners[i]); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errs <- err
			}
		}()
	}
	log.Info("serving", "api", lis.API.Addr().String(), "gitproxy", lis.GitProxy.Addr().String())

	select {
	case <-ctx.Done():
	case err := <-errs:
		return fmt.Errorf("controlplane: serve: %w", err)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, srv := range servers {
		_ = srv.Shutdown(shutdownCtx)
	}
	return nil
}

// openStore retries until Postgres accepts connections: in a cluster the
// control plane may start before the database is ready.
func openStore(ctx context.Context, cfg Config, log *slog.Logger) (*store.Store, error) {
	wait := cfg.DBWait
	if wait <= 0 {
		wait = 2 * time.Minute
	}
	deadline := time.Now().Add(wait)
	for {
		st, err := store.Open(ctx, cfg.DatabaseURL)
		if err == nil {
			return st, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("controlplane: database not reachable after %s: %w", wait, err)
		}
		log.Info("waiting for database", "error", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
