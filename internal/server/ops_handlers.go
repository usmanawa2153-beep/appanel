package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/usmanawa2153-beep/appanel/internal/cron"
	"github.com/usmanawa2153-beep/appanel/internal/firewall"
)

// SetCronManager and SetFirewallManager let tests inject managers with fake runners.
func (s *Server) SetCronManager(m *cron.Manager)         { s.cron = m }
func (s *Server) SetFirewallManager(m *firewall.Manager) { s.fw = m }

func pathInt(r *http.Request, name string) (int64, bool) {
	n, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return n, err == nil && n > 0
}

// ---------- cron ----------

func (s *Server) cronErr(w http.ResponseWriter, err error) {
	if errors.Is(err, cron.ErrNotFound) {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeErr(w, http.StatusBadRequest, err.Error())
}

func (s *Server) handleCronList(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"jobs": s.cron.List()})
}

func (s *Server) handleCronCreate(w http.ResponseWriter, r *http.Request) {
	var in cron.Input
	if !decode(w, r, maxJSON, &in) {
		return
	}
	j, err := s.cron.Create(in)
	if err != nil {
		s.cronErr(w, err)
		return
	}
	logf("cron job %q created by %s", j.Name, userFrom(r).Username)
	writeJSON(w, http.StatusCreated, j)
}

func (s *Server) handleCronUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusNotFound, "job not found")
		return
	}
	var in cron.Input
	if !decode(w, r, maxJSON, &in) {
		return
	}
	j, err := s.cron.Update(id, in)
	if err != nil {
		s.cronErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) handleCronDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusNotFound, "job not found")
		return
	}
	if err := s.cron.Delete(id); err != nil {
		s.cronErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleCronRun(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusNotFound, "job not found")
		return
	}
	logf("cron job %d run manually by %s", id, userFrom(r).Username)
	out, err := s.cron.RunNow(context.Background(), id)
	if errors.Is(err, cron.ErrNotFound) {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": err == nil, "output": out, "error": errString(err)})
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ---------- firewall ----------

func (s *Server) fwErr(w http.ResponseWriter, err error, log string) {
	code := http.StatusBadRequest
	switch {
	case errors.Is(err, firewall.ErrNotInstalled):
		code = http.StatusPreconditionFailed
	case errors.Is(err, firewall.ErrProtected), errors.Is(err, firewall.ErrChanged):
		code = http.StatusConflict
	}
	writeJSON(w, code, map[string]any{"error": err.Error(), "log": log})
}

func (s *Server) handleFirewallStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, s.fw.Status(ctx))
}

func (s *Server) handleFirewallInstall(w http.ResponseWriter, _ *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out, err := s.fw.Install(ctx)
	if err != nil {
		s.fwErr(w, err, out)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "log": out})
}

func (s *Server) handleFirewallEnable(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	out, err := s.fw.Enable(ctx)
	logf("firewall enabled by %s: err=%v", userFrom(r).Username, err)
	if err != nil {
		s.fwErr(w, err, out)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "log": out})
}

func (s *Server) handleFirewallDisable(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.fw.Disable(ctx); err != nil {
		s.fwErr(w, err, "")
		return
	}
	logf("firewall disabled by %s", userFrom(r).Username)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleFirewallAdd(w http.ResponseWriter, r *http.Request) {
	var in firewall.RuleInput
	if !decode(w, r, maxJSON, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	out, err := s.fw.AddRule(ctx, in)
	if err != nil {
		s.fwErr(w, err, out)
		return
	}
	logf("firewall rule added by %s: %+v", userFrom(r).Username, in)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleFirewallDelete(w http.ResponseWriter, r *http.Request) {
	n, ok := pathInt(r, "n")
	if !ok {
		writeErr(w, http.StatusNotFound, "rule not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.fw.DeleteRule(ctx, int(n), r.URL.Query().Get("to")); err != nil {
		s.fwErr(w, err, "")
		return
	}
	logf("firewall rule %d deleted by %s", n, userFrom(r).Username)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
