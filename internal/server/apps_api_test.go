package server

import (
	"context"
	"net/http"
	"testing"

	"github.com/usmanawa2153-beep/appanel/internal/apps"
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
