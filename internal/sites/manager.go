package sites

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/usmanawa2153-beep/appanel/internal/store"
)

// Errors with messages meant for the UI.
var (
	ErrNginxMissing   = errors.New("nginx is not installed on this server; use \"Install Nginx & Certbot\" first")
	ErrCertbotMissing = errors.New("certbot is not installed on this server; use \"Install Nginx & Certbot\" first")
	ErrNoEmail        = errors.New("set a Let's Encrypt email address in Settings first")
	ErrDuplicate      = errors.New("that domain or alias is already used by another website")
	ErrNotFound       = errors.New("website not found")
	ErrSiteDisabled   = errors.New("enable the website first")
	ErrPanelSite      = errors.New("this website is the panel itself; use Settings → Panel domain to change or remove it")
	ErrNoSSL          = errors.New("issue an SSL certificate before forcing HTTPS")
)

// Runner executes external commands. Tests replace it with a fake.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// ExecRunner runs real commands and returns their combined output.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive", "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Manager applies site changes to Nginx and keeps the store in sync.
type Manager struct {
	Store          *store.Store
	Runner         Runner
	NginxDir       string
	WebRoot        string
	ACMERoot       string
	LetsEncryptDir string
	LogDir         string
	CredDir        string // where the Cloudflare credentials file for certbot lives
	PHPSocketGlob  string
	LookPath       func(string) (string, error)
	LookupHost     func(ctx context.Context, host string) ([]string, error)

	mu sync.Mutex
}

// NewManager returns a Manager with production paths.
func NewManager(st *store.Store) *Manager {
	return &Manager{
		Store:          st,
		Runner:         ExecRunner{},
		NginxDir:       "/etc/nginx",
		WebRoot:        "/var/www",
		ACMERoot:       "/var/www/_acme",
		LetsEncryptDir: "/etc/letsencrypt",
		CredDir:        "/etc/appanel",
		LookupHost:     net.DefaultResolver.LookupHost,
		LogDir:         "/var/log/nginx",
		PHPSocketGlob:  "/run/php/php*-fpm.sock",
		LookPath:       exec.LookPath,
	}
}

func (m *Manager) renderOpts() RenderOptions {
	return RenderOptions{ACMERoot: m.ACMERoot, LetsEncryptDir: m.LetsEncryptDir, LogDir: m.LogDir}
}

func (m *Manager) availPath(domain string) string {
	return filepath.Join(m.NginxDir, "sites-available", "appanel-"+domain+".conf")
}
func (m *Manager) enabledPath(domain string) string {
	return filepath.Join(m.NginxDir, "sites-enabled", "appanel-"+domain+".conf")
}

// ---------- status and setup ----------

// Status describes what the server is ready for.
type Status struct {
	NginxInstalled   bool     `json:"nginx_installed"`
	NginxActive      bool     `json:"nginx_active"`
	CertbotInstalled bool     `json:"certbot_installed"`
	PHPSockets       []string `json:"php_sockets"`
	WebRoot          string   `json:"web_root"`
	ACMEEmail        string   `json:"acme_email"`
}

// PHPSockets lists PHP-FPM sockets that exist on this server.
func (m *Manager) PHPSockets() []string {
	matches, _ := filepath.Glob(m.PHPSocketGlob)
	out := []string{}
	for _, p := range matches {
		if ValidPHPSocket(p) {
			out = append(out, p)
		}
	}
	return out
}

// Status reports installed components.
func (m *Manager) Status(ctx context.Context) Status {
	st := Status{WebRoot: m.WebRoot, PHPSockets: m.PHPSockets(), ACMEEmail: m.Store.Settings().ACMEEmail}
	_, err := m.LookPath("nginx")
	st.NginxInstalled = err == nil
	_, err = m.LookPath("certbot")
	st.CertbotInstalled = err == nil
	if st.NginxInstalled {
		_, err := m.Runner.Run(ctx, "systemctl", "is-active", "--quiet", "nginx")
		st.NginxActive = err == nil
	}
	return st
}

