// Package server wires the HTTP API and the embedded dashboard together.
//
// Everything lives under the configured secret path, so scanners probing the
// panel's port see a bare 404 for every other URL.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/usmanawa2153-beep/appanel/internal/apps"
	"github.com/usmanawa2153-beep/appanel/internal/auth"
	"github.com/usmanawa2153-beep/appanel/internal/backups"
	"github.com/usmanawa2153-beep/appanel/internal/config"
	"github.com/usmanawa2153-beep/appanel/internal/cron"
	"github.com/usmanawa2153-beep/appanel/internal/databases"
	"github.com/usmanawa2153-beep/appanel/internal/dns"
	"github.com/usmanawa2153-beep/appanel/internal/files"
	"github.com/usmanawa2153-beep/appanel/internal/firewall"
	"github.com/usmanawa2153-beep/appanel/internal/sites"
	"github.com/usmanawa2153-beep/appanel/internal/store"
	"github.com/usmanawa2153-beep/appanel/internal/system"
	"github.com/usmanawa2153-beep/appanel/internal/updater"
)

const (
	cookieName = "appanel_session"
	csrfHeader = "X-Appanel-CSRF"
	maxJSON    = 1 << 20
)

// Server holds the panel's dependencies.
type Server struct {
	cfg     *config.Config
	st      *store.Store
	sess    *auth.Sessions
	lim     *auth.Limiter
	sys     *system.Sampler
	sites   *sites.Manager
	cron    *cron.Manager
	fw      *firewall.Manager
	db      *databases.Manager
	bk      *backups.Manager
	dns     *dns.Client
	dataDir string
	up      *updater.Updater
	wp      *apps.WordPress
	appBusy sync.Mutex
	wpBusy  sync.Mutex
	restart func() // restarts the panel service; replaced in tests
	fs      *files.FS
	ui      fs.FS
	version string
	secure  bool // serving over TLS => Secure cookies
	dummy   string
}

// New builds a Server. ui is the embedded dashboard rooted at index.html.
func New(cfg *config.Config, st *store.Store, ui fs.FS, version string) (*Server, error) {
	ffs, err := files.New(cfg.FileRoot)
	if err != nil {
		return nil, err
	}
	dummy, err := auth.HashPassword("appanel-timing-equaliser")
	if err != nil {
		return nil, err
	}
	dbm := databases.NewManager()
	sm := sites.NewManager(st)
	return &Server{
		cfg:     cfg,
		st:      st,
		sess:    auth.NewSessions(time.Duration(cfg.SessionHours) * time.Hour),
		lim:     auth.NewLimiter(5, 10*time.Minute, 15*time.Minute),
		sys:     system.NewSampler(),
		sites:   sm,
		wp:      apps.NewWordPress(st, sm, dbm),
		cron:    cron.NewManager(st),
		fw:      firewall.NewManager(func() int { return cfg.Port }),
		db:      dbm,
		bk:      backups.NewManager(st, dbm),
		dns:     dns.NewClient(func() string { return st.Settings().CloudflareToken }),
		fs:      ffs,
		ui:      ui,
		version: version,
		secure:  cfg.SecureCookies(),
		dataDir: config.DefaultDataDir(),
		up:      updater.New(version),
		restart: restartService,
		dummy:   dummy,
	}, nil
}

