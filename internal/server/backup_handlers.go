package server

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"os"
	"time"

	"github.com/usmanawa2153-beep/appanel/internal/backups"
	"github.com/usmanawa2153-beep/appanel/internal/config"
)

// SetBackupManager replaces the backup manager (used by tests).
func (s *Server) SetBackupManager(m *backups.Manager) { s.bk = m }

func (s *Server) bkErr(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	if errors.Is(err, backups.ErrNotFound) {
		code = http.StatusNotFound
	}
	writeErr(w, code, err.Error())
}

func (s *Server) handleBackupList(w http.ResponseWriter, _ *http.Request) {
	exe, err := os.Executable()
	if err != nil {
		exe = "/usr/local/bin/appanel"
	}
	writeJSON(w, http.StatusOK, map[string]any{"backups": s.bk.List(),
		"schedule_command": "APPANEL_DATA=" + config.DefaultDataDir() + " " + exe + " backup-all --keep 7"})
}

func (s *Server) handleBackupSite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SiteID int64 `json:"site_id"`
	}
	if !decode(w, r, maxJSON, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	it, err := s.bk.BackupSite(ctx, in.SiteID)
	if err != nil {
		s.bkErr(w, err)
		return
	}
	logf("site backup %s made by %s", it.Name, userFrom(r).Username)
	writeJSON(w, http.StatusCreated, it)
}

func (s *Server) handleBackupDB(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Engine string `json:"engine"`
		Name   string `json:"name"`
	}
	if !decode(w, r, maxJSON, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	it, err := s.bk.BackupDB(ctx, in.Engine, in.Name)
	if err != nil {
		s.bkErr(w, err)
		return
	}
	logf("database backup %s made by %s", it.Name, userFrom(r).Username)
	writeJSON(w, http.StatusCreated, it)
}

func (s *Server) handleBackupDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.bk.Delete(r.PathValue("name")); err != nil {
		s.bkErr(w, err)
		return
	}
	logf("backup %s deleted by %s", r.PathValue("name"), userFrom(r).Username)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	logf("backup %s restore started by %s", r.PathValue("name"), userFrom(r).Username)
	if err := s.bk.Restore(ctx, r.PathValue("name")); err != nil {
		s.bkErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleBackupDownload(w http.ResponseWriter, r *http.Request) {
	p, err := s.bk.Path(r.PathValue("name"))
	if err != nil {
		s.bkErr(w, err)
		return
	}
	fh, err := os.Open(p)
	if err != nil {
		s.bkErr(w, backups.ErrNotFound)
		return
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		s.bkErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": st.Name()}))
	http.ServeContent(w, r, st.Name(), st.ModTime(), fh)
}