// Setup installs Nginx and Certbot (and optionally PHP-FPM) with apt.
func (m *Manager) Setup(ctx context.Context, withPHP bool) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var log strings.Builder
	run := func(name string, args ...string) error {
		out, err := m.Runner.Run(ctx, name, args...)
		log.WriteString("$ " + name + " " + strings.Join(args, " ") + "\n" + tail(out, 4000) + "\n")
		return err
	}
	if err := run("apt-get", "update", "-qq"); err != nil {
		return log.String(), fmt.Errorf("apt-get update failed")
	}
	pkgs := []string{"nginx", "certbot"}
	if withPHP {
		pkgs = append(pkgs, "php-fpm")
	}
	if err := run("apt-get", append([]string{"install", "-y", "-qq"}, pkgs...)...); err != nil {
		return log.String(), fmt.Errorf("installing %s failed", strings.Join(pkgs, ", "))
	}
	if err := os.MkdirAll(m.ACMERoot, 0o755); err != nil {
		return log.String(), err
	}
	if err := m.ensureUpgradeMap(); err != nil {
		return log.String(), err
	}
	if err := run("systemctl", "enable", "--now", "nginx"); err != nil {
		return log.String(), fmt.Errorf("could not start nginx")
	}
	return log.String(), nil
}

// ---------- config application ----------

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".appanel-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (m *Manager) ensureUpgradeMap() error {
	p := filepath.Join(m.NginxDir, "conf.d", "appanel-upgrade-map.conf")
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	return writeAtomic(p, []byte(UpgradeMap), 0o644)
}

func isLink(p string) bool {
	st, err := os.Lstat(p)
	return err == nil && st.Mode()&os.ModeSymlink != 0
}

// apply writes the vhost, enables or disables it, validates with `nginx -t`
// and reloads. If Nginx rejects the result, the previous state is restored.
func (m *Manager) apply(ctx context.Context, s store.Site) error {
	conf, err := RenderVhost(s, m.renderOpts())
	if err != nil {
		return err
	}
	avail, enabled := m.availPath(s.Domain), m.enabledPath(s.Domain)
	prev, prevErr := os.ReadFile(avail)
	hadPrev := prevErr == nil
	wasEnabled := isLink(enabled)

	restore := func() {
		if hadPrev {
			_ = writeAtomic(avail, prev, 0o644)
		} else {
			_ = os.Remove(avail)
		}
		if wasEnabled {
			_ = os.Remove(enabled)
			_ = os.Symlink(avail, enabled)
		} else {
			_ = os.Remove(enabled)
		}
	}

	if err := m.ensureUpgradeMap(); err != nil {
		return err
	}
	if err := writeAtomic(avail, []byte(conf), 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(enabled), 0o755); err != nil {
		restore()
		return err
	}
	if s.Enabled {
		_ = os.Remove(enabled)
		if err := os.Symlink(avail, enabled); err != nil {
			restore()
			return err
		}
	} else {
		_ = os.Remove(enabled)
	}
	if !s.Enabled && !wasEnabled {
		return nil // nothing live changed
	}
	if out, err := m.Runner.Run(ctx, "nginx", "-t"); err != nil {
		restore()
		return fmt.Errorf("nginx rejected the configuration, nothing was changed:\n%s", tail(out, 1500))
	}
	if out, err := m.Runner.Run(ctx, "systemctl", "reload-or-restart", "nginx"); err != nil {
		return fmt.Errorf("configuration is valid but nginx could not be reloaded:\n%s", tail(out, 1500))
	}
	return nil
}

// ---------- CRUD ----------

// CreateInput is what the UI sends when adding a website.
type CreateInput struct {
	Domain    string   `json:"domain"`
	Aliases   []string `json:"aliases"`
	Type      string   `json:"type"`
	Root      string   `json:"root"`
	ProxyURL  string   `json:"proxy_url"`
	PHPSocket string   `json:"php_socket"`
	OwnerID   int64    `json:"-"`
}

func (m *Manager) insideACMERoot(p string) bool {
	a := filepath.Clean(m.ACMERoot)
	return p == a || strings.HasPrefix(p, a+"/")
}

