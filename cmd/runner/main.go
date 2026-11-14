// Command runner runs inside every agent pod and wraps the agent harness behind
// the RunnerService gRPC interface (docs/spec/04-agents-harnesses-models.md).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"

	runnerv1 "github.com/misarescu/frankie/gen/frankie/runner/v1"
	"github.com/misarescu/frankie/internal/runner"
	"github.com/misarescu/frankie/internal/runner/claudecode"
	"github.com/misarescu/frankie/internal/runner/opencode"
	"github.com/misarescu/frankie/internal/runner/pi"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "runner:", err)
		os.Exit(1)
	}
}

func run() error {
	listen := flag.String("listen", ":7070", "gRPC listen address")
	workdir := flag.String("workdir", "/workspace", "directory the harness works in")
	claudeBin := flag.String("claude-bin", "claude", "Claude Code executable")
	opencodeBin := flag.String("opencode-bin", "opencode", "opencode executable")
	piBin := flag.String("pi-bin", "pi", "pi executable")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	lis, err := (&net.ListenConfig{}).Listen(ctx, "tcp", *listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", *listen, err)
	}
	srv := grpc.NewServer()
	runnerv1.RegisterRunnerServiceServer(srv, runner.NewServer(*workdir, map[runnerv1.Harness]runner.Harness{
		runnerv1.Harness_HARNESS_CLAUDE_CODE: &claudecode.Harness{Binary: *claudeBin},
		runnerv1.Harness_HARNESS_OPENCODE:    &opencode.Harness{Binary: *opencodeBin},
		runnerv1.Harness_HARNESS_PI:          &pi.Harness{Binary: *piBin},
	}))

	go func() {
		<-ctx.Done()
		logger.Info("shutting down")
		// Events streams can follow a live session indefinitely; don't wait for them forever.
		stopped := make(chan struct{})
		go func() { srv.GracefulStop(); close(stopped) }()
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			srv.Stop()
		}
	}()

	logger.Info("serving", "addr", lis.Addr().String(), "workdir", *workdir)
	if err := srv.Serve(lis); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}
