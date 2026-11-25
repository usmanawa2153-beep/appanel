package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/usmanawa2153-beep/appanel/internal/auth"
	"github.com/usmanawa2153-beep/appanel/internal/config"
	"github.com/usmanawa2153-beep/appanel/internal/store"
)

const secret = "/s3cretpath1234"

type env struct {
	t    *testing.T
	ts   *httptest.Server
	root string
	cli  *http.Client
}

func newEnv(t *testing.T) *env { return newEnvWith(t, nil) }

func newEnvWith(t *testing.T, hook func(*Server)) *env {
	t.Helper()
	dir, root := t.TempDir(), t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword("hunter2hunter2")
	if _, err := st.CreateUser(store.User{Username: "admin", Role: "admin", PasswordHash: hash}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Listen: "127.0.0.1", Port: 1, SecretPath: secret, FileRoot: root, SessionHours: 1}
	ui := fstest.MapFS{"index.html": {Data: []byte("<title>ui</title>")}}
	srv, err := New(cfg, st, ui, "test")
	if err != nil {
		t.Fatal(err)
	}
	if hook != nil {
		hook(srv)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &env{t: t, ts: ts, root: root, cli: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (e *env) do(method, path string, body any, cookie *http.Cookie, csrf bool) *http.Response {
	e.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, e.ts.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf {
		req.Header.Set(csrfHeader, "1")
	}
	res, err := e.cli.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func (e *env) login() *http.Cookie {
	e.t.Helper()
	res := e.do("POST", secret+"/api/login", map[string]string{"username": "admin", "password": "hunter2hunter2"}, nil, false)
	defer res.Body.Close()
	if res.StatusCode != 200 {
		e.t.Fatalf("login status %d", res.StatusCode)
	}
	for _, c := range res.Cookies() {
		if c.Name == cookieName {
			return c
		}
	}
	e.t.Fatal("no session cookie")
	return nil
}

func TestSecretPathHidesPanel(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/", "/api/login", "/index.html", "/wrongsecret/", secret + "x/"} {
		res := e.do("GET", p, nil, nil, false)
		if res.StatusCode != 404 {
			t.Errorf("GET %s = %d, want 404", p, res.StatusCode)
		}
		res.Body.Close()
	}
	res := e.do("GET", secret, nil, nil, false)
	if res.StatusCode != 301 || res.Header.Get("Location") != secret+"/" {
		t.Errorf("bare secret should redirect, got %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	res = e.do("GET", secret+"/", nil, nil, false)
	if res.StatusCode != 200 {
		t.Errorf("UI = %d", res.StatusCode)
	}
	if res.Header.Get("Content-Security-Policy") == "" || res.Header.Get("X-Frame-Options") != "DENY" {
		t.Error("security headers missing")
	}
}

func TestAuthRequired(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/api/me", "/api/system", "/api/files?path=/"} {
		res := e.do("GET", secret+p, nil, nil, false)
		if res.StatusCode != 401 {
			t.Errorf("%s without session = %d, want 401", p, res.StatusCode)
		}
		res.Body.Close()
	}
}

func TestLoginAndSession(t *testing.T) {
	e := newEnv(t)
	bad := e.do("POST", secret+"/api/login", map[string]string{"username": "admin", "password": "nope"}, nil, false)
	if bad.StatusCode != 401 {
		t.Fatalf("bad login = %d", bad.StatusCode)
	}
	c := e.login()
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != secret+"/" {
		t.Errorf("cookie flags wrong: %+v", c)
	}
	res := e.do("GET", secret+"/api/me", nil, c, false)
	var me map[string]any
	json.NewDecoder(res.Body).Decode(&me)
	if res.StatusCode != 200 || me["username"] != "admin" {
		t.Fatalf("me = %d %v", res.StatusCode, me)
	}
	res = e.do("GET", secret+"/api/system", nil, c, false)
	if res.StatusCode != 200 {
		t.Fatalf("system = %d", res.StatusCode)
	}
	e.do("POST", secret+"/api/logout", nil, c, true).Body.Close()
	if res := e.do("GET", secret+"/api/me", nil, c, false); res.StatusCode != 401 {
		t.Fatalf("session survived logout: %d", res.StatusCode)
	}
}

func TestCSRFHeaderRequired(t *testing.T) {
	e := newEnv(t)
	c := e.login()
	res := e.do("POST", secret+"/api/files/mkdir", map[string]string{"path": "/x"}, c, false)
	if res.StatusCode != 403 {
		t.Fatalf("mutation without CSRF header = %d, want 403", res.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(e.root, "x")); err == nil {
		t.Fatal("directory was created despite CSRF failure")
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t)
	var last int
	for i := 0; i < 7; i++ {
		res := e.do("POST", secret+"/api/login", map[string]string{"username": "admin", "password": "wrong"}, nil, false)
		last = res.StatusCode
		res.Body.Close()
	}
	if last != 429 {
		t.Fatalf("expected 429 after repeated failures, got %d", last)
	}
	// even the right password is refused while locked
	res := e.do("POST", secret+"/api/login", map[string]string{"username": "admin", "password": "hunter2hunter2"}, nil, false)
	if res.StatusCode != 429 {
		t.Fatalf("locked client logged in: %d", res.StatusCode)
	}
}

func TestTwoFactorFlow(t *testing.T) {
	e := newEnv(t)
	c := e.login()

	res := e.do("POST", secret+"/api/2fa/setup", nil, c, true)
	var setup struct{ Secret string }
	json.NewDecoder(res.Body).Decode(&setup)
	if setup.Secret == "" {
		t.Fatal("no secret")
	}
	code := currentCode(t, setup.Secret)
	if res := e.do("POST", secret+"/api/2fa/enable", map[string]string{"code": code}, c, true); res.StatusCode != 200 {
		t.Fatalf("enable = %d", res.StatusCode)
	}

	// password alone no longer yields a session
	res = e.do("POST", secret+"/api/login", map[string]string{"username": "admin", "password": "hunter2hunter2"}, nil, false)
	var step struct {
		Need   bool   `json:"need_2fa"`
		Ticket string `json:"ticket"`
	}
	json.NewDecoder(res.Body).Decode(&step)
	if !step.Need || step.Ticket == "" || len(res.Cookies()) != 0 {
		t.Fatalf("expected 2FA challenge without cookie, got %+v cookies=%d", step, len(res.Cookies()))
	}
	if res := e.do("POST", secret+"/api/login/2fa", map[string]string{"ticket": step.Ticket, "code": "000000"}, nil, false); res.StatusCode != 401 {
		t.Fatalf("bad code = %d", res.StatusCode)
	}
	res = e.do("POST", secret+"/api/login/2fa", map[string]string{"ticket": step.Ticket, "code": currentCode(t, setup.Secret)}, nil, false)
	if res.StatusCode != 200 || len(res.Cookies()) == 0 {
		t.Fatalf("2FA login = %d", res.StatusCode)
	}
}

func currentCode(t *testing.T, secret string) string {
	t.Helper()
	code, err := auth.TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func TestFileAPI(t *testing.T) {
	e := newEnv(t)
	c := e.login()

	if res := e.do("POST", secret+"/api/files/mkdir", map[string]string{"path": "/site"}, c, true); res.StatusCode != 200 {
		t.Fatalf("mkdir = %d", res.StatusCode)
	}
	if res := e.do("PUT", secret+"/api/files/content", map[string]string{"path": "/site/index.html", "content": "<h1>hi</h1>"}, c, true); res.StatusCode != 200 {
		t.Fatalf("write = %d", res.StatusCode)
	}
	res := e.do("GET", secret+"/api/files/content?path=/site/index.html", nil, c, false)
	var got struct{ Content string }
	json.NewDecoder(res.Body).Decode(&got)
	if got.Content != "<h1>hi</h1>" {
		t.Fatalf("content = %q", got.Content)
	}

	// multipart upload
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "../../evil.txt") // path components must be stripped
	fw.Write([]byte("payload"))
	mw.Close()
	req, _ := http.NewRequest("POST", e.ts.URL+secret+"/api/files/upload?dir=/site", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set(csrfHeader, "1")
	req.AddCookie(c)
	up, err := e.cli.Do(req)
	if err != nil || up.StatusCode != 200 {
		t.Fatalf("upload err=%v status=%v", err, up)
	}
	if b, err := os.ReadFile(filepath.Join(e.root, "site", "evil.txt")); err != nil || string(b) != "payload" {
		t.Fatalf("upload landed wrong: %v %q", err, b)
	}

	// download
	res = e.do("GET", secret+"/api/files/download?path=/site/evil.txt", nil, c, false)
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("download = %d %q", res.StatusCode, res.Header.Get("Content-Disposition"))
	}

	// traversal and root protection through the API
	res = e.do("GET", secret+"/api/files/content?path=/../../etc/passwd", nil, c, false)
	b := new(bytes.Buffer)
	b.ReadFrom(res.Body)
	if strings.Contains(b.String(), "root:") {
		t.Fatal("API leaked /etc/passwd")
	}
	if res := e.do("DELETE", secret+"/api/files?path=/", nil, c, true); res.StatusCode != 403 {
		t.Fatalf("root delete = %d, want 403", res.StatusCode)
	}
	if res := e.do("DELETE", secret+"/api/files?path=/site", nil, c, true); res.StatusCode != 200 {
		t.Fatalf("delete = %d", res.StatusCode)
	}
}

func TestPasswordChangeSignsOutOthers(t *testing.T) {
	e := newEnv(t)
	a, b := e.login(), e.login()
	res := e.do("POST", secret+"/api/password", map[string]string{"current": "hunter2hunter2", "new": "an even longer one"}, a, true)
	if res.StatusCode != 200 {
		t.Fatalf("change = %d", res.StatusCode)
	}
	if res := e.do("GET", secret+"/api/me", nil, b, false); res.StatusCode != 401 {
		t.Fatalf("other session survived: %d", res.StatusCode)
	}
	if res := e.do("GET", secret+"/api/me", nil, a, false); res.StatusCode != 200 {
		t.Fatalf("current session lost: %d", res.StatusCode)
	}
	if res := e.do("POST", secret+"/api/password", map[string]string{"current": "an even longer one", "new": "short"}, a, true); res.StatusCode != 400 {
		t.Fatalf("short password accepted: %d", res.StatusCode)
	}
}
