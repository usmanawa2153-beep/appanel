package server

import (
	"context"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/usmanawa2153-beep/appanel/internal/auth"
	"github.com/usmanawa2153-beep/appanel/internal/config"
	"github.com/usmanawa2153-beep/appanel/internal/dns"
	"github.com/usmanawa2153-beep/appanel/internal/firewall"
)

// restartService asks systemd to restart the panel a moment from now, after
// the HTTP reply has gone out.
func restartService() {
	go func() {
		time.Sleep(1500 * time.Millisecond)
		if err := exec.Command("systemctl", "restart", "appanel").Run(); err != nil {
			logf("could not restart the panel automatically (%v); run: systemctl restart appanel", err)
		}
	}()
}

// confirmPassword re-checks the admin's password for changes that decide how
// the panel can be reached. It shares the login rate limiter.
func (s *Server) confirmPassword(w http.ResponseWriter, r *http.Request, pw string) bool {
	if s.throttled(w, s.clientIP(r)) {
		return false
	}
	if !auth.VerifyPassword(pw, userFrom(r).PasswordHash) {
		s.lim.Fail(s.clientIP(r))
		writeErr(w, http.StatusForbidden, "password is wrong")
		return false
	}
	return true
}

// saveConfigCopy persists a modified copy of the config without touching the
// live one (which handlers read concurrently); the restart applies it.
func (s *Server) saveConfigCopy(mod func(*config.Config)) error {
	c := *s.cfg
	mod(&c)
	if err := c.Validate(); err != nil {
		return err
	}
	return c.Save(s.dataDir)
}

func (s *Server) accessURL(domain, path string) string {
	if domain != "" {
		return "https://" + domain + path + "/"
	}
	host := s.cfg.Host
	if host == "" {
		host = dns.PublicIPv4()
	}
	if host == "" {
		host = "<server-ip>"
	}
	scheme := "http"
	if s.cfg.TLSCert != "" {
		scheme = "https"
	}
	return scheme + "://" + host + ":" + strconv.Itoa(s.cfg.Port) + path + "/"
}

func (s *Server) handlePanelDomainSet(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Domain   string `json:"domain"`
		Password string `json:"password"`
	}
	if !decode(w, r, maxJSON, &in) || !s.confirmPassword(w, r, in.Password) {
		return
	}
	domain := strings.ToLower(strings.TrimSpace(in.Domain))
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	logf("panel domain %q requested by %s", domain, userFrom(r).Username)
	out, err := s.sites.AttachPanel(ctx, domain, s.cfg.Port)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "log": out})
		return
	}
	// Ports 80/443 must be reachable for the domain to work; best effort.
	if st := s.fw.Status(ctx); st.Installed && st.Active {
		for _, p := range []string{"80", "443"} {
			_, _ = s.fw.AddRule(ctx, firewall.RuleInput{Action: "allow", Port: p, Proto: "tcp"})
		}
	}
	// Switch the listener only now that the domain demonstrably works.
	if err := s.saveConfigCopy(func(c *config.Config) { c.PanelDomain = domain }); err != nil {
		_ = s.sites.DetachPanel(ctx)
		writeErr(w, http.StatusInternalServerError, "could not save the configuration: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "url": s.accessURL(domain, s.cfg.SecretPath), "log": out, "restarting": true})
	s.restart()
}

func (s *Server) handlePanelDomainUnset(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, maxJSON, &in) || !s.confirmPassword(w, r, in.Password) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := s.saveConfigCopy(func(c *config.Config) { c.PanelDomain = "" }); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save the configuration: "+err.Error())
		return
	}
	if err := s.sites.DetachPanel(ctx); err != nil {
		logf("detach panel site: %v", err)
	}
	logf("panel domain removed by %s", userFrom(r).Username)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "url": s.accessURL("", s.cfg.SecretPath), "restarting": true})
	s.restart()
}

func (s *Server) handlePanelPath(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path     string `json:"path"`
		Random   bool   `json:"random"`
		Password string `json:"password"`
	}
	if !decode(w, r, maxJSON, &in) || !s.confirmPassword(w, r, in.Password) {
		return
	}
	path := strings.TrimSpace(in.Path)
	if in.Random {
		var err error
		if path, err = config.RandomSecretPath(); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not generate a path")
			return
		}
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if !config.ValidSecretPath(path) {
		writeErr(w, http.StatusBadRequest, "the login path must be 8-64 letters, digits, - or _ (for example /kkgwgifuiuriuriuyiug)")
		return
	}
	if err := s.saveConfigCopy(func(c *config.Config) { c.SecretPath = path }); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save the configuration: "+err.Error())
		return
	}
	logf("login path changed by %s", userFrom(r).Username)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": path, "url": s.accessURL(s.cfg.PanelDomain, path), "restarting": true})
	s.restart()
}
