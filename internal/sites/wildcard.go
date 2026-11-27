package sites

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/usmanawa2153-beep/appanel/internal/store"
)

var (
	ErrNoCloudflare = errors.New("connect Cloudflare under DNS first; wildcard certificates are verified through DNS")
	ErrNoWildcard   = errors.New("no wildcard certificate covers this site")
)

// directChild reports whether name is exactly one label below parent.
func directChild(name, parent string) bool {
	if !strings.HasSuffix(name, "."+parent) {
		return false
	}
	return !strings.Contains(strings.TrimSuffix(name, "."+parent), ".")
}

// wildcardParent finds an ancestor domain with a live certificate that
// includes "*.<ancestor>" and would cover domain (one label deep only, which
// is all a wildcard certificate covers).
func (m *Manager) wildcardParent(domain string) string {
	parts := strings.Split(domain, ".")
	for i := 1; i < len(parts)-1; i++ {
		parent := strings.Join(parts[i:], ".")
		if !directChild(domain, parent) {
			continue
		}
		ci, err := m.CertInfo(parent)
		if err != nil {
			continue
		}
		for _, n := range ci.Names {
			if n == "*."+parent {
				return parent
			}
		}
	}
	return ""
}

// writeCloudflareCreds stores the API token where certbot's dns-cloudflare
// plugin reads it. The file is created 0600 inside a 0700 folder.
func (m *Manager) writeCloudflareCreds(token string) (string, error) {
	if err := os.MkdirAll(m.CredDir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(m.CredDir, "cloudflare.ini")
	tmp, err := os.CreateTemp(m.CredDir, ".cloudflare-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.WriteString("dns_cloudflare_api_token = " + token + "\n"); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return path, os.Rename(tmp.Name(), path)
}

// IssueWildcardSSL requests one certificate for the site's domain plus
// *.domain using the DNS-01 challenge through Cloudflare, then switches the
// site to HTTPS. Aliases that the wildcard does not cover are included too.
func (m *Manager) IssueWildcardSSL(ctx context.Context, id int64) (store.Site, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, err := m.Store.SiteByID(id)
	if err != nil {
		return store.Site{}, "", ErrNotFound
	}
	if !old.Enabled {
		return store.Site{}, "", ErrSiteDisabled
	}
	st := m.Store.Settings()
	if !ValidEmail(st.ACMEEmail) {
		return store.Site{}, "", ErrNoEmail
	}
	if st.CloudflareToken == "" {
		return store.Site{}, "", ErrNoCloudflare
	}
	if _, err := m.LookPath("certbot"); err != nil {
		return store.Site{}, "", ErrCertbotMissing
	}
	var log strings.Builder
	// The plugin is a separate package; install it on first use.
	if plugins, _ := m.Runner.Run(ctx, "certbot", "plugins"); !strings.Contains(plugins, "dns-cloudflare") {
		out, err := m.Runner.Run(ctx, "apt-get", "install", "-y", "-qq", "python3-certbot-dns-cloudflare")
		log.WriteString("$ apt-get install python3-certbot-dns-cloudflare\n" + tail(out, 1500) + "\n")
		if err != nil {
			return store.Site{}, log.String(), errors.New("could not install the certbot Cloudflare plugin")
		}
	}
	creds, err := m.writeCloudflareCreds(st.CloudflareToken)
	if err != nil {
		return store.Site{}, "", fmt.Errorf("could not store the Cloudflare credentials: %w", err)
	}
	args := []string{"certonly", "--dns-cloudflare", "--dns-cloudflare-credentials", creds,
		"--dns-cloudflare-propagation-seconds", "30", "--cert-name", old.Domain, "-d", old.Domain, "-d", "*." + old.Domain}
	for _, a := range old.Aliases {
		if !directChild(a, old.Domain) {
			args = append(args, "-d", a)
		}
	}
	args = append(args, "--non-interactive", "--agree-tos", "--no-eff-email", "-m", st.ACMEEmail,
		"--keep-until-expiring", "--expand", "--deploy-hook", "systemctl reload nginx")
	out, err := m.Runner.Run(ctx, "certbot", args...)
	log.WriteString(tail(out, 3000))
	if err != nil {
		return store.Site{}, log.String(), errors.New("Let's Encrypt could not issue the wildcard certificate. Check that the Cloudflare token can edit DNS for this domain")
	}
	s := old
	s.SSL, s.CertName = true, ""
	if err := m.Store.UpdateSite(s); err != nil {
		return store.Site{}, log.String(), err
	}
	if err := m.apply(ctx, s); err != nil {
		_ = m.Store.UpdateSite(old)
		return store.Site{}, log.String(), err
	}
	return s, log.String(), nil
}

// UseWildcardSSL switches a site to its parent's existing wildcard
// certificate, so no new certificate is requested.
func (m *Manager) UseWildcardSSL(ctx context.Context, id int64) (store.Site, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, err := m.Store.SiteByID(id)
	if err != nil {
		return store.Site{}, ErrNotFound
	}
	if !old.Enabled {
		return store.Site{}, ErrSiteDisabled
	}
	parent := m.wildcardParent(old.Domain)
	if parent == "" {
		return store.Site{}, ErrNoWildcard
	}
	s := old
	s.SSL, s.CertName = true, parent
	if err := m.Store.UpdateSite(s); err != nil {
		return store.Site{}, err
	}
	if err := m.apply(ctx, s); err != nil {
		_ = m.Store.UpdateSite(old)
		return store.Site{}, err
	}
	return s, nil
}
