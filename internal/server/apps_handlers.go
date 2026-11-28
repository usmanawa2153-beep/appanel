package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
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

// ---- WordPress management ----

func wpID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeErr(w, http.StatusNotFound, "website not found")
		return 0, false
	}
	return id, true
}

func (s *Server) wpErr(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	if errors.Is(err, apps.ErrNotWordPress) {
		code = http.StatusNotFound
	}
	writeErr(w, code, err.Error())
}

// wpRead runs a read-only WP-CLI call.
func (s *Server) wpRead(fn func(ctx context.Context, m *apps.WP, id int64, r *http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := wpID(w, r)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		v, err := fn(ctx, s.wp.Manage(), id, r)
		if err != nil {
			s.wpErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	}
}

// wpWrite runs a changing call; one at a time because they share the site's files and the database.
func (s *Server) wpWrite(fn func(ctx context.Context, m *apps.WP, id int64, r *http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := wpID(w, r)
		if !ok {
			return
		}
		if !s.wpBusy.TryLock() {
			writeErr(w, http.StatusConflict, "another change is still running; wait for it to finish")
			return
		}
		defer s.wpBusy.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
		defer cancel()
		v, err := fn(ctx, s.wp.Manage(), id, r)
		if err != nil {
			s.wpErr(w, err)
			return
		}
		logf("wordpress %s %s on site %d by %s", r.Method, r.URL.Path, id, userFrom(r).Username)
		writeJSON(w, http.StatusOK, v)
	}
}

func okResult(out string) map[string]any { return map[string]any{"ok": true, "log": out} }

func (s *Server) registerWordPress(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/wp/{id}", s.authed(s.wpRead(func(ctx context.Context, m *apps.WP, id int64, _ *http.Request) (any, error) {
		return m.Overview(ctx, id)
	})))
	mux.HandleFunc("PUT /api/wp/{id}/settings", s.authed(s.wpWrite(func(ctx context.Context, m *apps.WP, id int64, r *http.Request) (any, error) {
		var in apps.SettingsInput
		if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxJSON)).Decode(&in); err != nil {
			return nil, errors.New("invalid JSON body")
		}
		return okResult(""), m.SetSettings(ctx, id, in)
	})))
	mux.HandleFunc("POST /api/wp/{id}/maintenance", s.authed(s.wpWrite(func(ctx context.Context, m *apps.WP, id int64, r *http.Request) (any, error) {
		var in struct {
			On bool `json:"on"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxJSON)).Decode(&in); err != nil {
			return nil, errors.New("invalid JSON body")
		}
		return okResult(""), m.Maintenance(ctx, id, in.On)
	})))
	mux.HandleFunc("POST /api/wp/{id}/core/update", s.authed(s.wpWrite(func(ctx context.Context, m *apps.WP, id int64, _ *http.Request) (any, error) {
		out, err := m.UpdateCore(ctx, id)
		return okResult(out), err
	})))
	mux.HandleFunc("POST /api/wp/{id}/cache/flush", s.authed(s.wpWrite(func(ctx context.Context, m *apps.WP, id int64, _ *http.Request) (any, error) {
		return okResult(""), m.FlushCache(ctx, id)
	})))
	mux.HandleFunc("GET /api/wp/{id}/users", s.authed(s.wpRead(func(ctx context.Context, m *apps.WP, id int64, _ *http.Request) (any, error) {
		u, err := m.Users(ctx, id)
		return map[string]any{"users": u}, err
	})))
	mux.HandleFunc("POST /api/wp/{id}/users/{uid}/password", s.authed(s.wpWrite(func(ctx context.Context, m *apps.WP, id int64, r *http.Request) (any, error) {
		uid, err := strconv.Atoi(r.PathValue("uid"))
		if err != nil {
			return nil, errors.New("unknown user")
		}
		var in struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxJSON)).Decode(&in); err != nil {
			return nil, errors.New("invalid JSON body")
		}
		pw, err := m.SetPassword(ctx, id, uid, in.Password)
		return map[string]any{"ok": true, "password": pw}, err
	})))

	for _, kind := range []string{"plugin", "theme"} {
		kind := kind
		base := "/api/wp/{id}/" + kind + "s"
		mux.HandleFunc("GET "+base, s.authed(s.wpRead(func(ctx context.Context, m *apps.WP, id int64, _ *http.Request) (any, error) {
			var items []apps.Item
			var err error
			if kind == "plugin" {
				items, err = m.Plugins(ctx, id)
			} else {
				items, err = m.Themes(ctx, id)
			}
			return map[string]any{"items": items}, err
		})))
		mux.HandleFunc("GET "+base+"/search", s.authed(s.wpRead(func(ctx context.Context, m *apps.WP, id int64, r *http.Request) (any, error) {
			var res []apps.Result
			var err error
			if kind == "plugin" {
				res, err = m.SearchPlugins(ctx, id, r.URL.Query().Get("q"))
			} else {
				res, err = m.SearchThemes(ctx, id, r.URL.Query().Get("q"))
			}
			return map[string]any{"results": res}, err
		})))
		mux.HandleFunc("POST "+base+"/install", s.authed(s.wpWrite(func(ctx context.Context, m *apps.WP, id int64, r *http.Request) (any, error) {
			var in struct {
				Slug     string `json:"slug"`
				Activate bool   `json:"activate"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxJSON)).Decode(&in); err != nil {
				return nil, errors.New("invalid JSON body")
			}
			out, err := m.Install(ctx, id, kind, in.Slug, in.Activate)
			return okResult(out), err
		})))
		mux.HandleFunc("POST "+base+"/update-all", s.authed(s.wpWrite(func(ctx context.Context, m *apps.WP, id int64, _ *http.Request) (any, error) {
			out, err := m.UpdateAll(ctx, id, kind)
			return okResult(out), err
		})))
		mux.HandleFunc("POST "+base+"/{slug}/{action}", s.authed(s.wpWrite(func(ctx context.Context, m *apps.WP, id int64, r *http.Request) (any, error) {
			out, err := m.Action(ctx, id, kind, r.PathValue("slug"), r.PathValue("action"))
			return okResult(out), err
		})))
	}
}
