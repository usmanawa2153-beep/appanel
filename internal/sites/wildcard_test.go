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

func writeCert(t *testing.T, m *Manager, name string, sans ...string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(80 * 24 * time.Hour), DNSNames: sans}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(m.LetsEncryptDir, "live", name)
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "fullchain.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}

func TestDirectChild(t *testing.T) {
	for _, c := range []struct {
		n, p string
		want bool
	}{{"a.example.com", "example.com", true}, {"a.b.example.com", "example.com", false}, {"example.com", "example.com", false}, {"badexample.com", "example.com", false}} {
		if directChild(c.n, c.p) != c.want {
			t.Errorf("%s under %s", c.n, c.p)
		}
	}
}

func TestIssueWildcardSSL(t *testing.T) {
	m, fr, dir := newMgr(t)
	m.CredDir = filepath.Join(dir, "cred")
	ctx := context.Background()
	s, _ := m.Create(ctx, CreateInput{Domain: "example.com", Aliases: []string{"www.example.com", "other.net"}, Type: "static"})

	if _, _, err := m.IssueWildcardSSL(ctx, s.ID); !errors.Is(err, ErrNoEmail) {
		t.Fatalf("no email: %v", err)
	}
	m.Store.SetSettings(store.Settings{ACMEEmail: "a@example.com"})
	if _, _, err := m.IssueWildcardSSL(ctx, s.ID); !errors.Is(err, ErrNoCloudflare) {
		t.Fatalf("no token: %v", err)
	}
	m.Store.SetSettings(store.Settings{ACMEEmail: "a@example.com", CloudflareToken: "cf_token_value_1234567890"})

	fr.failCB = true
	if got, _, err := m.IssueWildcardSSL(ctx, s.ID); err == nil || got.SSL {
		t.Fatalf("certbot failure: %v", err)
	}
	if g, _ := m.Store.SiteByID(s.ID); g.SSL {
		t.Fatal("SSL set despite failure")
	}
	fr.failCB = false
	got, _, err := m.IssueWildcardSSL(ctx, s.ID)
	if err != nil || !got.SSL {
		t.Fatalf("%v %+v", err, got)
	}
	var cb string
	for _, c := range fr.calls {
		if c[0] == "certbot" && len(c) > 1 && c[1] == "certonly" {
			cb = strings.Join(c, " ")
		}
	}
	for _, want := range []string{"--dns-cloudflare", "-d example.com", "-d *.example.com", "-d other.net", "--cert-name example.com", "--expand"} {
		if !strings.Contains(cb, want) {
			t.Errorf("missing %q in %s", want, cb)
		}
	}
	if strings.Contains(cb, "-d www.example.com") || strings.Contains(cb, "cf_token") || strings.Contains(cb, "--webroot") {
		t.Errorf("unexpected args / token on command line: %s", cb)
	}
	if !fr.ran("apt-get", "install", "-y", "-qq", "python3-certbot-dns-cloudflare") {
		t.Error("plugin not installed when missing")
	}
	fi, err := os.Stat(filepath.Join(m.CredDir, "cloudflare.ini"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("credentials file: %v %v", err, fi)
	}
	b, _ := os.ReadFile(filepath.Join(m.CredDir, "cloudflare.ini"))
	if string(b) != "dns_cloudflare_api_token = cf_token_value_1234567890\n" {
		t.Errorf("creds %q", b)
	}
	if di, _ := os.Stat(m.CredDir); di.Mode().Perm() != 0o700 {
		t.Errorf("cred dir mode %v", di.Mode())
	}
}

func TestUseWildcardSSL(t *testing.T) {
	m, _, _ := newMgr(t)
	ctx := context.Background()
	parent, _ := m.Create(ctx, CreateInput{Domain: "example.com", Type: "static"})
	child, _ := m.Create(ctx, CreateInput{Domain: "blog.example.com", Type: "static"})
	deep, _ := m.Create(ctx, CreateInput{Domain: "a.blog.example.com", Type: "static"})
	_ = parent

	if _, err := m.UseWildcardSSL(ctx, child.ID); !errors.Is(err, ErrNoWildcard) {
		t.Fatalf("no cert yet: %v", err)
	}
	writeCert(t, m, "example.com", "example.com", "*.example.com")
	if v := m.View(child); v.WildcardFrom != "example.com" {
		t.Errorf("hint = %q", v.WildcardFrom)
	}
	if v := m.View(deep); v.WildcardFrom != "" {
		t.Errorf("a wildcard does not cover two labels deep, hint = %q", v.WildcardFrom)
	}
	if _, err := m.UseWildcardSSL(ctx, deep.ID); !errors.Is(err, ErrNoWildcard) {
		t.Errorf("deep: %v", err)
	}
	got, err := m.UseWildcardSSL(ctx, child.ID)
	if err != nil || !got.SSL || got.CertName != "example.com" {
		t.Fatalf("%v %+v", err, got)
	}
	conf, _ := os.ReadFile(m.availPath("blog.example.com"))
	if !strings.Contains(string(conf), "/live/example.com/fullchain.pem") || strings.Contains(string(conf), "/live/blog.example.com/") {
		t.Errorf("vhost does not use the parent certificate:\n%s", conf)
	}
	if v := m.View(got); v.Cert == nil || v.Cert.Names[1] != "*.example.com" {
		t.Errorf("cert info missing: %+v", v.Cert)
	}
	// A normal certificate request later goes back to the site's own certificate.
	m.Store.SetSettings(store.Settings{ACMEEmail: "a@example.com"})
	again, _, err := m.IssueSSL(ctx, child.ID)
	if err != nil || again.CertName != "" {
		t.Errorf("CertName not reset: %v %+v", err, again)
	}
}