// Handler returns the root handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Public.
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/login/2fa", s.handleLogin2FA)
	mux.HandleFunc("GET /api/ping", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": s.version})
	})

	// Authenticated.
	mux.HandleFunc("POST /api/logout", s.authed(s.handleLogout))
	mux.HandleFunc("GET /api/me", s.authed(s.handleMe))
	mux.HandleFunc("POST /api/password", s.authed(s.handlePassword))
	mux.HandleFunc("POST /api/2fa/setup", s.authed(s.handle2FASetup))
	mux.HandleFunc("POST /api/2fa/enable", s.authed(s.handle2FAEnable))
	mux.HandleFunc("POST /api/2fa/disable", s.authed(s.handle2FADisable))
	mux.HandleFunc("GET /api/system", s.authed(s.handleSystem))
	mux.HandleFunc("GET /api/terminal", s.authed(s.handleTerminal))

	mux.HandleFunc("GET /api/sites", s.authed(s.handleSiteList))
	mux.HandleFunc("POST /api/sites", s.authed(s.handleSiteCreate))
	mux.HandleFunc("GET /api/sites/status", s.authed(s.handleSiteStatus))
	mux.HandleFunc("POST /api/sites/setup", s.authed(s.handleSiteSetup))
	mux.HandleFunc("GET /api/sites/{id}", s.authed(s.handleSiteGet))
	mux.HandleFunc("PUT /api/sites/{id}", s.authed(s.handleSiteUpdate))
	mux.HandleFunc("DELETE /api/sites/{id}", s.authed(s.handleSiteDelete))
	mux.HandleFunc("POST /api/sites/{id}/ssl", s.authed(s.handleSiteSSL))
	mux.HandleFunc("POST /api/sites/{id}/ssl/wildcard", s.authed(s.handleSiteWildcard))
	mux.HandleFunc("POST /api/sites/{id}/ssl/use-wildcard", s.authed(s.handleSiteUseWildcard))
	mux.HandleFunc("GET /api/cron", s.authed(s.handleCronList))
	mux.HandleFunc("POST /api/cron", s.authed(s.handleCronCreate))
	mux.HandleFunc("PUT /api/cron/{id}", s.authed(s.handleCronUpdate))
	mux.HandleFunc("DELETE /api/cron/{id}", s.authed(s.handleCronDelete))
	mux.HandleFunc("POST /api/cron/{id}/run", s.authed(s.handleCronRun))

	mux.HandleFunc("GET /api/firewall", s.authed(s.handleFirewallStatus))
	mux.HandleFunc("POST /api/firewall/install", s.authed(s.handleFirewallInstall))
	mux.HandleFunc("POST /api/firewall/enable", s.authed(s.handleFirewallEnable))
	mux.HandleFunc("POST /api/firewall/disable", s.authed(s.handleFirewallDisable))
	mux.HandleFunc("POST /api/firewall/rules", s.authed(s.handleFirewallAdd))
	mux.HandleFunc("DELETE /api/firewall/rules/{n}", s.authed(s.handleFirewallDelete))
	mux.HandleFunc("GET /api/databases", s.authed(s.handleDBList))
	mux.HandleFunc("POST /api/databases/{engine}/install", s.authed(s.handleDBInstall))
	mux.HandleFunc("POST /api/databases/{engine}", s.authed(s.handleDBCreate))
	mux.HandleFunc("DELETE /api/databases/{engine}/{name}", s.authed(s.handleDBDrop))
	mux.HandleFunc("POST /api/databases/{engine}/{name}/password", s.authed(s.handleDBPassword))
	mux.HandleFunc("GET /api/backups", s.authed(s.handleBackupList))
	mux.HandleFunc("POST /api/backups/site", s.authed(s.handleBackupSite))
	mux.HandleFunc("POST /api/backups/db", s.authed(s.handleBackupDB))
	mux.HandleFunc("DELETE /api/backups/{name}", s.authed(s.handleBackupDelete))
	mux.HandleFunc("POST /api/backups/{name}/restore", s.authed(s.handleBackupRestore))
	mux.HandleFunc("GET /api/backups/{name}/download", s.authed(s.handleBackupDownload))
	mux.HandleFunc("GET /api/dns", s.authed(s.handleDNSStatus))
	mux.HandleFunc("PUT /api/dns/token", s.authed(s.handleDNSToken))
	mux.HandleFunc("DELETE /api/dns/token", s.authed(s.handleDNSTokenDelete))
	mux.HandleFunc("GET /api/dns/zones", s.authed(s.handleDNSZones))
	mux.HandleFunc("GET /api/dns/zones/{zone}/records", s.authed(s.handleDNSRecords))
	mux.HandleFunc("POST /api/dns/zones/{zone}/records", s.authed(s.handleDNSCreate))
	mux.HandleFunc("PUT /api/dns/zones/{zone}/records/{id}", s.authed(s.handleDNSUpdate))
	mux.HandleFunc("DELETE /api/dns/zones/{zone}/records/{id}", s.authed(s.handleDNSDelete))
	mux.HandleFunc("POST /api/dns/zones/{zone}/point", s.authed(s.handleDNSPoint))
	mux.HandleFunc("POST /api/apps/wordpress", s.authed(s.handleWordPressInstall))
	s.registerWordPress(mux)
	mux.HandleFunc("GET /api/update", s.authed(s.handleUpdateCheck))
	mux.HandleFunc("POST /api/update/apply", s.authed(s.handleUpdateApply))
	mux.HandleFunc("POST /api/panel/domain", s.authed(s.handlePanelDomainSet))
	mux.HandleFunc("DELETE /api/panel/domain", s.authed(s.handlePanelDomainUnset))
	mux.HandleFunc("POST /api/panel/path", s.authed(s.handlePanelPath))
	mux.HandleFunc("GET /api/settings", s.authed(s.handleSettingsGet))
	mux.HandleFunc("PUT /api/settings", s.authed(s.handleSettingsPut))

	mux.HandleFunc("GET /api/files", s.authed(s.handleList))
	mux.HandleFunc("GET /api/files/content", s.authed(s.handleRead))
	mux.HandleFunc("PUT /api/files/content", s.authed(s.handleWrite))
	mux.HandleFunc("POST /api/files/mkdir", s.authed(s.handleMkdir))
	mux.HandleFunc("POST /api/files/rename", s.authed(s.handleRename))
	mux.HandleFunc("DELETE /api/files", s.authed(s.handleDelete))
	mux.HandleFunc("POST /api/files/upload", s.authed(s.handleUpload))
	mux.HandleFunc("GET /api/files/download", s.authed(s.handleDownload))

	static := http.FileServerFS(s.ui)
	mux.Handle("GET /", noStore(static))

	prefix := s.cfg.SecretPath
	inner := http.StripPrefix(prefix, mux)

	root := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// With a panel domain attached the panel answers that name only, so
		// reaching it by IP address (or any other name) gets a bare 404.
		if d := s.cfg.PanelDomain; d != "" && !strings.EqualFold(hostOnly(r.Host), d) {
			http.NotFound(w, r)
			return
		}
		switch {
		case r.URL.Path == prefix:
			http.Redirect(w, r, prefix+"/", http.StatusMovedPermanently)
		case strings.HasPrefix(r.URL.Path, prefix+"/"):
			inner.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
	return secureHeaders(root)
}

