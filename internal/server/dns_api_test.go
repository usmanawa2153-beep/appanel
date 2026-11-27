package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/usmanawa2153-beep/appanel/internal/dns"
)

const cfZone = "0123456789abcdef0123456789abcdef"
const cfToken = "tok_abcdefghijklmnopqrstuvwxyz"

func TestDNSAPI(t *testing.T) {
	var seenAuth []string
	var created dns.Record
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = append(seenAuth, r.Header.Get("Authorization"))
		ok := func(v string) {
			w.Write([]byte(`{"success":true,"errors":[],"result":` + v + `,"result_info":{"page":1,"total_pages":1}}`))
		}
		switch {
		case r.URL.Path == "/user/tokens/verify":
			if r.Header.Get("Authorization") != "Bearer "+cfToken {
				w.WriteHeader(403)
				w.Write([]byte(`{"success":false,"errors":[{"code":1,"message":"Invalid API Token"}],"result":null}`))
				return
			}
			ok(`{"status":"active"}`)
		case r.URL.Path == "/zones":
			ok(`[{"id":"` + cfZone + `","name":"example.com","status":"active"}]`)
		case r.Method == "GET":
			ok(`[]`)
		case r.Method == "POST":
			b, _ := io.ReadAll(r.Body)
			json.Unmarshal(b, &created)
			created.ID = "fedcba9876543210fedcba9876543210"
			out, _ := json.Marshal(created)
			ok(string(out))
		}
	}))
	defer cf.Close()
	e := newEnvWith(t, func(s *Server) {
		s.SetDNSClient(&dns.Client{BaseURL: cf.URL, HTTP: cf.Client(), Token: func() string { return s.st.Settings().CloudflareToken }})
	})
	ck := e.login()

	// Before a token is set, record calls are refused (412) without any network call.
	res := e.do("GET", secret+"/api/dns/zones", nil, ck, false)
	res.Body.Close()
	if res.StatusCode != http.StatusPreconditionFailed || len(seenAuth) != 0 {
		t.Fatalf("no token: %d, calls %v", res.StatusCode, seenAuth)
	}
	res = e.do("PUT", secret+"/api/dns/token", map[string]string{"token": "x"}, ck, true)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("short token = %d", res.StatusCode)
	}
	res = e.do("PUT", secret+"/api/dns/token", map[string]string{"token": "wrong_token_wrong_token_wrong"}, ck, true)
	res.Body.Close()
	if res.StatusCode != http.StatusBadGateway || e.srvSettings().CloudflareToken != "" {
		t.Errorf("bad token saved or wrong status %d", res.StatusCode)
	}
	res = e.do("PUT", secret+"/api/dns/token", map[string]string{"token": cfToken}, ck, true)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("save token = %d", res.StatusCode)
	}

	// The token must never come back out.
	for _, p := range []string{"/api/settings", "/api/dns"} {
		res = e.do("GET", secret+p, nil, ck, false)
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if strings.Contains(string(b), cfToken) || strings.Contains(string(b), "cloudflare_token") {
			t.Errorf("%s leaks the token: %s", p, b)
		}
	}
	// Saving the ACME email must not wipe the token.
	res = e.do("PUT", secret+"/api/settings", map[string]string{"acme_email": "a@example.com"}, ck, true)
	res.Body.Close()
	if e.srvSettings().CloudflareToken != cfToken {
		t.Error("settings save erased the Cloudflare token")
	}

	res = e.do("GET", secret+"/api/dns/zones", nil, ck, false)
	var z struct{ Zones []dns.Zone }
	decodeBody(t, res, &z)
	if len(z.Zones) != 1 {
		t.Errorf("%+v", z)
	}
	res = e.do("POST", secret+"/api/dns/zones/"+cfZone+"/point", map[string]any{"name": "www", "ip": "203.0.113.5"}, ck, true)
	res.Body.Close()
	if res.StatusCode != 200 || created.Content != "203.0.113.5" || created.Type != "A" {
		t.Errorf("point: %d %+v", res.StatusCode, created)
	}
	res = e.do("POST", secret+"/api/dns/zones/"+cfZone+"/records", map[string]any{"type": "A", "name": "x", "content": "not-an-ip"}, ck, true)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("bad record = %d", res.StatusCode)
	}
	res = e.do("GET", secret+"/api/dns/zones/..%2f..%2fuser/records", nil, ck, false)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest && res.StatusCode != http.StatusNotFound {
		t.Errorf("bad zone id = %d", res.StatusCode)
	}
	res = e.do("DELETE", secret+"/api/dns/token", nil, ck, true)
	res.Body.Close()
	if e.srvSettings().CloudflareToken != "" {
		t.Error("token not removed")
	}
}
