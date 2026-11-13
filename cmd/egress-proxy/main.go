// Command egress-proxy is the only route from agent pods to the internet (D20):
// a CONNECT-only forward proxy with a hostname allowlist.
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
	logger.Info("serving", "addr", lis.Addr().String(), "rules", policy.Len())
	return egress.Serve(ctx, lis, egress.NewProxy(policy, logger))
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
