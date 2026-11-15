// Command frankie-imagebuild builds a toolchain or dependency image by
// appending one layer (D81). It ships in the runner base image, so a build pod
// runs it from the image it extends:
//
//	frankie-imagebuild layer --dir DIR --run CMD --out FILE [--context TAR] [--exclude PATH]...
//	frankie-imagebuild push --base REF --layer FILE --target REPO [--mirror REPO] [--env K=V]... [--prepend-path DIR] [--insecure HOST]...
//
// push writes the image digest to the termination message (D45).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"

	"github.com/misarescu/frankie/internal/imagebuild"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "frankie-imagebuild:", err)
		stop()
		os.Exit(1)
	}
}

type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(v string) error { *l = append(*l, v); return nil }

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: frankie-imagebuild layer|push [flags]")
	}
	switch args[0] {
	case "layer":
		return layer(ctx, args[1:])
	case "push":
		return push(ctx, args[1:])
	default:
		return fmt.Errorf("unknown command %q, want layer or push", args[0])
	}
}

func layer(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("layer", flag.ContinueOnError)
	var o imagebuild.LayerOptions
	var exclude list
	fs.StringVar(&o.Context, "context", "", "tar archive of files to place in --dir")
	fs.StringVar(&o.Dir, "dir", "", "directory the context goes to and the install runs in")
	fs.StringVar(&o.Run, "run", "", "the install, run with /bin/sh -c")
	fs.StringVar(&o.Out, "out", "", "layer archive to write")
	fs.StringVar(&o.Marker, "marker", "/tmp/frankie-imagebuild.start", "marks the start; on the root filesystem")
	fs.Var(&exclude, "exclude", "path left out of the layer (repeatable), in addition to "+strings.Join(imagebuild.DefaultExclude, " "))
	if err := fs.Parse(args); err != nil {
		return err
	}
	o.Root = "/"
	o.Exclude = append(append([]string{}, imagebuild.DefaultExclude...), exclude...)
	o.Exclude = append(o.Exclude, o.Out, o.Marker)
	o.Stdout, o.Stderr = os.Stdout, os.Stderr
	start := time.Now()
	stats, err := imagebuild.Layer(ctx, o)
	if err != nil {
		return err
	}
	fmt.Printf("frankie-imagebuild: layer of %d entries, %d MB, in %s\n", stats.Entries, stats.Bytes>>20, time.Since(start).Round(time.Second))
	return nil
}

func push(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	var o imagebuild.PushOptions
	var env, insecure list
	report := fs.String("report", "/dev/termination-log", "file the digest is written to")
	fs.StringVar(&o.Base, "base", "", "image the layer is appended to")
	fs.StringVar(&o.Mirror, "mirror", "", "repository the base image is copied into first")
	fs.StringVar(&o.Layer, "layer", "", "layer archive")
	fs.StringVar(&o.Target, "target", "", "repository pushed to, by digest")
	fs.StringVar(&o.PrependPath, "prepend-path", "", "directory put in front of the image's PATH")
	fs.StringVar(&o.CreatedBy, "created-by", "frankie-imagebuild", "the layer's history entry")
	fs.Var(&env, "env", "KEY=VALUE set in the image (repeatable)")
	fs.Var(&insecure, "insecure", "registry host reached over plain HTTP (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	o.Env, o.Insecure = env, insecure
	o.Platform = v1.Platform{OS: "linux", Architecture: runtime.GOARCH}
	o.Log = os.Stdout
	digest, err := imagebuild.Push(ctx, o)
	if err != nil {
		return err
	}
	// The key is the one BuildKit's metadata file used, which the control
	// plane reads (images.KubeBuilder).
	msg, err := json.Marshal(map[string]string{"containerimage.digest": digest.String()})
	if err != nil {
		return err
	}
	if err := os.WriteFile(*report, msg, 0o644); err != nil {
		return fmt.Errorf("report digest: %w", err)
	}
	fmt.Println(digest.String())
	return nil
}
