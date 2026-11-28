// Command controlplane runs the Frankie control plane: store, git proxy for
// agent pods, API, Temporal worker and issue intake.
//
// Non-secret configuration is a JSON file (FRANKIE_CONFIG, rendered by the Helm
// chart); secrets come from the environment:
//
//	FRANKIE_DATABASE_URL             Postgres URL (required)
//	FRANKIE_GITPROXY_KEY             git proxy token signing key, >= 32 bytes (required)
//	FRANKIE_EGRESS_KEY               egress token signing key, >= 32 bytes (with egressProjectAllow, D80)
//	FRANKIE_AUTH_STATE_KEY           OAuth state signing key, >= 32 bytes (required when SSO is enabled, D145)
//	FRANKIE_BOOTSTRAP_ADMIN_USERNAME initial admin username when DB has no users (default "admin", D145)
//	FRANKIE_BOOTSTRAP_ADMIN_PASSWORD initial admin password when DB has no users (D145)
//	FRANKIE_BOOTSTRAP_ADMIN_NAME     initial admin display name (optional)
//	FRANKIE_BOOTSTRAP_ADMIN_EMAIL    initial admin email (optional)
//	FRANKIE_SSO_GOOGLE_CLIENT_ID / FRANKIE_SSO_GOOGLE_CLIENT_SECRET
//	FRANKIE_SSO_GITLAB_CLIENT_ID / FRANKIE_SSO_GITLAB_CLIENT_SECRET
//	FRANKIE_SSO_GITHUB_CLIENT_ID / FRANKIE_SSO_GITHUB_CLIENT_SECRET
//	FRANKIE_API_TOKEN                preview-mode static bearer token, >= 32 bytes (preview only, D146)
//	FRANKIE_GITHUB_TOKEN             fine-grained PAT for the project repos (D26)
//	FRANKIE_GATEWAY_MASTER_KEY       LLM gateway admin key
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/misarescu/frankie/internal/controlplane"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "controlplane:", err)
		os.Exit(1)
	}
}

func run() error {
	apiAddr := flag.String("api-listen", ":8080", "API and health listen address")
	gitAddr := flag.String("gitproxy-listen", ":8081", "git proxy listen address (agent pods)")
	configPath := flag.String("config", os.Getenv("FRANKIE_CONFIG"), "JSON configuration file")
	flag.Parse()

	cfg := controlplane.Config{
		DatabaseURL:      os.Getenv("FRANKIE_DATABASE_URL"),
		GitProxyKey:      []byte(os.Getenv("FRANKIE_GITPROXY_KEY")),
		EgressKey:        []byte(os.Getenv("FRANKIE_EGRESS_KEY")),
		APIToken:         os.Getenv("FRANKIE_API_TOKEN"),
		GitHubToken:      os.Getenv("FRANKIE_GITHUB_TOKEN"),
		GatewayMasterKey: os.Getenv("FRANKIE_GATEWAY_MASTER_KEY"),
		Auth: controlplane.AuthConfig{
			StateKey: []byte(os.Getenv("FRANKIE_AUTH_STATE_KEY")),
			BootstrapAdmin: controlplane.BootstrapAdminConfig{
				Username:    os.Getenv("FRANKIE_BOOTSTRAP_ADMIN_USERNAME"),
				Password:    os.Getenv("FRANKIE_BOOTSTRAP_ADMIN_PASSWORD"),
				DisplayName: os.Getenv("FRANKIE_BOOTSTRAP_ADMIN_NAME"),
				Email:       os.Getenv("FRANKIE_BOOTSTRAP_ADMIN_EMAIL"),
			},
			GoogleClientID:     os.Getenv("FRANKIE_SSO_GOOGLE_CLIENT_ID"),
			GoogleClientSecret: os.Getenv("FRANKIE_SSO_GOOGLE_CLIENT_SECRET"),
			GitLabClientID:     os.Getenv("FRANKIE_SSO_GITLAB_CLIENT_ID"),
			GitLabClientSecret: os.Getenv("FRANKIE_SSO_GITLAB_CLIENT_SECRET"),
			GitHubClientID:     os.Getenv("FRANKIE_SSO_GITHUB_CLIENT_ID"),
			GitHubClientSecret: os.Getenv("FRANKIE_SSO_GITHUB_CLIENT_SECRET"),
		},
	}
	if cfg.DatabaseURL == "" {
		return errors.New("FRANKIE_DATABASE_URL is required")
	}
	if *configPath != "" {
		if err := controlplane.LoadConfigFile(*configPath, &cfg); err != nil {
			return err
		}
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		rc, err := rest.InClusterConfig()
		if err != nil {
			return fmt.Errorf("kubernetes config: %w", err)
		}
		if cfg.Kube, err = kubernetes.NewForConfig(rc); err != nil {
			return fmt.Errorf("kubernetes client: %w", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	lc := net.ListenConfig{}
	apiLis, err := lc.Listen(ctx, "tcp", *apiAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", *apiAddr, err)
	}
	gitLis, err := lc.Listen(ctx, "tcp", *gitAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", *gitAddr, err)
	}
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	return controlplane.Run(ctx, cfg, controlplane.Listeners{API: apiLis, GitProxy: gitLis}, log)
}
