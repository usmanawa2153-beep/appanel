package server

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

	"github.com/usmanawa2153-beep/appanel/internal/updater"
)

func TestUpdateAPI(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "appanel", Mode: 0o755, Size: 6, Typeflag: tar.TypeReg})
	tw.Write([]byte("v0.1.9\n"[:6]))
	tw.Close()
	gz.Close()
	sum := sha256.Sum256(buf.Bytes())
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			fmt.Fprint(w, `{"tag_name":"v0.1.9","body":"n","html_url":"u","published_at":"p"}`)
		case strings.HasSuffix(r.URL.Path, "checksums.txt"):
			fmt.Fprintf(w, "%s  appanel-linux-amd64.tar.gz\n", hex.EncodeToString(sum[:]))
		default:
			w.Write(buf.Bytes())
		}
	}))
	defer gh.Close()
	exe := filepath.Join(t.TempDir(), "appanel")
	os.WriteFile(exe, []byte("old"), 0o755)
	restarts := 0
	e := newEnvWith(t, func(s *Server) {
		s.restart = func() { restarts++ }
		s.SetUpdater(&updater.Updater{Current: "v0.1.3", APIBase: gh.URL, DLBase: gh.URL, HTTP: gh.Client(), Exe: exe, Arch: "amd64",
			Probe: func(_ context.Context, p string) (string, error) { b, _ := os.ReadFile(p); return string(b), nil }})
	})
	ck := e.login()

	res := e.do("GET", secret+"/api/update", nil, nil, false)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated = %d", res.StatusCode)
	}
	res = e.do("GET", secret+"/api/update", nil, ck, false)
	var info updater.Info
	decodeBody(t, res, &info)
	if !info.Available || info.Latest != "v0.1.9" || !strings.Contains(info.OneLiner, "install.sh") {
		t.Errorf("%+v", info)
	}
	res = e.do("POST", secret+"/api/update/apply", map[string]string{"password": "wrong"}, ck, true)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden || restarts != 0 {
		t.Errorf("wrong password = %d restarts %d", res.StatusCode, restarts)
	}
	if b, _ := os.ReadFile(exe); string(b) != "old" {
		t.Fatal("binary changed without a valid password")
	}
	res = e.do("POST", secret+"/api/update/apply", map[string]string{"password": "hunter2hunter2"}, ck, true)
	res.Body.Close()
	if b, _ := os.ReadFile(exe); res.StatusCode != 200 || string(b) != "v0.1.9" || restarts != 1 {
		t.Errorf("apply = %d binary %q restarts %d", res.StatusCode, b, restarts)
	}
}
