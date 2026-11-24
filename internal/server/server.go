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
	"time"

	"github.com/usmanawa2153-beep/appanel/internal/auth"
	"github.com/usmanawa2153-beep/appanel/internal/config"
	"github.com/usmanawa2153-beep/appanel/internal/files"
	"github.com/usmanawa2153-beep/appanel/internal/store"
	"github.com/usmanawa2153-beep/appanel/internal/system"
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
	return &Server{
		cfg:     cfg,
		st:      st,
		sess:    auth.NewSessions(time.Duration(cfg.SessionHours) * time.Hour),
		lim:     auth.NewLimiter(5, 10*time.Minute, 15*time.Minute),
		sys:     system.NewSampler(),
		fs:      ffs,
		ui:      ui,
		version: version,
		secure:  cfg.TLSCert != "",
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
		if s.cfg.TLSCert != "" {
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

// clientIP uses the socket address only. X-Forwarded-For is deliberately
// ignored: it is client-controlled and would let attackers dodge the limiter.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func logf(format string, args ...any) { log.Printf(format, args...) }
