package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usmanawa2153-beep/appanel/internal/backups"
	"github.com/usmanawa2153-beep/appanel/internal/databases"
	"github.com/usmanawa2153-beep/appanel/internal/store"
)

type bkRunner struct{ calls []string }

func (r *bkRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	if name == "tar" && args[0] == "-czf" {
		_ = os.WriteFile(args[1], []byte("archive-bytes"), 0o644)
	}
	return "", nil
}
func (r *bkRunner) RunInput(ctx context.Context, _ string, n string, a ...string) (string, error) {
	return r.Run(ctx, n, a...)
}

func TestBackupAPI(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "site")
	os.MkdirAll(root, 0o755)
	rn := &bkRunner{}
	var siteID int64
	e := newEnvWith(t, func(s *Server) {
		site, err := s.st.CreateSite(store.Site{Domain: "example.com", Type: store.SiteStatic, Root: root, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		siteID = site.ID
		db := &databases.Manager{Runner: rn, LookPath: func(string) (string, error) { return "/x", nil }}
		m := backups.NewManager(s.st, db)
		m.Runner = rn
		m.Dir = filepath.Join(dir, "bk")
		s.SetBackupManager(m)
	})
	ck := e.login()

	res := e.do("GET", secret+"/api/backups", nil, nil, false)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated = %d", res.StatusCode)
	}
	res = e.do("POST", secret+"/api/backups/site", map[string]int64{"site_id": siteID}, ck, false)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("no CSRF = %d", res.StatusCode)
	}
	res = e.do("POST", secret+"/api/backups/site", map[string]int64{"site_id": siteID}, ck, true)
	var it backups.Item
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d", res.StatusCode)
	}
	decodeBody(t, res, &it)
	if it.Kind != "site" || it.Target != "example.com" {
		t.Errorf("%+v", it)
	}

	res = e.do("GET", secret+"/api/backups/"+it.Name+"/download", nil, ck, false)
	var buf strings.Builder
	b := make([]byte, 64)
	n, _ := res.Body.Read(b)
	buf.Write(b[:n])
	res.Body.Close()
	if res.StatusCode != 200 || buf.String() != "archive-bytes" || !strings.Contains(res.Header.Get("Content-Disposition"), it.Name) {
		t.Errorf("download: %d %q %q", res.StatusCode, buf.String(), res.Header.Get("Content-Disposition"))
	}

	for _, bad := range []string{"..%2f..%2fetc%2fpasswd", "x", "site-a.com-20261127-100000.tar.gz"} {
		res = e.do("GET", secret+"/api/backups/"+bad+"/download", nil, ck, false)
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("download %q = %d, want 404", bad, res.StatusCode)
		}
	}

	res = e.do("POST", secret+"/api/backups/"+it.Name+"/restore", nil, ck, true)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(strings.Join(rn.calls, "|"), "tar -xzf") {
		t.Errorf("restore = %d %v", res.StatusCode, rn.calls)
	}
	res = e.do("DELETE", secret+"/api/backups/"+it.Name, nil, ck, true)
	res.Body.Close()
	res = e.do("GET", secret+"/api/backups", nil, ck, false)
	var l struct {
		Backups []backups.Item `json:"backups"`
		Cmd     string         `json:"schedule_command"`
	}
	decodeBody(t, res, &l)
	if len(l.Backups) != 0 || !strings.Contains(l.Cmd, "backup-all --keep 7") {
		t.Errorf("%+v", l)
	}
}
