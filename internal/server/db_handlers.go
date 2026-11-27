package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/usmanawa2153-beep/appanel/internal/databases"
)

// SetDatabaseManager replaces the database manager (used by tests).
func (s *Server) SetDatabaseManager(m *databases.Manager) { s.db = m }

func (s *Server) dbErr(w http.ResponseWriter, err error, log string) {
	code := http.StatusBadRequest
	switch {
	case errors.Is(err, databases.ErrNotInstalled):
		code = http.StatusPreconditionFailed
	case errors.Is(err, databases.ErrExists):
		code = http.StatusConflict
	}
	writeJSON(w, code, map[string]any{"error": err.Error(), "log": log})
}

func dbEngine(w http.ResponseWriter, r *http.Request) (string, bool) {
	e := r.PathValue("engine")
	if !databases.ValidEngine(e) {
		writeErr(w, http.StatusNotFound, "unknown database engine")
		return "", false
	}
	return e, true
}

func (s *Server) handleDBList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, map[string]any{"engines": []databases.EngineStatus{
		s.db.Status(ctx, databases.MySQL), s.db.Status(ctx, databases.Postgres)}})
}

func (s *Server) handleDBInstall(w http.ResponseWriter, r *http.Request) {
	e, ok := dbEngine(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	logf("database server %s install requested by %s", e, userFrom(r).Username)
	out, err := s.db.Install(ctx, e)
	if err != nil {
		s.dbErr(w, err, out)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "log": out})
}

func (s *Server) handleDBCreate(w http.ResponseWriter, r *http.Request) {
	e, ok := dbEngine(w, r)
	if !ok {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, maxJSON, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	res, err := s.db.Create(ctx, e, in.Name)
	if err != nil {
		s.dbErr(w, err, "")
		return
	}
	logf("database %s/%s created by %s", e, in.Name, userFrom(r).Username)
	writeJSON(w, http.StatusCreated, res)
}

func (s *Server) handleDBDrop(w http.ResponseWriter, r *http.Request) {
	e, ok := dbEngine(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if err := s.db.Drop(ctx, e, r.PathValue("name")); err != nil {
		s.dbErr(w, err, "")
		return
	}
	logf("database %s/%s dropped by %s", e, r.PathValue("name"), userFrom(r).Username)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDBPassword(w http.ResponseWriter, r *http.Request) {
	e, ok := dbEngine(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	res, err := s.db.ResetPassword(ctx, e, r.PathValue("name"))
	if err != nil {
		s.dbErr(w, err, "")
		return
	}
	logf("database %s/%s password reset by %s", e, r.PathValue("name"), userFrom(r).Username)
	writeJSON(w, http.StatusOK, res)
}
