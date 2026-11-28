package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		tag, cur string
		want     bool
	}{{"v0.1.10", "v0.1.9", true}, {"v0.1.9", "v0.1.10", false}, {"v0.1.5", "v0.1.5", false}, {"v0.2.0", "v0.1.99", true},
		{"v1.0.0", "dev", false}, {"latest", "v0.1.1", false}, {"v0.1.2", "", false}} {
		if Newer(c.tag, c.cur) != c.want {
			t.Errorf("Newer(%q,%q) != %v", c.tag, c.cur, c.want)
		}
	}
}

func archive(t *testing.T, name string, content string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg})
	tw.Write([]byte(content))
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

type fixture struct {
	u       *Updater
	exe     string
	tag     string
	archive []byte
	sums    string
	hits    []string
}

func newFixture(t *testing.T, current, tag string) *fixture {
	t.Helper()
	f := &fixture{tag: tag}
	f.archive = archive(t, "appanel", "NEWBINARY "+tag)
	sum := sha256.Sum256(f.archive)
	f.sums = hex.EncodeToString(sum[:]) + "  appanel-linux-amd64.tar.gz\n"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits = append(f.hits, r.URL.Path)
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			fmt.Fprintf(w, `{"tag_name":%q,"body":"notes","html_url":"https://example/r","published_at":"2026-11-27T00:00:00Z"}`, f.tag)
		case strings.HasSuffix(r.URL.Path, "checksums.txt"):
			w.Write([]byte(f.sums))
		case strings.HasSuffix(r.URL.Path, ".tar.gz"):
			w.Write(f.archive)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(ts.Close)
	f.exe = filepath.Join(t.TempDir(), "appanel")
	os.WriteFile(f.exe, []byte("OLDBINARY"), 0o755)
	f.u = &Updater{Current: current, APIBase: ts.URL, DLBase: ts.URL, HTTP: ts.Client(), Exe: f.exe, Arch: "amd64",
		Probe: func(_ context.Context, p string) (string, error) {
			b, _ := os.ReadFile(p)
			return "appanel " + strings.TrimPrefix(string(b), "NEWBINARY "), nil
		}}
	return f
}

func TestCheck(t *testing.T) {
	f := newFixture(t, "v0.1.3", "v0.1.9")
	info, err := f.u.Check(context.Background(), false)
	if err != nil || !info.Available || info.Latest != "v0.1.9" || !info.CanUpdate || info.OneLiner != OneLiner {
		t.Fatalf("%v %+v", err, info)
	}
	n := len(f.hits)
	f.u.Check(context.Background(), false)
	if len(f.hits) != n {
		t.Error("result not cached")
	}
	f.tag = "not-a-version"
	if _, err := f.u.Check(context.Background(), true); err == nil {
		t.Error("odd tag accepted")
	}
}

func TestApplyReplacesBinary(t *testing.T) {
	f := newFixture(t, "v0.1.3", "v0.1.9")
	v, err := f.u.Apply(context.Background())
	if err != nil || v != "v0.1.9" {
		t.Fatalf("%v %q", err, v)
	}
	if b, _ := os.ReadFile(f.exe); string(b) != "NEWBINARY v0.1.9" {
		t.Errorf("binary = %q", b)
	}
	if b, _ := os.ReadFile(f.exe + ".prev"); string(b) != "OLDBINARY" {
		t.Errorf("no rollback copy: %q", b)
	}
	if fi, _ := os.Stat(f.exe); fi.Mode().Perm() != 0o755 {
		t.Errorf("mode %v", fi.Mode())
	}
	if ents, _ := os.ReadDir(filepath.Dir(f.exe)); len(ents) != 2 {
		t.Errorf("leftover temp files: %v", ents)
	}
}

func TestApplyRefusals(t *testing.T) {
	ctx := context.Background()
	// Tampered download.
	f := newFixture(t, "v0.1.3", "v0.1.9")
	f.archive = archive(t, "appanel", "EVIL")
	if _, err := f.u.Apply(ctx); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Errorf("tampered: %v", err)
	}
	if b, _ := os.ReadFile(f.exe); string(b) != "OLDBINARY" {
		t.Error("binary replaced after a checksum failure")
	}
	// Missing checksum line.
	f = newFixture(t, "v0.1.3", "v0.1.9")
	f.sums = strings.Repeat("a", 64) + "  other.tar.gz\n"
	if _, err := f.u.Apply(ctx); err == nil {
		t.Error("no checksum accepted")
	}
	// Archive without the binary.
	f = newFixture(t, "v0.1.3", "v0.1.9")
	f.archive = archive(t, "something-else", "x")
	sum := sha256.Sum256(f.archive)
	f.sums = hex.EncodeToString(sum[:]) + "  appanel-linux-amd64.tar.gz\n"
	if _, err := f.u.Apply(ctx); err == nil {
		t.Error("archive without binary accepted")
	}
	// Binary that does not start.
	f = newFixture(t, "v0.1.3", "v0.1.9")
	f.u.Probe = func(context.Context, string) (string, error) { return "", fmt.Errorf("exec format error") }
	if _, err := f.u.Apply(ctx); err == nil {
		t.Error("broken binary accepted")
	}
	if b, _ := os.ReadFile(f.exe); string(b) != "OLDBINARY" {
		t.Error("working binary replaced by a broken one")
	}
	// Binary that reports a different version.
	f = newFixture(t, "v0.1.3", "v0.1.9")
	f.u.Probe = func(context.Context, string) (string, error) { return "appanel v0.0.1", nil }
	if _, err := f.u.Apply(ctx); err == nil {
		t.Error("wrong-version binary accepted")
	}
	// Already latest / dev build / downgrade.
	for _, cur := range []string{"v0.1.9", "v0.1.12", "dev"} {
		f = newFixture(t, cur, "v0.1.9")
		if _, err := f.u.Apply(ctx); err == nil {
			t.Errorf("applied over %s", cur)
		}
		for _, h := range f.hits {
			if strings.HasSuffix(h, ".tar.gz") {
				t.Errorf("downloaded the archive although %s needs no update", cur)
			}
		}
	}
}
