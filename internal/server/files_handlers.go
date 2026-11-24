package server

import (
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"

	"github.com/usmanawa2153-beep/appanel/internal/files"
)

const maxUpload = 4 << 30 // 4 GiB per request

func (s *Server) fileErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		writeErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, fs.ErrPermission):
		writeErr(w, http.StatusForbidden, "permission denied")
	case errors.Is(err, fs.ErrExist):
		writeErr(w, http.StatusConflict, "already exists")
	case errors.Is(err, files.ErrOutside), errors.Is(err, files.ErrProtected):
		writeErr(w, http.StatusForbidden, err.Error())
	case errors.Is(err, files.ErrTooLarge):
		writeErr(w, http.StatusRequestEntityTooLarge, err.Error())
	case errors.Is(err, files.ErrBinary), errors.Is(err, files.ErrNotRegular),
		errors.Is(err, files.ErrNotDir), errors.Is(err, files.ErrBadName):
		writeErr(w, http.StatusBadRequest, err.Error())
	default:
		// OS errors embed server paths; log them, don't send them to the browser.
		logf("file operation failed: %v", err)
		writeErr(w, http.StatusInternalServerError, "operation failed")
	}
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" {
		p = "/"
	}
	dir, entries, err := s.fs.List(p)
	if err != nil {
		s.fileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": dir, "entries": entries})
}

func (s *Server) handleRead(w http.ResponseWriter, r *http.Request) {
	b, err := s.fs.ReadText(r.URL.Query().Get("path"))
	if err != nil {
		s.fileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": r.URL.Query().Get("path"), "content": string(b)})
}

func (s *Server) handleWrite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if !decode(w, r, files.MaxEditSize+(64<<10), &in) {
		return
	}
	if err := s.fs.WriteText(in.Path, []byte(in.Content)); err != nil {
		s.fileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMkdir(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
	}
	if !decode(w, r, maxJSON, &in) {
		return
	}
	if err := s.fs.Mkdir(in.Path); err != nil {
		s.fileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleRename(w http.ResponseWriter, r *http.Request) {
	var in struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if !decode(w, r, maxJSON, &in) {
		return
	}
	if err := s.fs.Rename(in.From, in.To); err != nil {
		s.fileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.fs.Delete(r.URL.Query().Get("path")); err != nil {
		s.fileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleUpload streams a multipart body straight to disk (no buffering of
// whole files in memory). The destination directory comes from ?dir=.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		dir = "/"
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "expected multipart upload")
		return
	}
	var saved []string
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeErr(w, http.StatusBadRequest, "upload interrupted")
			return
		}
		name := part.FileName()
		if name == "" {
			continue
		}
		p, err := s.fs.SaveUpload(dir, filepath.Base(name), part)
		if err != nil {
			s.fileErr(w, err)
			return
		}
		saved = append(saved, p)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "saved": saved})
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	fh, st, err := s.fs.OpenDownload(r.URL.Query().Get("path"))
	if err != nil {
		s.fileErr(w, err)
		return
	}
	defer fh.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": st.Name()}))
	http.ServeContent(w, r, st.Name(), st.ModTime(), fh)
}
