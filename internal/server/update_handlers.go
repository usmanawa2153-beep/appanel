package server

import (
	"context"
	"net/http"
	"time"

	"github.com/usmanawa2153-beep/appanel/internal/updater"
)

// SetUpdater replaces the updater (used by tests).
func (s *Server) SetUpdater(u *updater.Updater) { s.up = u }

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	info, err := s.up.Check(ctx, r.URL.Query().Get("force") == "1")
	if err != nil {
		// Still tell the UI which version runs and how to update by hand.
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "current": s.version, "one_liner": updater.OneLiner})
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, maxJSON, &in) || !s.confirmPassword(w, r, in.Password) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	logf("update requested by %s", userFrom(r).Username)
	v, err := s.up.Apply(ctx)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	logf("updated to %s", v)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": v, "restarting": true})
	s.restart()
}
