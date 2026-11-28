package server

import (
	"context"
	"net/http"
	"time"

	"github.com/usmanawa2153-beep/appanel/internal/apps"
)

// SetWordPress replaces the WordPress installer (used by tests).
func (s *Server) SetWordPress(w *apps.WordPress) { s.wp = w }

func (s *Server) handleWordPressInstall(w http.ResponseWriter, r *http.Request) {
	var in apps.WPInput
	if !decode(w, r, maxJSON, &in) {
		return
	}
	if err := in.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// One install at a time: they share apt, the database server and Nginx.
	if !s.appBusy.TryLock() {
		writeErr(w, http.StatusConflict, "another app is being installed; wait for it to finish")
		return
	}
	defer s.appBusy.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	logf("wordpress install on %q requested by %s", in.Domain, userFrom(r).Username)
	res, err := s.wp.Install(ctx, in)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "log": res.Log})
		return
	}
	logf("wordpress installed on %s", in.Domain)
	writeJSON(w, http.StatusCreated, res)
}
