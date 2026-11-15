// Command egress-proxy is the only route from agent pods to the internet (D20):
// a CONNECT-only forward proxy with a hostname allowlist. With a signing key in
// FRANKIE_EGRESS_KEY and --project-allow, a pod's egress token adds the hosts
// its project declared (D80).
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/misarescu/frankie/internal/egress"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "egress-proxy:", err)
		os.Exit(1)
	}
}

func run() error {
	listen := flag.String("listen", ":3128", "proxy listen address")
	allowFile := flag.String("allow-file", "", "file with one allowlist entry per line (# comments)")
	allow := flag.String("allow", "", "comma-separated allowlist entries, added to --allow-file")
	projectAllow := flag.Bool("project-allow", false, "let egress tokens add their project's hosts (needs FRANKIE_EGRESS_KEY)")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	entries := strings.Split(*allow, ",")
	if *allowFile != "" {
		fromFile, err := readEntries(*allowFile)
		if err != nil {
			return err
		}
		entries = append(entries, fromFile...)
	}
	policy, err := egress.ParsePolicy(entries)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	lis, err := (&net.ListenConfig{}).Listen(ctx, "tcp", *listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", *listen, err)
	}
	proxy := egress.NewProxy(policy, logger)
	if *projectAllow {
		if proxy.Tokens, err = egress.NewSigner([]byte(os.Getenv("FRANKIE_EGRESS_KEY"))); err != nil {
			return fmt.Errorf("--project-allow: FRANKIE_EGRESS_KEY: %w", err)
		}
	}
	logger.Info("serving", "addr", lis.Addr().String(), "rules", policy.Len(), "project_allow", proxy.Tokens != nil)
	return egress.Serve(ctx, lis, proxy)
}

func readEntries(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read allowlist: %w", err)
	}
	defer func() { _ = f.Close() }()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "#")
		out = append(out, line)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read allowlist: %w", err)
	}
	return out, nil
}
