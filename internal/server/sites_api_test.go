package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usmanawa2153-beep/appanel/internal/sites"
)

type okRunner struct{ calls []string }

func (r *okRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	return "", nil
}

// newSiteEnv returns a test env whose website manager writes into a temp dir
// and records the commands it would have run.
func newSiteEnv(t *testing.T) (*env, *http.Cookie, *okRunner, string) {
	t.Helper()
	dir := t.TempDir()
	fr := &okRunner{}
	e := newEnvWith(t, func(s *Server) {
		m := sites.NewManager(s.st)
		m.Runner = fr
		m.NginxDir = filepath.Join(dir, "nginx")
		m.WebRoot = filepath.Join(dir, "www")
		m.ACMERoot = filepath.Join(dir, "www", "_acme")
		m.LetsEncryptDir = filepath.Join(dir, "le")
		m.LogDir = filepath.Join(dir, "log")
		m.PHPSocketGlob = filepath.Join(dir, "none-*.sock")
		m.LookPath = func(string) (string, error) { return "/usr/bin/fake", nil }
		s.SetSiteManager(m)
	})
	return e, e.login(), fr, dir
}

func TestSitesRequireAuth(t *testing.T) {
	e := newEnv(t)
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/sites"}, {"GET", "/api/sites/status"}, {"GET", "/api/sites/1"}, {"GET", "/api/settings"},
	} {
		res := e.do(c.method, secret+c.path, nil, nil, false)
		if res.StatusCode != 401 {
			t.Errorf("%s %s = %d, want 401", c.method, c.path, res.StatusCode)
		}
	}
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/sites"}, {"POST", "/api/sites/setup"}, {"DELETE", "/api/sites/1"}, {"POST", "/api/sites/1/ssl"}, {"PUT", "/api/settings"},
	} {
		c0 := e.login()
		res := e.do(c.method, secret+c.path, map[string]string{}, c0, false) // missing CSRF header
		if res.StatusCode != 403 {
			t.Errorf("%s %s without CSRF = %d, want 403", c.method, c.path, res.StatusCode)
		}
	}
}