// Run starts background samplers and serves until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	s.sys.Start(ctx)
	srv := &http.Server{
		Addr:              s.cfg.Addr(),
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	errc := make(chan error, 1)
	go func() {
		if s.cfg.UseTLS() {
			errc <- srv.ListenAndServeTLS(s.cfg.TLSCert, s.cfg.TLSKey)
		} else {
			errc <- srv.ListenAndServe()
		}
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shctx)
	}
}

// ---- helpers ----

func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decode(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeErr(w, http.StatusRequestEntityTooLarge, "request too large")
		} else {
			writeErr(w, http.StatusBadRequest, "invalid JSON body")
		}
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "unexpected data after JSON body")
		return false
	}
	return true
}

// clientIP is the address of whoever is connecting. It is the socket address,
// never a client-supplied header: X-Forwarded-For would let attackers dodge
// the login limiter. The one exception is a panel domain, where every request
// arrives from Nginx on loopback and Nginx overwrites X-Real-IP with the real
// peer address; only then, and only from a loopback socket, is it believed.
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if s.cfg.PanelDomain != "" {
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			if real := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); real != nil {
				return real.String()
			}
		}
	}
	return host
}

func hostOnly(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		return host
	}
	return h
}

func logf(format string, args ...any) { log.Printf(format, args...) }
