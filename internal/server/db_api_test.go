package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/usmanawa2153-beep/appanel/internal/databases"
)

func TestDatabaseAPI(t *testing.T) {
	rn := &opsRunner{}
	e := newEnvWith(t, func(s *Server) {
		m := databases.NewManager()
		m.Runner = rn
		m.LookPath = func(string) (string, error) { return "/usr/bin/x", nil }
		s.SetDatabaseManager(m)
	})
	ck := e.login()

	res := e.do("GET", secret+"/api/databases", nil, nil, false)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated = %d", res.StatusCode)
	}
	res = e.do("POST", secret+"/api/databases/mysql", map[string]string{"name": "shop"}, ck, false)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("no CSRF = %d", res.StatusCode)
	}
	res = e.do("POST", secret+"/api/databases/oracle", map[string]string{"name": "shop"}, ck, true)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown engine = %d", res.StatusCode)
	}
	res = e.do("POST", secret+"/api/databases/mysql", map[string]string{"name": "x'; DROP"}, ck, true)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("bad name = %d", res.StatusCode)
	}
	res = e.do("POST", secret+"/api/databases/mysql", map[string]string{"name": "shop"}, ck, true)
	var c databases.Created
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d", res.StatusCode)
	}
	decodeBody(t, res, &c)
	if len(c.Password) != 24 || c.User != "shop" {
		t.Errorf("%+v", c)
	}
	res = e.do("GET", secret+"/api/databases", nil, ck, false)
	var l struct {
		Engines []databases.EngineStatus `json:"engines"`
	}
	decodeBody(t, res, &l)
	if len(l.Engines) != 2 || !l.Engines[0].Installed {
		t.Errorf("%+v", l)
	}
	// The generated password must not appear in the server log of commands.
	for _, c2 := range rn.calls {
		if strings.Contains(c2, c.Password) {
			t.Errorf("password in argv: %s", c2)
		}
	}
}