func TestSitesAPIFlow(t *testing.T) {
	e, c, fr, dir := newSiteEnv(t)

	res := e.do("GET", secret+"/api/sites/status", nil, c, false)
	var st sites.Status
	json.NewDecoder(res.Body).Decode(&st)
	if res.StatusCode != 200 || !st.NginxInstalled || !st.CertbotInstalled {
		t.Fatalf("status %d %+v", res.StatusCode, st)
	}

	// create
	res = e.do("POST", secret+"/api/sites", map[string]any{"domain": "Demo.Example.com", "aliases": []string{"www.demo.example.com"}, "type": "static"}, c, true)
	var created sites.SiteView
	json.NewDecoder(res.Body).Decode(&created)
	if res.StatusCode != 201 || created.Domain != "demo.example.com" || created.OwnerID == 0 {
		t.Fatalf("create = %d %+v", res.StatusCode, created)
	}
	if !strings.HasPrefix(created.Root, filepath.Join(dir, "www")) {
		t.Fatalf("root outside webroot: %s", created.Root)
	}
	if !contains(fr.calls, "nginx -t") {
		t.Fatalf("nginx not validated: %v", fr.calls)
	}

	// duplicate
	res = e.do("POST", secret+"/api/sites", map[string]any{"domain": "demo.example.com", "type": "static"}, c, true)
	if res.StatusCode != 409 {
		t.Fatalf("duplicate = %d", res.StatusCode)
	}

	// hostile input is rejected before it can reach any config
	for _, body := range []map[string]any{
		{"domain": "a.com; } server { listen 1; ", "type": "static"},
		{"domain": "ok.example.com", "type": "proxy", "proxy_url": "http://127.0.0.1:1;\nreturn 200"},
		{"domain": "ok.example.com", "type": "static", "root": "/etc"},
		{"domain": "ok.example.com", "type": "../../x"},
	} {
		if res := e.do("POST", secret+"/api/sites", body, c, true); res.StatusCode != 400 {
			t.Errorf("hostile %v = %d, want 400", body, res.StatusCode)
		}
	}
	if res := e.do("POST", secret+"/api/sites", "not an object", c, true); res.StatusCode != 400 {
		t.Errorf("bad body = %d", res.StatusCode)
	}

	// list + get
	res = e.do("GET", secret+"/api/sites", nil, c, false)
	var list struct{ Sites []sites.SiteView }
	json.NewDecoder(res.Body).Decode(&list)
	if len(list.Sites) != 1 {
		t.Fatalf("list = %+v", list)
	}
	id := itoa(int(list.Sites[0].ID))
	if res := e.do("GET", secret+"/api/sites/"+id, nil, c, false); res.StatusCode != 200 {
		t.Fatalf("get = %d", res.StatusCode)
	}
	if res := e.do("GET", secret+"/api/sites/999", nil, c, false); res.StatusCode != 404 {
		t.Fatalf("missing = %d", res.StatusCode)
	}
	if res := e.do("GET", secret+"/api/sites/abc", nil, c, false); res.StatusCode != 404 {
		t.Fatalf("non-numeric id = %d", res.StatusCode)
	}

	// SSL needs an email first
	res = e.do("POST", secret+"/api/sites/"+id+"/ssl", map[string]any{}, c, true)
	if res.StatusCode != 412 {
		t.Fatalf("ssl without email = %d, want 412", res.StatusCode)
	}
	if res := e.do("PUT", secret+"/api/settings", map[string]string{"acme_email": "not-an-email"}, c, true); res.StatusCode != 400 {
		t.Fatalf("bad email accepted: %d", res.StatusCode)
	}
	if res := e.do("PUT", secret+"/api/settings", map[string]string{"acme_email": "me@example.com"}, c, true); res.StatusCode != 200 {
		t.Fatalf("settings = %d", res.StatusCode)
	}
	res = e.do("POST", secret+"/api/sites/"+id+"/ssl", map[string]any{}, c, true)
	if res.StatusCode != 200 {
		t.Fatalf("ssl = %d", res.StatusCode)
	}
	if !contains(fr.calls, "certbot certonly") {
		t.Fatalf("certbot not run: %v", fr.calls)
	}

	// force https, disable, delete
	if res := e.do("PUT", secret+"/api/sites/"+id, map[string]any{"force_https": true}, c, true); res.StatusCode != 200 {
		t.Fatalf("force https = %d", res.StatusCode)
	}
	if res := e.do("PUT", secret+"/api/sites/"+id, map[string]any{"enabled": false}, c, true); res.StatusCode != 200 {
		t.Fatalf("disable = %d", res.StatusCode)
	}
	// regression: a site without aliases must serialise "aliases":[] (null crashed the UI)
	res = e.do("POST", secret+"/api/sites", map[string]any{"domain": "noalias.example.com", "type": "proxy", "proxy_url": "http://127.0.0.1:3000"}, c, true)
	if res.StatusCode != 201 {
		t.Fatalf("create noalias = %d", res.StatusCode)
	}
	var na sites.SiteView
	json.NewDecoder(res.Body).Decode(&na)
	off := e.do("PUT", secret+"/api/sites/"+itoa(int(na.ID)), map[string]any{"enabled": false}, c, true)
	raw := new(strings.Builder)
	buf := make([]byte, 4096)
	for {
		n, err := off.Body.Read(buf)
		raw.Write(buf[:n])
		if err != nil {
			break
		}
	}
	if !strings.Contains(raw.String(), `"aliases":[]`) || strings.Contains(raw.String(), `"aliases":null`) {
		t.Fatalf("aliases must be [] after update, got %s", raw.String())
	}
	lst := e.do("GET", secret+"/api/sites", nil, c, false)
	rawList := new(strings.Builder)
	for {
		n, err := lst.Body.Read(buf)
		rawList.Write(buf[:n])
		if err != nil {
			break
		}
	}
	if strings.Contains(rawList.String(), `"aliases":null`) {
		t.Fatalf("list contains null aliases: %s", rawList.String())
	}
	if res := e.do("DELETE", secret+"/api/sites/"+id, nil, c, true); res.StatusCode != 200 {
		t.Fatalf("delete = %d", res.StatusCode)
	}
	if res := e.do("GET", secret+"/api/sites/"+id, nil, c, false); res.StatusCode != 404 {
		t.Fatalf("after delete = %d", res.StatusCode)
	}
}

func contains(calls []string, prefix string) bool {
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func TestWildcardSSLAPI(t *testing.T) {
	e, ck, fr, _ := newSiteEnv(t)
	res := e.do("POST", secret+"/api/sites", map[string]any{"domain": "example.com", "type": "static"}, ck, true)
	var s struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, res, &s)
	id := i64(s.ID)

	res = e.do("POST", secret+"/api/sites/"+id+"/ssl/wildcard", nil, ck, false)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("no CSRF = %d", res.StatusCode)
	}
	res = e.do("POST", secret+"/api/sites/"+id+"/ssl/wildcard", nil, ck, true)
	res.Body.Close()
	if res.StatusCode != http.StatusPreconditionFailed { // no ACME email yet
		t.Errorf("no email = %d", res.StatusCode)
	}
	for _, c := range fr.calls {
		if strings.HasPrefix(c, "certbot certonly") {
			t.Errorf("certbot ran without prerequisites: %s", c)
		}
	}
	res = e.do("POST", secret+"/api/sites/"+id+"/ssl/use-wildcard", nil, ck, true)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("use-wildcard without cert = %d", res.StatusCode)
	}
}
