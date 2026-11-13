// Command controlplane runs the Frankie control plane: store, git proxy for
// agent pods, API, and (as M1 proceeds) Temporal workers and connectors.
//
// Configuration comes from flags and the environment:
//
//	FRANKIE_DATABASE_URL  Postgres URL (required)
//	FRANKIE_GITPROXY_KEY  git proxy token signing key, >= 32 bytes (required)
//	FRANKIE_GITHUB_TOKEN  fine-grained PAT for the project repos (D26, optional)
//	FRANKIE_REPOS         comma-separated name=upstream pairs, e.g.
//	                      misarescu/frankie-toy=https://github.com/misarescu/frankie-toy.git
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
	"strings"
	"syscall"

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
	flag.Parse()

	repos, err := parseRepos(os.Getenv("FRANKIE_REPOS"))
	if err != nil {
		return err
	}
	cfg := controlplane.Config{
		DatabaseURL: os.Getenv("FRANKIE_DATABASE_URL"),
		GitProxyKey: []byte(os.Getenv("FRANKIE_GITPROXY_KEY")),
		GitHubToken: os.Getenv("FRANKIE_GITHUB_TOKEN"),
		Repos:       repos,
	}
	if cfg.DatabaseURL == "" {
		return errors.New("FRANKIE_DATABASE_URL is required")
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

// parseRepos reads "name=url,name=url".
func parseRepos(s string) (map[string]string, error) {
	repos := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		name, upstream, ok := strings.Cut(pair, "=")
		if !ok || name == "" || upstream == "" {
			return nil, fmt.Errorf("FRANKIE_REPOS: %q is not name=upstream", pair)
		}
		repos[name] = upstream
	}
	return repos, nil
}
