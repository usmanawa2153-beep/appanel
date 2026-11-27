package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/usmanawa2153-beep/appanel/internal/sites"
)

// SetSiteManager replaces the website manager (tests inject a fake-runner one).
func (s *Server) SetSiteManager(m *sites.Manager) { s.sites = m }

func (s *Server) siteErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sites.ErrNotFound):
		writeErr(w, http.StatusNotFound, err.Error())
	case errors.Is(err, sites.ErrDuplicate):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, sites.ErrNginxMissing), errors.Is(err, sites.ErrCertbotMissing),
		errors.Is(err, sites.ErrNoEmail), errors.Is(err, sites.ErrSiteDisabled), errors.Is(err, sites.ErrNoSSL):
		writeErr(w, http.StatusPreconditionFailed, err.Error())
	default:
		// These messages are written for the user (validation text, nginx -t output).
		writeErr(w, http.StatusBadRequest, err.Error())
	}
}

func siteID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func (s *Server) handleSiteStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, s.sites.Status(ctx))
}

func (s *Server) handleSiteSetup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PHP bool `json:"php"`
	}
	if !decode(w, r, maxJSON, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	out, err := s.sites.Setup(ctx, in.PHP)
	logf("site setup by %s: err=%v", userFrom(r).Username, err)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "log": out})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "log": out})
}

func (s *Server) handleSiteList(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"sites": s.sites.List()})
}

func (s *Server) handleSiteGet(w http.ResponseWriter, r *http.Request) {
	id, ok := siteID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "website not found")
		return
	}
	v, err := s.sites.Get(id)
	if err != nil {
		s.siteErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleSiteCreate(w http.ResponseWriter, r *http.Request) {
	var in sites.CreateInput
	if !decode(w, r, maxJSON, &in) {
		return
	}
	in.OwnerID = userFrom(r).ID
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	site, err := s.sites.Create(ctx, in)
	if err != nil {
		s.siteErr(w, err)
		return
	}
	logf("website %s created by %s", site.Domain, userFrom(r).Username)
	writeJSON(w, http.StatusCreated, s.sites.View(site))
}

func (s *Server) handleSiteUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := siteID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "website not found")
		return
	}
	var in sites.UpdateInput
	if !decode(w, r, maxJSON, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	site, err := s.sites.Update(ctx, id, in)
	if err != nil {
		s.siteErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.sites.View(site))
}

func (s *Server) handleSiteDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := siteID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "website not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if err := s.sites.Delete(ctx, id); err != nil {
		s.siteErr(w, err)
		return
	}
	logf("website %d deleted by %s", id, userFrom(r).Username)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSiteSSL(w http.ResponseWriter, r *http.Request) {
	id, ok := siteID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "website not found")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	site, out, err := s.sites.IssueSSL(ctx, id)
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, sites.ErrNotFound) {
			code = http.StatusNotFound
		} else if errors.Is(err, sites.ErrNoEmail) || errors.Is(err, sites.ErrCertbotMissing) || errors.Is(err, sites.ErrSiteDisabled) {
			code = http.StatusPreconditionFailed
		}
		writeJSON(w, code, map[string]any{"error": err.Error(), "log": out})
		return
	}
	logf("ssl issued for %s by %s", site.Domain, userFrom(r).Username)
	writeJSON(w, http.StatusOK, map[string]any{"site": s.sites.View(site), "log": out})
}

func (s *Server) handleSiteWildcard(w http.ResponseWriter, r *http.Request) {
	id, ok := siteID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "website not found")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	site, out, err := s.sites.IssueWildcardSSL(ctx, id)
	if err != nil {
		code := http.StatusBadRequest
		switch {
		case errors.Is(err, sites.ErrNotFound):
			code = http.StatusNotFound
		case errors.Is(err, sites.ErrNoEmail), errors.Is(err, sites.ErrCertbotMissing), errors.Is(err, sites.ErrSiteDisabled), errors.Is(err, sites.ErrNoCloudflare):
			code = http.StatusPreconditionFailed
		}
		writeJSON(w, code, map[string]any{"error": err.Error(), "log": out})
		return
	}
	logf("wildcard ssl issued for %s by %s", site.Domain, userFrom(r).Username)
	writeJSON(w, http.StatusOK, map[string]any{"site": s.sites.View(site), "log": out})
}

func (s *Server) handleSiteUseWildcard(w http.ResponseWriter, r *http.Request) {
	id, ok := siteID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "website not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	site, err := s.sites.UseWildcardSSL(ctx, id)
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, sites.ErrNotFound) {
			code = http.StatusNotFound
		}
		writeErr(w, code, err.Error())
		return
	}
	logf("site %s switched to wildcard certificate by %s", site.Domain, userFrom(r).Username)
	writeJSON(w, http.StatusOK, map[string]any{"site": s.sites.View(site)})
}

func (s *Server) handleSettingsGet(w http.ResponseWriter, _ *http.Request) {
	st := s.st.Settings()
	writeJSON(w, http.StatusOK, map[string]any{"acme_email": st.ACMEEmail, "cloudflare_configured": st.CloudflareToken != ""})
}

func (s *Server) handleSettingsPut(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ACMEEmail string `json:"acme_email"`
	}
	if !decode(w, r, maxJSON, &in) {
		return
	}
	in.ACMEEmail = strings.TrimSpace(in.ACMEEmail)
	if in.ACMEEmail != "" && !sites.ValidEmail(in.ACMEEmail) {
		writeErr(w, http.StatusBadRequest, "that email address doesn't look valid")
		return
	}
	st := s.st.Settings() // keep every other setting (e.g. the Cloudflare token)
	st.ACMEEmail = in.ACMEEmail
	if err := s.st.SetSettings(st); err != nil {
		logf("save settings: %v", err)
		writeErr(w, http.StatusInternalServerError, "could not save")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"acme_email": st.ACMEEmail, "cloudflare_configured": st.CloudflareToken != ""})
}