// Create adds a website, writes its vhost and reloads Nginx.
func (m *Manager) Create(ctx context.Context, in CreateInput) (store.Site, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.LookPath("nginx"); err != nil {
		return store.Site{}, ErrNginxMissing
	}
	domain, err := NormalizeDomain(in.Domain)
	if err != nil {
		return store.Site{}, err
	}
	aliases, err := NormalizeAliases(domain, in.Aliases)
	if err != nil {
		return store.Site{}, err
	}
	if !ValidType(in.Type) {
		return store.Site{}, errors.New("type must be static, php or proxy")
	}
	s := store.Site{OwnerID: in.OwnerID, Domain: domain, Aliases: aliases, Type: in.Type, Enabled: true}

	switch in.Type {
	case store.SiteProxy:
		if s.ProxyURL, err = NormalizeProxyURL(in.ProxyURL); err != nil {
			return store.Site{}, err
		}
	default:
		root := strings.TrimSpace(in.Root)
		if root == "" {
			root = filepath.Join(m.WebRoot, domain, "public_html")
		}
		if s.Root, err = NormalizeRoot(m.WebRoot, root); err != nil {
			return store.Site{}, err
		}
		if m.insideACMERoot(s.Root) {
			return store.Site{}, errors.New("that folder is reserved for certificate validation")
		}
		if in.Type == store.SitePHP {
			s.PHPSocket = in.PHPSocket
			if s.PHPSocket == "" {
				socks := m.PHPSockets()
				if len(socks) == 0 {
					return store.Site{}, errors.New("PHP-FPM was not found; install it with \"Install Nginx & Certbot\" (tick PHP)")
				}
				s.PHPSocket = socks[len(socks)-1]
			}
			if !ValidPHPSocket(s.PHPSocket) {
				return store.Site{}, errors.New("invalid PHP-FPM socket")
			}
		}
		if err := m.prepareRoot(s); err != nil {
			return store.Site{}, fmt.Errorf("could not prepare %s: %w", s.Root, err)
		}
	}

	created, err := m.Store.CreateSite(s)
	if errors.Is(err, store.ErrExists) {
		return store.Site{}, ErrDuplicate
	}
	if err != nil {
		return store.Site{}, err
	}
	if err := m.apply(ctx, created); err != nil {
		_ = m.Store.DeleteSite(created.ID)
		return store.Site{}, err
	}
	return created, nil
}

// prepareRoot creates the document root and a placeholder page, owned by www-data.
func (m *Manager) prepareRoot(s store.Site) error {
	if err := os.MkdirAll(s.Root, 0o755); err != nil {
		return err
	}
	entries, _ := os.ReadDir(s.Root)
	if len(entries) == 0 {
		page := "<!doctype html>\n<meta charset=\"utf-8\">\n<title>" + html.EscapeString(s.Domain) + "</title>\n" +
			"<h1>" + html.EscapeString(s.Domain) + "</h1>\n<p>This website is served by Appanel. Upload your files to replace this page.</p>\n"
		name := "index.html"
		if err := os.WriteFile(filepath.Join(s.Root, name), []byte(page), 0o644); err != nil {
			return err
		}
	}
	if u, err := user.Lookup("www-data"); err == nil {
		uid, _ := strconv.Atoi(u.Uid)
		gid, _ := strconv.Atoi(u.Gid)
		_ = os.Chown(s.Root, uid, gid) // best effort; only root can chown
		if es, _ := os.ReadDir(s.Root); len(es) == 1 {
			_ = os.Chown(filepath.Join(s.Root, es[0].Name()), uid, gid)
		}
	}
	return nil
}

// UpdateInput holds optional changes; nil fields are left alone.
type UpdateInput struct {
	Aliases    *[]string `json:"aliases"`
	ProxyURL   *string   `json:"proxy_url"`
	ForceHTTPS *bool     `json:"force_https"`
	Enabled    *bool     `json:"enabled"`
}

