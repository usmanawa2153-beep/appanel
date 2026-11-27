package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/usmanawa2153-beep/appanel/internal/dns"
)

// SetDNSClient replaces the Cloudflare client (used by tests).
func (s *Server) SetDNSClient(c *dns.Client) { s.dns = c }

func (s *Server) dnsErr(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	var ae *dns.APIError
	switch {
	case errors.Is(err, dns.ErrNoToken):
		code = http.StatusPreconditionFailed
	case errors.As(err, &ae):
		code = http.StatusBadGateway
	}
	writeErr(w, code, err.Error())
}

func (s *Server) dnsCtx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 40*time.Second)
}

func (s *Server) handleDNSStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"configured": s.st.Settings().CloudflareToken != "", "server_ip": dns.PublicIPv4()})
}

// handleDNSToken verifies a token with Cloudflare before saving it.
func (s *Server) handleDNSToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if !decode(w, r, maxJSON, &in) {
		return
	}
	tok := strings.TrimSpace(in.Token)
	if len(tok) < 20 || len(tok) > 200 || strings.ContainsAny(tok, " \t\r\n\"") {
		writeErr(w, http.StatusBadRequest, "that does not look like a Cloudflare API token")
		return
	}
	probe := &dns.Client{BaseURL: s.dns.BaseURL, Token: func() string { return tok }, HTTP: s.dns.HTTP}
	ctx, cancel := s.dnsCtx(r)
	defer cancel()
	if err := probe.Verify(ctx); err != nil {
		s.dnsErr(w, err)
		return
	}
	st := s.st.Settings()
	st.CloudflareToken = tok
	if err := s.st.SetSettings(st); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save")
		return
	}
	logf("cloudflare token saved by %s", userFrom(r).Username)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDNSTokenDelete(w http.ResponseWriter, r *http.Request) {
	st := s.st.Settings()
	st.CloudflareToken = ""
	if err := s.st.SetSettings(st); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save")
		return
	}
	logf("cloudflare token removed by %s", userFrom(r).Username)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDNSZones(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.dnsCtx(r)
	defer cancel()
	z, err := s.dns.Zones(ctx)
	if err != nil {
		s.dnsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"zones": z})
}

func (s *Server) handleDNSRecords(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.dnsCtx(r)
	defer cancel()
	recs, err := s.dns.Records(ctx, r.PathValue("zone"))
	if err != nil {
		s.dnsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": recs})
}

func (s *Server) handleDNSCreate(w http.ResponseWriter, r *http.Request) {
	var in dns.Record
	if !decode(w, r, maxJSON, &in) {
		return
	}
	ctx, cancel := s.dnsCtx(r)
	defer cancel()
	rec, err := s.dns.CreateRecord(ctx, r.PathValue("zone"), in)
	if err != nil {
		s.dnsErr(w, err)
		return
	}
	logf("dns record %s %s created by %s", rec.Type, rec.Name, userFrom(r).Username)
	writeJSON(w, http.StatusCreated, rec)
}

func (s *Server) handleDNSUpdate(w http.ResponseWriter, r *http.Request) {
	var in dns.Record
	if !decode(w, r, maxJSON, &in) {
		return
	}
	ctx, cancel := s.dnsCtx(r)
	defer cancel()
	rec, err := s.dns.UpdateRecord(ctx, r.PathValue("zone"), r.PathValue("id"), in)
	if err != nil {
		s.dnsErr(w, err)
		return
	}
	logf("dns record %s %s updated by %s", rec.Type, rec.Name, userFrom(r).Username)
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleDNSDelete(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.dnsCtx(r)
	defer cancel()
	if err := s.dns.DeleteRecord(ctx, r.PathValue("zone"), r.PathValue("id")); err != nil {
		s.dnsErr(w, err)
		return
	}
	logf("dns record %s deleted by %s", r.PathValue("id"), userFrom(r).Username)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDNSPoint(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name    string `json:"name"`
		IP      string `json:"ip"`
		Proxied bool   `json:"proxied"`
	}
	if !decode(w, r, maxJSON, &in) {
		return
	}
	ctx, cancel := s.dnsCtx(r)
	defer cancel()
	rec, how, err := s.dns.PointToServer(ctx, r.PathValue("zone"), strings.TrimSpace(in.Name), strings.TrimSpace(in.IP), in.Proxied)
	if err != nil {
		s.dnsErr(w, err)
		return
	}
	logf("dns %s %s -> %s (%s) by %s", rec.Type, rec.Name, in.IP, how, userFrom(r).Username)
	writeJSON(w, http.StatusOK, map[string]any{"record": rec, "result": how})
}
