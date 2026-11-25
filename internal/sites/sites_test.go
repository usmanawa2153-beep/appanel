package sites

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usmanawa2153-beep/appanel/internal/store"
)

func TestNormalizeDomain(t *testing.T) {
	good := map[string]string{"Example.COM": "example.com", " a.b.example.org. ": "a.b.example.org", "xn--bcher-kva.example": "xn--bcher-kva.example", "my-site.co.uk": "my-site.co.uk"}
	for in, want := range good {
		got, err := NormalizeDomain(in)
		if err != nil || got != want {
			t.Errorf("%q -> %q, %v (want %q)", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "localhost", "-a.com", "a-.com", "a..com", "*.example.com", "exa_mple.com", "example.com:8080",
		"example.com;", "example.com } server {", "a b.com", "1.2.3.4", "example.c", "ex\nample.com", strings.Repeat("a", 64) + ".com", "../etc.com"} {
		if d, err := NormalizeDomain(bad); err == nil {
			t.Errorf("%q accepted as %q", bad, d)
		}
	}
}

func TestNormalizeProxyURL(t *testing.T) {
	good := map[string]string{
		"http://127.0.0.1:3000":  "http://127.0.0.1:3000",
		"http://127.0.0.1:3000/": "http://127.0.0.1:3000",
		"https://Backend:8443":   "https://backend:8443",
		"http://localhost":       "http://localhost",
		"http://[::1]:9000":      "http://[::1]:9000",
		"http://app.internal":    "http://app.internal",
	}
	for in, want := range good {
		got, err := NormalizeProxyURL(in)
		if err != nil || got != want {
			t.Errorf("%q -> %q, %v (want %q)", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "127.0.0.1:3000", "ftp://x:21", "http://", "http://u:p@host:80", "http://host:80/path", "http://host:80?x=1",
		"http://host:99999", "http://host;}:80", "http://127.0.0.1:3000;\nproxy_pass http://evil", "http://host:80#frag", "http://a b:80", "unix:/tmp/x.sock", "http://$host"} {
		if u, err := NormalizeProxyURL(bad); err == nil {
			t.Errorf("%q accepted as %q", bad, u)
		}
	}
}

func TestNormalizeRoot(t *testing.T) {
	for _, ok := range []string{"/var/www/example.com/public_html", "/var/www/a/b-c_d.e"} {
		if _, err := NormalizeRoot("/var/www", ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"/etc", "/var/www", "/var/www/../etc", "/var/wwwevil/x", "var/www/x", "/var/www/a b", "/var/www/a;b", "/var/www/$x", "/var/www/a\"b", ""} {
		if r, err := NormalizeRoot("/var/www", bad); err == nil {
			t.Errorf("%q accepted as %q", bad, r)
		}
	}
}

var opts = RenderOptions{ACMERoot: "/var/www/_acme", LetsEncryptDir: "/etc/letsencrypt", LogDir: "/var/log/nginx"}

func TestRenderStatic(t *testing.T) {
	s := store.Site{Domain: "example.com", Aliases: []string{"www.example.com"}, Type: store.SiteStatic, Root: "/var/www/example.com/public_html", Enabled: true}
	conf, err := RenderVhost(s, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"server_name example.com www.example.com;", "listen 80;", "root /var/www/example.com/public_html;",
		"location ^~ /.well-known/acme-challenge/", "root /var/www/_acme;", "try_files $uri $uri/ =404;"} {
		if !strings.Contains(conf, want) {
			t.Errorf("missing %q in:\n%s", want, conf)
		}
	}
	if strings.Contains(conf, "443") || strings.Contains(conf, "ssl_certificate") {
		t.Errorf("plain HTTP site must not mention TLS:\n%s", conf)
	}
}

func TestRenderSSLModes(t *testing.T) {
	s := store.Site{Domain: "example.com", Type: store.SiteStatic, Root: "/var/www/example.com/public_html", Enabled: true, SSL: true}
	both, _ := RenderVhost(s, opts)
	if !strings.Contains(both, "listen 443 ssl;") || !strings.Contains(both, "ssl_certificate /etc/letsencrypt/live/example.com/fullchain.pem;") ||
		!strings.Contains(both, "ssl_certificate_key /etc/letsencrypt/live/example.com/privkey.pem;") {
		t.Errorf("SSL block missing:\n%s", both)
	}
	if strings.Contains(both, "return 301") {
		t.Error("HTTP must not redirect unless ForceHTTPS is on")
	}
	if strings.Count(both, "root /var/www/example.com/public_html;") != 2 {
		t.Errorf("site should be served on both 80 and 443:\n%s", both)
	}
	s.ForceHTTPS = true
	redir, _ := RenderVhost(s, opts)
	if !strings.Contains(redir, "return 301 https://$host$request_uri;") {
		t.Errorf("redirect missing:\n%s", redir)
	}
	if strings.Count(redir, "root /var/www/example.com/public_html;") != 1 {
		t.Errorf("content should only be on 443 when forcing HTTPS:\n%s", redir)
	}
	if !strings.Contains(redir, "/.well-known/acme-challenge/") {
		t.Error("ACME challenge must stay reachable over HTTP for renewals")
	}
}

func TestRenderProxyAndPHP(t *testing.T) {
	p, err := RenderVhost(store.Site{Domain: "app.example.com", Type: store.SiteProxy, ProxyURL: "http://127.0.0.1:3000"}, opts)
	if err != nil || !strings.Contains(p, "proxy_pass http://127.0.0.1:3000;") || !strings.Contains(p, "$connection_upgrade") {
		t.Fatalf("proxy: %v\n%s", err, p)
	}
	ph, err := RenderVhost(store.Site{Domain: "blog.example.com", Type: store.SitePHP, Root: "/var/www/blog/public_html", PHPSocket: "/run/php/php8.3-fpm.sock"}, opts)
	if err != nil || !strings.Contains(ph, "fastcgi_pass unix:/run/php/php8.3-fpm.sock;") || !strings.Contains(ph, "index index.php") {
		t.Fatalf("php: %v\n%s", err, ph)
	}
}

func TestRenderRefusesInjection(t *testing.T) {
	bad := []store.Site{
		{Domain: "a.com; } server { listen 1", Type: store.SiteStatic, Root: "/var/www/x"},
		{Domain: "a.com", Aliases: []string{"b.com\nroot /;"}, Type: store.SiteStatic, Root: "/var/www/x"},
		{Domain: "a.com", Type: store.SiteStatic, Root: "/var/www/x; autoindex on"},
		{Domain: "a.com", Type: store.SiteProxy, ProxyURL: "http://x:80;\nreturn 200"},
		{Domain: "a.com", Type: store.SitePHP, Root: "/var/www/x", PHPSocket: "/run/php/../../tmp/x.sock"},
		{Domain: "a.com", Type: "weird"},
	}
	for i, s := range bad {
		if conf, err := RenderVhost(s, opts); err == nil {
			t.Errorf("case %d rendered:\n%s", i, conf)
		}
	}
	if _, err := RenderVhost(store.Site{Domain: "a.com", Type: store.SiteStatic, Root: "/var/www/x"}, RenderOptions{ACMERoot: "/x;y", LetsEncryptDir: "/e", LogDir: "/l"}); err == nil {
		t.Error("unsafe render option accepted")
	}
}

// ---- manager with a fake runner ----

type fakeRunner struct {
	calls   [][]string
	failNgx bool // make `nginx -t` fail
	failCB  bool // make certbot fail
	out     string
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	switch {
	case name == "nginx" && len(args) > 0 && args[0] == "-t" && f.failNgx:
		return "nginx: [emerg] unknown directive \"bogus\"", errors.New("exit 1")
	case name == "certbot" && f.failCB:
		return "Challenge failed for domain example.com", errors.New("exit 1")
	}
	return f.out, nil
}

func (f *fakeRunner) ran(prefix ...string) bool {
	for _, c := range f.calls {
		if len(c) >= len(prefix) && strings.Join(c[:len(prefix)], " ") == strings.Join(prefix, " ") {
			return true
		}
	}
	return false
}

func newMgr(t *testing.T) (*Manager, *fakeRunner, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	fr := &fakeRunner{}
	m := NewManager(st)
	m.Runner = fr
	m.NginxDir = filepath.Join(dir, "nginx")
	m.WebRoot = filepath.Join(dir, "www")
	m.ACMERoot = filepath.Join(dir, "www", "_acme")
	m.LetsEncryptDir = filepath.Join(dir, "le")
	m.LogDir = filepath.Join(dir, "log")
	m.PHPSocketGlob = filepath.Join(dir, "no-php-*.sock")
	m.LookPath = func(string) (string, error) { return "/usr/bin/fake", nil }
	return m, fr, dir
}

func TestCreateWritesConfigAndReloads(t *testing.T) {
	m, fr, _ := newMgr(t)
	s, err := m.Create(context.Background(), CreateInput{Domain: "Example.com", Aliases: []string{"www.example.com", "WWW.example.com"}, Type: "static"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Domain != "example.com" || len(s.Aliases) != 1 || !s.Enabled {
		t.Fatalf("site = %+v", s)
	}
	conf, err := os.ReadFile(m.availPath("example.com"))
	if err != nil || !strings.Contains(string(conf), "server_name example.com www.example.com;") {
		t.Fatalf("config: %v\n%s", err, conf)
	}
	if !isLink(m.enabledPath("example.com")) {
		t.Fatal("site not enabled")
	}
	if _, err := os.Stat(filepath.Join(s.Root, "index.html")); err != nil {
		t.Fatal("placeholder page missing")
	}
	if !fr.ran("nginx", "-t") || !fr.ran("systemctl", "reload-or-restart", "nginx") {
		t.Fatalf("expected nginx -t then reload, got %v", fr.calls)
	}
	if _, err := os.Stat(filepath.Join(m.NginxDir, "conf.d", "appanel-upgrade-map.conf")); err != nil {
		t.Fatal("websocket upgrade map missing")
	}
}

func TestCreateRollsBackWhenNginxRejects(t *testing.T) {
	m, fr, _ := newMgr(t)
	fr.failNgx = true
	_, err := m.Create(context.Background(), CreateInput{Domain: "bad.example.com", Type: "static"})
	if err == nil || !strings.Contains(err.Error(), "nginx rejected") || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("err = %v", err)
	}
	if _, e := os.Stat(m.availPath("bad.example.com")); e == nil {
		t.Error("config left behind")
	}
	if isLink(m.enabledPath("bad.example.com")) {
		t.Error("symlink left behind")
	}
	if len(m.Store.ListSites()) != 0 {
		t.Error("site left in store")
	}
}

func TestCreateValidationAndDuplicates(t *testing.T) {
	m, _, _ := newMgr(t)
	ctx := context.Background()
	for _, in := range []CreateInput{
		{Domain: "not a domain", Type: "static"},
		{Domain: "a.example.com", Type: "static", Root: "/etc"},
		{Domain: "a.example.com", Type: "static", Root: filepath.Join(m.ACMERoot, "x")},
		{Domain: "a.example.com", Type: "proxy", ProxyURL: "http://x:1/evil"},
		{Domain: "a.example.com", Type: "php"}, // no PHP-FPM socket present
		{Domain: "a.example.com", Type: "nope"},
	} {
		if _, err := m.Create(ctx, in); err == nil {
			t.Errorf("accepted %+v", in)
		}
	}
	if _, err := m.Create(ctx, CreateInput{Domain: "dup.example.com", Type: "static"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(ctx, CreateInput{Domain: "dup.example.com", Type: "static"}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate domain: %v", err)
	}
	if _, err := m.Create(ctx, CreateInput{Domain: "other.example.com", Aliases: []string{"dup.example.com"}, Type: "static"}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("alias clash: %v", err)
	}
}

func TestNginxMissing(t *testing.T) {
	m, _, _ := newMgr(t)
	m.LookPath = func(string) (string, error) { return "", errors.New("nope") }
	if _, err := m.Create(context.Background(), CreateInput{Domain: "a.example.com", Type: "static"}); !errors.Is(err, ErrNginxMissing) {
		t.Fatalf("err = %v", err)
	}
}

func TestDisableEnableDelete(t *testing.T) {
	m, _, _ := newMgr(t)
	ctx := context.Background()
	s, _ := m.Create(ctx, CreateInput{Domain: "x.example.com", Type: "proxy", ProxyURL: "http://127.0.0.1:3000"})
	off := false
	if _, err := m.Update(ctx, s.ID, UpdateInput{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if isLink(m.enabledPath("x.example.com")) {
		t.Fatal("still enabled after disable")
	}
	if _, err := os.Stat(m.availPath("x.example.com")); err != nil {
		t.Fatal("config should be kept while disabled")
	}
	on := true
	if _, err := m.Update(ctx, s.ID, UpdateInput{Enabled: &on}); err != nil || !isLink(m.enabledPath("x.example.com")) {
		t.Fatalf("re-enable: %v", err)
	}
	newURL := "http://127.0.0.1:4000"
	if got, err := m.Update(ctx, s.ID, UpdateInput{ProxyURL: &newURL}); err != nil || got.ProxyURL != newURL {
		t.Fatalf("proxy update: %v %+v", err, got)
	}
	if err := m.Delete(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if _, e := os.Stat(m.availPath("x.example.com")); e == nil || isLink(m.enabledPath("x.example.com")) {
		t.Error("files remain after delete")
	}
	if _, err := m.Get(s.ID); !errors.Is(err, ErrNotFound) {
		t.Error("record remains after delete")
	}
}

func TestUpdateRevertsOnNginxFailure(t *testing.T) {
	m, fr, _ := newMgr(t)
	ctx := context.Background()
	s, _ := m.Create(ctx, CreateInput{Domain: "y.example.com", Type: "proxy", ProxyURL: "http://127.0.0.1:3000"})
	before, _ := os.ReadFile(m.availPath("y.example.com"))
	fr.failNgx = true
	u := "http://127.0.0.1:5000"
	if _, err := m.Update(ctx, s.ID, UpdateInput{ProxyURL: &u}); err == nil {
		t.Fatal("expected failure")
	}
	after, _ := os.ReadFile(m.availPath("y.example.com"))
	if string(before) != string(after) {
		t.Error("config not restored")
	}
	if got, _ := m.Store.SiteByID(s.ID); got.ProxyURL != "http://127.0.0.1:3000" {
		t.Errorf("store not reverted: %+v", got)
	}
	if !isLink(m.enabledPath("y.example.com")) {
		t.Error("site should stay enabled")
	}
}

func TestIssueSSLFlow(t *testing.T) {
	m, fr, _ := newMgr(t)
	ctx := context.Background()
	s, _ := m.Create(ctx, CreateInput{Domain: "ssl.example.com", Aliases: []string{"www.ssl.example.com"}, Type: "static"})

	if _, _, err := m.IssueSSL(ctx, s.ID); !errors.Is(err, ErrNoEmail) {
		t.Fatalf("without email: %v", err)
	}
	force := true
	if _, err := m.Update(ctx, s.ID, UpdateInput{ForceHTTPS: &force}); !errors.Is(err, ErrNoSSL) {
		t.Fatalf("force without ssl: %v", err)
	}
	m.Store.SetSettings(store.Settings{ACMEEmail: "admin@example.com"})

	fr.failCB = true
	if _, out, err := m.IssueSSL(ctx, s.ID); err == nil || !strings.Contains(out, "Challenge failed") {
		t.Fatalf("certbot failure should surface output: %v %q", err, out)
	}
	if got, _ := m.Store.SiteByID(s.ID); got.SSL {
		t.Fatal("SSL flag set despite failure")
	}

	fr.failCB = false
	got, _, err := m.IssueSSL(ctx, s.ID)
	if err != nil || !got.SSL {
		t.Fatalf("issue: %v %+v", err, got)
	}
	var cb []string
	for _, c := range fr.calls {
		if c[0] == "certbot" {
			cb = c
		}
	}
	joined := strings.Join(cb, " ")
	for _, want := range []string{"certonly", "--webroot", "-w " + m.ACMERoot, "--cert-name ssl.example.com", "-d ssl.example.com", "-d www.ssl.example.com",
		"--non-interactive", "--agree-tos", "-m admin@example.com", "--keep-until-expiring"} {
		if !strings.Contains(joined, want) {
			t.Errorf("certbot args missing %q: %s", want, joined)
		}
	}
	conf, _ := os.ReadFile(m.availPath("ssl.example.com"))
	if !strings.Contains(string(conf), "listen 443 ssl;") {
		t.Fatalf("HTTPS not enabled:\n%s", conf)
	}
	if _, err := m.Update(ctx, s.ID, UpdateInput{ForceHTTPS: &force}); err != nil {
		t.Fatal(err)
	}
	conf, _ = os.ReadFile(m.availPath("ssl.example.com"))
	if !strings.Contains(string(conf), "return 301 https://") {
		t.Fatalf("redirect missing:\n%s", conf)
	}
	newAliases := []string{}
	if _, err := m.Update(ctx, s.ID, UpdateInput{Aliases: &newAliases}); err == nil {
		t.Error("aliases must be locked once a certificate exists")
	}
}

func TestIssueSSLRevertsWhenNginxRejectsHTTPS(t *testing.T) {
	m, fr, _ := newMgr(t)
	ctx := context.Background()
	s, _ := m.Create(ctx, CreateInput{Domain: "r.example.com", Type: "static"})
	m.Store.SetSettings(store.Settings{ACMEEmail: "a@example.com"})
	fr.failNgx = true
	if _, _, err := m.IssueSSL(ctx, s.ID); err == nil {
		t.Fatal("expected failure")
	}
	if got, _ := m.Store.SiteByID(s.ID); got.SSL {
		t.Error("SSL flag must be reverted")
	}
}

func TestSetupRunsAptAndStartsNginx(t *testing.T) {
	m, fr, _ := newMgr(t)
	if _, err := m.Setup(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if !fr.ran("apt-get", "update") || !fr.ran("apt-get", "install", "-y", "-qq", "nginx", "certbot", "php-fpm") || !fr.ran("systemctl", "enable", "--now", "nginx") {
		t.Fatalf("calls: %v", fr.calls)
	}
}

func TestValidEmail(t *testing.T) {
	for _, ok := range []string{"a@b.co", "first.last+tag@example.com"} {
		if !ValidEmail(ok) {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"", "a@b", "a b@c.com", "a@b.com;rm", "@b.com", "a@@b.com", "a@b.com\n-d evil.com"} {
		if ValidEmail(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestParseCertFile(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	notAfter := time.Now().Add(45*24*time.Hour + time.Hour)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "example.com"}, Issuer: pkix.Name{CommonName: "Test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter, DNSNames: []string{"example.com", "www.example.com"}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "fullchain.pem")
	os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	ci, err := ParseCertFile(p, time.Now())
	if err != nil || ci.DaysLeft != 45 || len(ci.Names) != 2 || ci.NotAfter.IsZero() {
		t.Fatalf("%+v %v", ci, err)
	}
	if _, err := ParseCertFile(filepath.Join(t.TempDir(), "missing.pem"), time.Now()); err == nil {
		t.Error("missing file should error")
	}
}
