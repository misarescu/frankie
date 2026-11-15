package imagebuild

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// layerEntry is one tar entry as a test sees it.
type layerEntry struct {
	typ      byte
	linkname string
	body     string
}

func readLayer(t *testing.T, path string) ([]string, map[string]layerEntry) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var order []string
	entries := map[string]layerEntry{}
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return order, entries
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		order = append(order, h.Name)
		entries[h.Name] = layerEntry{typ: h.Typeflag, linkname: h.Linkname, body: string(b)}
	}
}

func contextArchive(t *testing.T, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "context.tar")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeRoot is a container filesystem with some files from the image.
func fakeRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"home/agent/.local/bin", "usr/bin", "tmp", "mnt/out"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"home/agent/.local/bin/claude", "usr/bin/tool"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("from the image"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Change times can be as coarse as a clock tick: keep the image's files
	// clearly older than the build's start.
	time.Sleep(50 * time.Millisecond)
	return root
}

// M5.2 (D81): the layer holds what the install created or changed and the
// context it ran with, and nothing else of the image.
func TestLayerHoldsWhatTheInstallChanged(t *testing.T) {
	root := fakeRoot(t)
	out := filepath.Join(root, "mnt/out/layer.tar")
	o := LayerOptions{
		Context: contextArchive(t, map[string]string{"mise.toml": "[tools]\n", "deps/go.sum": "sum\n"}),
		Dir:     filepath.Join(root, "home/agent/toolchain"),
		Run: `set -e
mkdir -p ../.local/share/mise/installs/go
echo go > ../.local/share/mise/installs/go/bin
ln -s installs/go/bin ../.local/share/mise/shim
ln ../.local/share/mise/installs/go/bin ../.local/share/mise/zz-hardlink
echo scratch > ` + filepath.Join(root, "tmp/scratch") + `
echo changed > ` + filepath.Join(root, "usr/bin/tool") + `
echo hello`,
		Out:     out,
		Root:    root,
		Marker:  filepath.Join(root, "tmp/marker"),
		Exclude: []string{filepath.Join(root, "tmp"), filepath.Join(root, "mnt/out")},
	}
	var stdout bytes.Buffer
	o.Stdout = &stdout
	stats, err := Layer(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "hello") {
		t.Errorf("install output = %q", stdout.String())
	}
	order, entries := readLayer(t, out)
	if stats.Entries != len(order) {
		t.Errorf("stats.Entries = %d, the archive has %d", stats.Entries, len(order))
	}
	want := map[string]layerEntry{
		"home/agent/toolchain/mise.toml":               {typ: tar.TypeReg, body: "[tools]\n"},
		"home/agent/toolchain/deps/go.sum":             {typ: tar.TypeReg, body: "sum\n"},
		"home/agent/.local/share/mise/installs/go/bin": {typ: tar.TypeReg, body: "go\n"},
		"home/agent/.local/share/mise/shim":            {typ: tar.TypeSymlink, linkname: "installs/go/bin"},
		"usr/bin/tool":                                 {typ: tar.TypeReg, body: "changed\n"},
		"home/agent/.local/share/mise/installs/go/":    {typ: tar.TypeDir},
		"home/agent/toolchain/":                        {typ: tar.TypeDir},
		"home/agent/.local/share/mise/zz-hardlink":     {typ: tar.TypeLink, linkname: "home/agent/.local/share/mise/installs/go/bin"},
	}
	for name, w := range want {
		got, ok := entries[name]
		if !ok {
			t.Errorf("layer has no %s; it has %v", name, order)
			continue
		}
		if got != w {
			t.Errorf("%s = %+v, want %+v", name, got, w)
		}
	}
	for _, name := range []string{"home/agent/.local/bin/claude", "tmp/scratch", "tmp/marker", "mnt/out/layer.tar"} {
		if _, ok := entries[name]; ok {
			t.Errorf("layer has %s, which the install did not change or is excluded", name)
		}
	}
	// Parents come before their children, so the layer unpacks in order.
	for i, name := range order {
		parent := filepath.Dir(strings.TrimSuffix(name, "/")) + "/"
		if j := slices.Index(order, parent); j > i {
			t.Errorf("%s comes before its parent %s", name, parent)
		}
	}
}

func TestLayerFailsWhenTheInstallFails(t *testing.T) {
	root := fakeRoot(t)
	var stderr bytes.Buffer
	_, err := Layer(t.Context(), LayerOptions{
		Dir: filepath.Join(root, "home/agent/toolchain"), Run: "echo no such tool >&2; exit 3",
		Out: filepath.Join(root, "mnt/out/layer.tar"), Root: root, Marker: filepath.Join(root, "tmp/marker"),
		Exclude: []string{filepath.Join(root, "tmp"), filepath.Join(root, "mnt/out")}, Stderr: &stderr,
	})
	if err == nil || !strings.Contains(err.Error(), "exit status 3") {
		t.Errorf("err = %v, want the install's exit status", err)
	}
	if !strings.Contains(stderr.String(), "no such tool") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestLayerRejectsAContextOutsideItsDirectory(t *testing.T) {
	root := fakeRoot(t)
	_, err := Layer(t.Context(), LayerOptions{
		Context: contextArchive(t, map[string]string{"../../escape": "x"}),
		Dir:     filepath.Join(root, "home/agent/toolchain"), Run: "true",
		Out: filepath.Join(root, "mnt/out/layer.tar"), Root: root, Marker: filepath.Join(root, "tmp/marker"),
	})
	if err == nil || !strings.Contains(err.Error(), "escape") {
		t.Errorf("err = %v, want the context rejected", err)
	}
	if _, err := os.Stat(filepath.Join(root, "home/escape")); err == nil {
		t.Error("the context wrote outside its directory")
	}
}

// A layer without the install's directory means change times cannot be
// trusted on this filesystem: the build fails instead of pushing an image
// without the install.
func TestLayerFailsWhenItMissesTheInstall(t *testing.T) {
	root := fakeRoot(t)
	elsewhere := t.TempDir()
	_, err := Layer(t.Context(), LayerOptions{
		Dir: filepath.Join(elsewhere, "toolchain"), Run: "true",
		Out: filepath.Join(root, "mnt/out/layer.tar"), Root: root, Marker: filepath.Join(root, "tmp/marker"),
		Exclude: []string{filepath.Join(root, "tmp"), filepath.Join(root, "mnt/out")},
	})
	if err == nil || !strings.Contains(err.Error(), "does not contain") {
		t.Errorf("err = %v, want the missing install reported", err)
	}
}
