package telemetry

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func gunzipFile(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip reader %s: %v", path, err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip %s: %v", path, err)
	}
	if err := zr.Close(); err != nil {
		t.Fatalf("close gzip %s: %v", path, err)
	}
	return b
}

func mustNewWriter(t *testing.T, path string, maxBytes int64, keep int) *rotatingWriter {
	t.Helper()
	w, err := newRotatingWriter(path, maxBytes, keep)
	if err != nil {
		t.Fatalf("new rotating writer: %v", err)
	}
	return w
}

func TestRotatingWriterNoRotationBelowLimit(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.log")
	w := mustNewWriter(t, p, 100, 2)
	first := strings.Repeat("a", 40)
	second := strings.Repeat("b", 40)
	if _, err := w.Write([]byte(first)); err != nil {
		t.Fatalf("write first: %v", err)
	}
	if _, err := w.Write([]byte(second)); err != nil {
		t.Fatalf("write second: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := string(readFile(t, p)); got != first+second {
		t.Fatalf("content = %q, want %q", got, first+second)
	}
	if _, err := os.Stat(p + ".1.gz"); !os.IsNotExist(err) {
		t.Fatalf("archive must not exist below limit, stat err = %v", err)
	}
}

func TestRotatingWriterRotatesAndCompresses(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.log")
	w := mustNewWriter(t, p, 100, 2)
	first := strings.Repeat("a", 60)
	second := strings.Repeat("b", 60)
	if _, err := w.Write([]byte(first)); err != nil {
		t.Fatalf("write first: %v", err)
	}
	if _, err := w.Write([]byte(second)); err != nil {
		t.Fatalf("write second: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := string(readFile(t, p)); got != second {
		t.Fatalf("live content = %q, want only second write", got)
	}
	if got := string(gunzipFile(t, p+".1.gz")); got != first {
		t.Fatalf("archive content = %q, want first write", got)
	}
}

func TestRotatingWriterKeepsOnlyConfiguredArchives(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.log")
	w := mustNewWriter(t, p, 10, 2)
	for i := 1; i <= 5; i++ {
		if _, err := w.Write([]byte(fmt.Sprintf("%08d", i))); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := string(readFile(t, p)); got != "00000005" {
		t.Fatalf("live content = %q, want 00000005", got)
	}
	if got := string(gunzipFile(t, p+".1.gz")); got != "00000004" {
		t.Fatalf(".1.gz content = %q, want 00000004", got)
	}
	if got := string(gunzipFile(t, p+".2.gz")); got != "00000003" {
		t.Fatalf(".2.gz content = %q, want 00000003", got)
	}
	if _, err := os.Stat(p + ".3.gz"); !os.IsNotExist(err) {
		t.Fatalf("archive beyond keep must be deleted, stat err = %v", err)
	}
}

func TestRotatingWriterRotatesOversizedExistingFileOnOpen(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.log")
	old := []byte(strings.Repeat("x", 150))
	if err := os.WriteFile(p, old, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	w := mustNewWriter(t, p, 100, 2)
	if got := readFile(t, p); len(got) != 0 {
		t.Fatalf("live file must be empty after open rotation, got %d bytes", len(got))
	}
	if got := gunzipFile(t, p+".1.gz"); !bytes.Equal(got, old) {
		t.Fatalf("archive content mismatch: %d bytes", len(got))
	}
	if _, err := w.Write([]byte("y")); err != nil {
		t.Fatalf("write after open rotation: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := string(readFile(t, p)); got != "y" {
		t.Fatalf("live content = %q, want y", got)
	}
}

func TestRotatingWriterConcurrentWritesKeepAllLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.log")
	const writers, perWriter = 8, 50
	w := mustNewWriter(t, p, 64, writers*perWriter)
	var wg sync.WaitGroup
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				if _, err := w.Write([]byte(fmt.Sprintf("w%02d-%03d\n", g, i))); err != nil {
					t.Errorf("write: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	var all []byte
	all = append(all, readFile(t, p)...)
	for i := 1; ; i++ {
		path := fmt.Sprintf("%s.%d.gz", p, i)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
		all = append(all, gunzipFile(t, path)...)
	}
	got := map[string]int{}
	for _, line := range strings.Split(strings.TrimSuffix(string(all), "\n"), "\n") {
		got[line]++
	}
	for g := 0; g < writers; g++ {
		for i := 0; i < perWriter; i++ {
			line := fmt.Sprintf("w%02d-%03d", g, i)
			if got[line] != 1 {
				t.Fatalf("line %q appears %d times, want exactly 1", line, got[line])
			}
		}
	}
}

func TestNewLoggerRotatesAtDefaultLimit(t *testing.T) {
	p := filepath.Join(t.TempDir(), "logs", "app.log")
	lg, closer, err := NewLogger(p)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	huge := strings.Repeat("z", int(defaultMaxBytes)+1024)
	lg.Info("huge", "pad", huge)
	lg.Info("trigger-rotation")
	if err := closer.Close(); err != nil {
		t.Fatalf("close logger: %v", err)
	}
	archived := gunzipFile(t, p+".1.gz")
	if !bytes.Contains(archived, []byte(`"msg":"huge"`)) {
		t.Fatalf("archive must contain the first record (got %d bytes)", len(archived))
	}
	if !bytes.Contains(readFile(t, p), []byte(`"msg":"trigger-rotation"`)) {
		t.Fatal("live log must contain the post-rotation record")
	}
}
