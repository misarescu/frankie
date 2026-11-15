// Package imagebuild builds a toolchain or dependency image by appending one
// layer to an image that already exists, without BuildKit (D81,
// docs/spec/05-sandbox-and-toolchains.md). A build pod runs it twice, both
// times in the image it extends and as the unprivileged agent user: Layer
// runs the install and packs every file it created or changed into a tar
// archive; Push appends that archive to the image in the registry and reports
// the new image's digest.
package imagebuild

import (
	"archive/tar"
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DefaultExclude are the paths of a container that are never part of a
// layer: kernel filesystems, scratch space and the files the kubelet mounts.
var DefaultExclude = []string{"/proc", "/sys", "/dev", "/tmp", "/run", "/etc/hosts", "/etc/hostname", "/etc/resolv.conf"}

// LayerOptions describe one install.
type LayerOptions struct {
	// Context is a tar archive of regular files placed in Dir before Run;
	// empty means none.
	Context string
	// Dir is where the context goes and Run runs; it is created if missing.
	Dir string
	// Run is the install, run with /bin/sh -c.
	Run string
	// Out is the layer archive written.
	Out string
	// Root is the filesystem searched for changes, "/" in a build pod.
	Root string
	// Marker is a file created before anything else; files whose change time
	// is not before its own make up the layer. It must be on Root's
	// filesystem, which sets the change times, and under Exclude.
	Marker string
	// Exclude are absolute paths that are not searched: mounts, the marker
	// and Out.
	Exclude []string
	// Stdout and Stderr receive the install's output; nil discards it.
	Stdout, Stderr io.Writer
}

// LayerStats describe the layer written.
type LayerStats struct {
	Entries int
	Bytes   int64
}

// Layer runs the install and writes every file, directory and link it
// created or changed under Root to Out. Deletions are not recorded: an
// appended layer only adds, which installs and dependency warm-ups need.
func Layer(ctx context.Context, o LayerOptions) (LayerStats, error) {
	if o.Dir == "" || o.Run == "" || o.Out == "" || o.Root == "" || o.Marker == "" {
		return LayerStats{}, errors.New("imagebuild: dir, run, out, root and marker are required")
	}
	since, err := mark(o.Marker)
	if err != nil {
		return LayerStats{}, err
	}
	if err := os.MkdirAll(o.Dir, 0o755); err != nil {
		return LayerStats{}, fmt.Errorf("imagebuild: %w", err)
	}
	if o.Context != "" {
		if err := unpack(o.Context, o.Dir); err != nil {
			return LayerStats{}, err
		}
	}
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", o.Run)
	cmd.Dir = o.Dir
	cmd.Stdout, cmd.Stderr = o.Stdout, o.Stderr
	if err := cmd.Run(); err != nil {
		return LayerStats{}, fmt.Errorf("imagebuild: install %q: %w", o.Run, err)
	}
	stats, sawDir, err := collect(o.Root, since, o.Exclude, o.Dir, o.Out)
	if err != nil {
		return LayerStats{}, err
	}
	// The install's directory is new or changed in every build, so a layer
	// without it means change times did not work as expected here (a
	// filesystem or clock that differs from the marker's): fail rather than
	// push an image without the install.
	if !sawDir {
		return LayerStats{}, fmt.Errorf("imagebuild: the layer does not contain %s: change times on %s are not usable", o.Dir, o.Root)
	}
	return stats, nil
}

// mark creates the marker and returns its change time.
func mark(path string) (time.Time, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return time.Time{}, fmt.Errorf("imagebuild: marker: %w", err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		return time.Time{}, fmt.Errorf("imagebuild: marker: %w", err)
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return time.Time{}, fmt.Errorf("imagebuild: marker: %w", err)
	}
	st, ok := statOf(fi)
	if !ok {
		return time.Time{}, errors.New("imagebuild: marker: no change time")
	}
	return st.ctime, nil
}

// unpack places the context's regular files under dir.
func unpack(archive, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return fmt.Errorf("imagebuild: context: %w", err)
	}
	defer func() { _ = f.Close() }()
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("imagebuild: context: %w", err)
		}
		if h.Typeflag != tar.TypeReg || !filepath.IsLocal(h.Name) {
			return fmt.Errorf("imagebuild: context: %q is not a regular file inside the directory", h.Name)
		}
		dst := filepath.Join(dir, h.Name)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("imagebuild: context: %w", err)
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("imagebuild: context: %w", err)
		}
		_, err = io.Copy(out, tr)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return fmt.Errorf("imagebuild: context %s: %w", h.Name, err)
		}
	}
}

