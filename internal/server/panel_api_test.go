package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usmanawa2153-beep/appanel/internal/config"
	"github.com/usmanawa2153-beep/appanel/internal/store"
)

func TestPanelDomainBlocksIPAccess(t *testing.T) {
	e := newEnvWith(t, func(s *Server) { s.cfg.PanelDomain = "panel.example.com" })
	get := func(host, path string) int {
		req, _ := http.NewRequest("GET", e.ts.URL+path, nil)
		req.Host = host
		res, err := e.cli.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if c := get("panel.example.com", secret+"/api/ping"); c != 200 {
		t.Errorf("via domain = %d", c)
	}
	if c := get("PANEL.example.com:443", secret+"/api/ping"); c != 200 {
		t.Errorf("case/port variant = %d", c)
	}
	for _, host := range []string{"203.0.113.5", "203.0.113.5:23456", "evil.example.net", "panel.example.com.evil.net", ""} {
		for _, p := range []string{secret + "/", secret + "/api/ping", "/"} {
			if c := get(host, p); c != 404 {
				t.Errorf("Host %q path %q = %d, want 404", host, p, c)
			}
		}
	}
}

func TestWithoutPanelDomainAnyHostWorks(t *testing.T) {
	e := newEnv(t)
	req, _ := http.NewRequest("GET", e.ts.URL+secret+"/api/ping", nil)
	req.Host = "203.0.113.5:23456"
	res, _ := e.cli.Do(req)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Errorf("= %d", res.StatusCode)
	}
}

func TestClientIPTrustsNginxOnlyWithPanelDomain(t *testing.T) {
	mk := func(domain, remote, xreal string) string {
		e := newEnvWith(t, nil)
		_ = e
		s := &Server{cfg: &config.Config{PanelDomain: domain}}
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if xreal != "" {
			r.Header.Set("X-Real-IP", xreal)
		}
		r.Header.Set("X-Forwarded-For", "9.9.9.9")
		return s.clientIP(r)
	}
	if got := mk("", "127.0.0.1:5000", "198.51.100.7"); got != "127.0.0.1" {
		t.Errorf("no domain must ignore headers, got %s", got)
	}
	if got := mk("p.example.com", "127.0.0.1:5000", "198.51.100.7"); got != "198.51.100.7" {
		t.Errorf("behind nginx got %s", got)
	}
	if got := mk("p.example.com", "203.0.113.9:5000", "198.51.100.7"); got != "203.0.113.9" {
		t.Errorf("a non-loopback peer must not be believed, got %s", got)
	}
	if got := mk("p.example.com", "127.0.0.1:5000", "not-an-ip"); got != "127.0.0.1" {
		t.Errorf("garbage header, got %s", got)
	}
}

func TestPanelPathAPI(t *testing.T) {
	dir := t.TempDir()
	restarts := 0
	e := newEnvWith(t, func(s *Server) {
		s.dataDir = dir
		s.restart = func() { restarts++ }
		s.cfg.Save(dir)
	})
	ck := e.login()
	post := func(body map[string]any) int {
		res := e.do("POST", secret+"/api/panel/path", body, ck, true)
		res.Body.Close()
		return res.StatusCode
	}
	if c := post(map[string]any{"path": "/kkgwgifuiuriuriuyiug", "password": "wrong"}); c != http.StatusForbidden {
		t.Errorf("wrong password = %d", c)
	}
	for _, bad := range []string{"/", "/a", "/has space/x", "/../etc", "/" + strings.Repeat("a", 65), "/semi;colon1"} {
		if c := post(map[string]any{"path": bad, "password": "hunter2hunter2"}); c != http.StatusBadRequest {
			t.Errorf("%q = %d", bad, c)
		}
	}
	if restarts != 0 {
		t.Error("restarted on a refused change")
	}
	if c := post(map[string]any{"path": "/kkgwgifuiuriuriuyiug", "password": "hunter2hunter2"}); c != 200 || restarts != 1 {
		t.Fatalf("set = %d restarts %d", c, restarts)
	}
	c, err := config.Load(dir)
	if err != nil || c.SecretPath != "/kkgwgifuiuriuriuyiug" {
		t.Errorf("saved config: %v %+v", err, c)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "config.json")); fi.Mode().Perm() != 0o600 {
		t.Errorf("config mode %v", fi.Mode())
	}
	// The running panel keeps its path until the restart.
	res := e.do("GET", secret+"/api/ping", nil, nil, false)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Errorf("live path changed early: %d", res.StatusCode)
	}
	if c := post(map[string]any{"random": true, "password": "hunter2hunter2"}); c != 200 {
		t.Errorf("random = %d", c)
	}
	if n, _ := config.Load(dir); n.SecretPath == "/kkgwgifuiuriuriuyiug" || !config.ValidSecretPath(n.SecretPath) {
		t.Errorf("random path %q", n.SecretPath)
	}
}

func TestPanelDomainAPI(t *testing.T) {
	dir := t.TempDir()
	restarts := 0
	e, ck, fr, _ := newSiteEnvHook(t, func(s *Server) {
		s.dataDir = dir
		s.restart = func() { restarts++ }
		s.cfg.Save(dir)
		s.sites.LookupHost = func(context.Context, string) ([]string, error) { return []string{"203.0.113.5"}, nil }
	})
	e.st.SetSettings(store.Settings{ACMEEmail: "a@example.com"})
	do := func(method string, body map[string]any) (int, map[string]any) {
		res := e.do(method, secret+"/api/panel/domain", body, ck, true)
		var m map[string]any
		decodeBody(t, res, &m)
		return res.StatusCode, m
	}
	if c, _ := do("POST", map[string]any{"domain": "panel.example.com", "password": "nope"}); c != http.StatusForbidden {
		t.Errorf("wrong password = %d", c)
	}
	if c, m := do("POST", map[string]any{"domain": "not a domain", "password": "hunter2hunter2"}); c != http.StatusBadRequest {
		t.Errorf("bad domain = %d %v", c, m)
	}
	if restarts != 0 || cfgDomain(t, dir) != "" {
		t.Fatal("state changed by refused requests")
	}
	c, m := do("POST", map[string]any{"domain": "panel.example.com", "password": "hunter2hunter2"})
	if c != 200 || restarts != 1 || cfgDomain(t, dir) != "panel.example.com" {
		t.Fatalf("set: %d %v restarts=%d", c, m, restarts)
	}
	if u, _ := m["url"].(string); u != "https://panel.example.com"+secret+"/" {
		t.Errorf("url %q", u)
	}
	// Remove it again.
	if c, _ := do("DELETE", map[string]any{"password": "hunter2hunter2"}); c != 200 || cfgDomain(t, dir) != "" || restarts != 2 {
		t.Errorf("unset: %d domain=%q restarts=%d", c, cfgDomain(t, dir), restarts)
	}
	_ = fr
}

func cfgDomain(t *testing.T, dir string) string {
	t.Helper()
	c, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return c.PanelDomain
}
