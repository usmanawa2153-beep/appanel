package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/usmanawa2153-beep/appanel/internal/apps"
	"github.com/usmanawa2153-beep/appanel/internal/store"
)

func TestWordPressAPIGuards(t *testing.T) {
	fetched := 0
	var srv *Server
	e := newEnvWith(t, func(s *Server) {
		srv = s
		s.SetWordPress(&apps.WordPress{Runner: &opsRunner{}, Fetch: func(context.Context, string, int64) ([]byte, error) { fetched++; return nil, nil }})
	})
	ck := e.login()
	body := map[string]any{"domain": "blog.example.com", "title": "T", "admin_user": "siteadmin", "admin_email": "a@example.com"}

	res := e.do("POST", secret+"/api/apps/wordpress", body, nil, true)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated = %d", res.StatusCode)
	}
	res = e.do("POST", secret+"/api/apps/wordpress", body, ck, false)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("no CSRF = %d", res.StatusCode)
	}
	bad := map[string]any{"domain": "blog.example.com", "title": "", "admin_user": "x", "admin_email": "nope"}
	res = e.do("POST", secret+"/api/apps/wordpress", bad, ck, true)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || fetched != 0 {
		t.Errorf("invalid form = %d, downloads %d", res.StatusCode, fetched)
	}
	srv.appBusy.Lock()
	res = e.do("POST", secret+"/api/apps/wordpress", body, ck, true)
	res.Body.Close()
	srv.appBusy.Unlock()
	if res.StatusCode != http.StatusConflict {
		t.Errorf("concurrent install = %d, want 409", res.StatusCode)
	}
}

func TestWordPressManageAPI(t *testing.T) {
	rn := &opsRunner{}
	var srv *Server
	var wpID, plainID int64
	e := newEnvWith(t, func(s *Server) {
		srv = s
		s.SetWordPress(&apps.WordPress{Store: s.st, Runner: rn, CLIPath: "/usr/local/bin/wp"})
		a, _ := s.st.CreateSite(store.Site{Domain: "blog.example.com", Type: store.SitePHP, Root: "/var/www/b", App: "wordpress", Enabled: true})
		b, _ := s.st.CreateSite(store.Site{Domain: "plain.example.com", Type: store.SitePHP, Root: "/var/www/p", Enabled: true})
		wpID, plainID = a.ID, b.ID
	})
	ck := e.login()
	url := func(id int64, rest string) string { return secret + "/api/wp/" + i64(id) + rest }

	res := e.do("GET", url(wpID, "/plugins"), nil, nil, false)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated = %d", res.StatusCode)
	}
	res = e.do("POST", url(wpID, "/plugins/install"), map[string]any{"slug": "akismet"}, ck, false)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("no CSRF = %d", res.StatusCode)
	}
	res = e.do("GET", url(plainID, "/plugins"), nil, ck, false)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("non-WordPress site = %d, want 404", res.StatusCode)
	}
	n := len(rn.calls)
	for _, slug := range []string{"--url=evil", "a b", "../x"} {
		res = e.do("POST", url(wpID, "/plugins/install"), map[string]any{"slug": slug}, ck, true)
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("slug %q = %d", slug, res.StatusCode)
		}
	}
	res = e.do("POST", url(wpID, "/plugins/akismet/eval"), nil, ck, true)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || len(rn.calls) != n {
		t.Errorf("bad action = %d, commands %v", res.StatusCode, rn.calls[n:])
	}
	res = e.do("POST", url(wpID, "/plugins/install"), map[string]any{"slug": "wordpress-seo", "activate": true}, ck, true)
	res.Body.Close()
	last := rn.calls[len(rn.calls)-1]
	if res.StatusCode != 200 || !strings.HasSuffix(last, "plugin install wordpress-seo --activate") {
		t.Errorf("install = %d %s", res.StatusCode, last)
	}
	res = e.do("POST", url(wpID, "/themes/twentytwentyfour/activate"), nil, ck, true)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.HasSuffix(rn.calls[len(rn.calls)-1], "theme activate twentytwentyfour") {
		t.Errorf("theme activate = %d", res.StatusCode)
	}
	srv.wpBusy.Lock()
	res = e.do("POST", url(wpID, "/plugins/update-all"), nil, ck, true)
	res.Body.Close()
	srv.wpBusy.Unlock()
	if res.StatusCode != http.StatusConflict {
		t.Errorf("concurrent change = %d, want 409", res.StatusCode)
	}
}