// fileID identifies a file across its hard links.
type fileID struct{ dev, ino uint64 }

// collect writes to out a tar archive of every entry under root whose change
// time is not before since, parents before children. Entries on another
// filesystem than root, and excluded paths, are not searched. It reports
// whether want was among the entries.
func collect(root string, since time.Time, exclude []string, want, out string) (LayerStats, bool, error) {
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return LayerStats{}, false, fmt.Errorf("imagebuild: %w", err)
	}
	rootStat, ok := statOf(rootInfo)
	if !ok {
		return LayerStats{}, false, fmt.Errorf("imagebuild: %s: no file status", root)
	}
	f, err := os.Create(out)
	if err != nil {
		return LayerStats{}, false, fmt.Errorf("imagebuild: %w", err)
	}
	defer func() { _ = f.Close() }()
	counter := &countingWriter{w: f}
	buf := bufio.NewWriterSize(counter, 1<<20)
	tw := tar.NewWriter(buf)
	links := map[fileID]string{}
	stats := LayerStats{}
	sawWant := false

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Unreadable or vanished entries were not made by the install,
			// which runs as the same user.
			if errors.Is(err, fs.ErrPermission) || errors.Is(err, fs.ErrNotExist) {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			return err
		}
		if path == root {
			return nil
		}
		if excluded(path, exclude) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		fi, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		st, ok := statOf(fi)
		if !ok {
			return fmt.Errorf("%s: no file status", path)
		}
		if st.dev != rootStat.dev {
			if fi.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if st.ctime.Before(since) || fi.Mode()&fs.ModeSocket != 0 {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if path == want {
			sawWant = true
		}
		stats.Entries++
		return writeEntry(tw, path, filepath.ToSlash(rel), fi, st, links)
	})
	if walkErr != nil {
		return LayerStats{}, false, fmt.Errorf("imagebuild: layer: %w", walkErr)
	}
	if err := tw.Close(); err != nil {
		return LayerStats{}, false, fmt.Errorf("imagebuild: layer: %w", err)
	}
	if err := buf.Flush(); err != nil {
		return LayerStats{}, false, fmt.Errorf("imagebuild: layer: %w", err)
	}
	if err := f.Close(); err != nil {
		return LayerStats{}, false, fmt.Errorf("imagebuild: layer: %w", err)
	}
	stats.Bytes = counter.n
	return stats, sawWant, nil
}

func excluded(path string, exclude []string) bool {
	for _, e := range exclude {
		if path == e || strings.HasPrefix(path, strings.TrimSuffix(e, "/")+"/") {
			return true
		}
	}
	return false
}

// writeEntry writes one entry with its numeric owner. A regular file with
// several links is stored once; its other paths become hard links to it.
func writeEntry(tw *tar.Writer, path, name string, fi fs.FileInfo, st fileStat, links map[fileID]string) error {
	target := ""
	if fi.Mode()&fs.ModeSymlink != 0 {
		t, err := os.Readlink(path)
		if err != nil {
			return err
		}
		target = t
	}
	h, err := tar.FileInfoHeader(fi, target)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	h.Name = name
	if fi.IsDir() {
		h.Name += "/"
	}
	h.Uname, h.Gname = "", ""
	h.AccessTime, h.ChangeTime = time.Time{}, time.Time{}
	h.Format = tar.FormatPAX
	if fi.Mode().IsRegular() && st.nlink > 1 {
		id := fileID{st.dev, st.ino}
		if first, ok := links[id]; ok {
			h.Typeflag, h.Linkname, h.Size = tar.TypeLink, first, 0
			return tw.WriteHeader(h)
		}
		links[id] = name
	}
	if err := tw.WriteHeader(h); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if !fi.Mode().IsRegular() {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := io.CopyN(tw, f, h.Size); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