// Update changes a site and re-applies its config, reverting if Nginx objects.
func (m *Manager) Update(ctx context.Context, id int64, in UpdateInput) (store.Site, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, err := m.Store.SiteByID(id)
	if err != nil {
		return store.Site{}, ErrNotFound
	}
	if old.Panel {
		return store.Site{}, ErrPanelSite
	}
	s := old
	s.Aliases = append([]string{}, old.Aliases...)
	if in.Aliases != nil {
		if s.Aliases, err = NormalizeAliases(s.Domain, *in.Aliases); err != nil {
			return store.Site{}, err
		}
	}
	if in.ProxyURL != nil {
		if s.Type != store.SiteProxy {
			return store.Site{}, errors.New("only proxy sites have a proxy URL")
		}
		if s.ProxyURL, err = NormalizeProxyURL(*in.ProxyURL); err != nil {
			return store.Site{}, err
		}
	}
	if in.Enabled != nil {
		s.Enabled = *in.Enabled
	}
	if in.ForceHTTPS != nil {
		if *in.ForceHTTPS && !s.SSL {
			return store.Site{}, ErrNoSSL
		}
		s.ForceHTTPS = *in.ForceHTTPS
	}
	if s.SSL && !sameStrings(s.Aliases, old.Aliases) {
		return store.Site{}, errors.New("aliases cannot be changed after an SSL certificate was issued (the certificate covers the current names); delete and re-create the website to change them")
	}
	if err := m.Store.UpdateSite(s); err != nil {
		if errors.Is(err, store.ErrExists) {
			return store.Site{}, ErrDuplicate
		}
		return store.Site{}, err
	}
	if err := m.apply(ctx, s); err != nil {
		_ = m.Store.UpdateSite(old)
		return store.Site{}, err
	}
	return s, nil
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Delete removes the vhost and the record. Files and certificates are kept.
func (m *Manager) Delete(ctx context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.Store.SiteByID(id)
	if err != nil {
		return ErrNotFound
	}
	if s.Panel {
		return ErrPanelSite
	}
	wasEnabled := isLink(m.enabledPath(s.Domain))
	_ = os.Remove(m.enabledPath(s.Domain))
	_ = os.Remove(m.availPath(s.Domain))
	if wasEnabled {
		if _, err := m.LookPath("nginx"); err == nil {
			if out, err := m.Runner.Run(ctx, "nginx", "-t"); err == nil {
				_, _ = m.Runner.Run(ctx, "systemctl", "reload-or-restart", "nginx")
			} else {
				return fmt.Errorf("website removed, but nginx reports a problem elsewhere:\n%s", tail(out, 1500))
			}
		}
	}
	return m.Store.DeleteSite(id)
}

// ---------- SSL ----------

var emailRe = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$`)

// ValidEmail reports whether s is acceptable as a Let's Encrypt contact address.
func ValidEmail(s string) bool { return len(s) <= 254 && emailRe.MatchString(s) }

// IssueSSL requests a Let's Encrypt certificate (HTTP-01 via the shared
// webroot) for the site's domain and aliases, then switches the vhost to HTTPS.
// The returned string is certbot's output, useful when it fails.
func (m *Manager) IssueSSL(ctx context.Context, id int64) (store.Site, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, err := m.Store.SiteByID(id)
	if err != nil {
		return store.Site{}, "", ErrNotFound
	}
	if !old.Enabled {
		return store.Site{}, "", ErrSiteDisabled
	}
	email := m.Store.Settings().ACMEEmail
	if !ValidEmail(email) {
		return store.Site{}, "", ErrNoEmail
	}
	if _, err := m.LookPath("certbot"); err != nil {
		return store.Site{}, "", ErrCertbotMissing
	}
	if err := os.MkdirAll(m.ACMERoot, 0o755); err != nil {
		return store.Site{}, "", err
	}
	args := []string{"certonly", "--webroot", "-w", m.ACMERoot, "--cert-name", old.Domain}
	for _, n := range old.Names() {
		args = append(args, "-d", n)
	}
	args = append(args, "--non-interactive", "--agree-tos", "--no-eff-email", "-m", email,
		"--keep-until-expiring", "--deploy-hook", "systemctl reload nginx")
	out, err := m.Runner.Run(ctx, "certbot", args...)
	if err != nil {
		return store.Site{}, tail(out, 3000), fmt.Errorf("Let's Encrypt could not issue the certificate. Check that %s points to this server (DNS A/AAAA record) and that port 80 is reachable from the internet", strings.Join(old.Names(), ", "))
	}
	s := old
	s.SSL, s.CertName = true, ""
	if err := m.Store.UpdateSite(s); err != nil {
		return store.Site{}, out, err
	}
	if err := m.apply(ctx, s); err != nil {
		_ = m.Store.UpdateSite(old)
		return store.Site{}, out, err
	}
	return s, tail(out, 3000), nil
}

// ---------- views ----------

// SiteView is a site plus live certificate info, as shown in the UI.
type SiteView struct {
	store.Site
	Cert *CertInfo `json:"cert,omitempty"`
	// WildcardFrom names a parent domain whose wildcard certificate could serve this site.
	WildcardFrom string `json:"wildcard_from,omitempty"`
}

// View attaches certificate details when the site has SSL.
func (m *Manager) View(s store.Site) SiteView {
	if s.Aliases == nil {
		s.Aliases = []string{} // JSON must say [], never null
	}
	v := SiteView{Site: s}
	if s.SSL {
		name := s.Domain
		if s.CertName != "" {
			name = s.CertName
		}
		if ci, err := m.CertInfo(name); err == nil {
			v.Cert = ci
		}
	}
	if s.CertName == "" {
		v.WildcardFrom = m.wildcardParent(s.Domain)
	}
	return v
}

// List returns all sites with certificate info.
func (m *Manager) List() []SiteView {
	sites := m.Store.ListSites()
	out := make([]SiteView, 0, len(sites))
	for _, s := range sites {
		out = append(out, m.View(s))
	}
	return out
}

// Get returns one site view.
func (m *Manager) Get(id int64) (SiteView, error) {
	s, err := m.Store.SiteByID(id)
	if err != nil {
		return SiteView{}, ErrNotFound
	}
	return m.View(s), nil
}
