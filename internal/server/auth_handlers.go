package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/usmanawa2153-beep/appanel/internal/auth"
	"github.com/usmanawa2153-beep/appanel/internal/store"
)

type ctxKey struct{}

func userFrom(r *http.Request) store.User { return r.Context().Value(ctxKey{}).(store.User) }

func tokenFrom(r *http.Request) string {
	if c, err := r.Cookie(cookieName); err == nil {
		return c.Value
	}
	return ""
}

// authed requires a valid session and, for state-changing methods, the CSRF
// header (a custom header cannot be sent cross-site without a CORS preflight,
// which this server never approves).
func (s *Server) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get(csrfHeader) != "1" {
			writeErr(w, http.StatusForbidden, "missing CSRF header")
			return
		}
		sess, ok := s.sess.Get(tokenFrom(r))
		if !ok {
			writeErr(w, http.StatusUnauthorized, "not signed in")
			return
		}
		u, err := s.st.UserByID(sess.UserID)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "not signed in")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
	}
}

func (s *Server) startSession(w http.ResponseWriter, u store.User) {
	sess := s.sess.Create(u.ID)
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    sess.Token,
		Path:     s.cfg.SecretPath + "/",
		Expires:  sess.Expires,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: "", Path: s.cfg.SecretPath + "/",
		MaxAge: -1, HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) throttled(w http.ResponseWriter, ip string) bool {
	if ok, wait := s.lim.Allowed(ip); !ok {
		w.Header().Set("Retry-After", itoa(int(wait.Seconds())+1))
		writeErr(w, http.StatusTooManyRequests, "too many failed attempts, try again later")
		return true
	}
	return false
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if s.throttled(w, ip) {
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, maxJSON, &in) {
		return
	}
	u, err := s.st.UserByName(strings.TrimSpace(in.Username))
	hash := s.dummy
	if err == nil {
		hash = u.PasswordHash
	}
	// Always verify, even for unknown users, so response time doesn't reveal
	// which usernames exist.
	if !auth.VerifyPassword(in.Password, hash) || err != nil {
		s.lim.Fail(ip)
		writeErr(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if u.TOTPEnabled {
		writeJSON(w, http.StatusOK, map[string]any{"need_2fa": true, "ticket": s.sess.NewTicket(u.ID)})
		return
	}
	s.lim.Reset(ip)
	s.startSession(w, u)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLogin2FA(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if s.throttled(w, ip) {
		return
	}
	var in struct {
		Ticket string `json:"ticket"`
		Code   string `json:"code"`
	}
	if !decode(w, r, maxJSON, &in) {
		return
	}
	uid, ok := s.sess.TicketUser(in.Ticket)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "login expired, sign in again")
		return
	}
	u, err := s.st.UserByID(uid)
	if err != nil || !u.TOTPEnabled || !auth.VerifyTOTP(u.TOTPSecret, in.Code, time.Now()) {
		s.sess.TicketFail(in.Ticket)
		s.lim.Fail(ip)
		writeErr(w, http.StatusUnauthorized, "invalid code")
		return
	}
	s.sess.TicketDone(in.Ticket)
	s.lim.Reset(ip)
	s.startSession(w, u)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.sess.Delete(tokenFrom(r))
	s.clearCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"username": u.Username, "role": u.Role, "totp_enabled": u.TOTPEnabled, "version": s.version,
	})
}

const minPasswordLen = 10

func (s *Server) handlePassword(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if s.throttled(w, clientIP(r)) {
		return
	}
	var in struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !decode(w, r, maxJSON, &in) {
		return
	}
	if !auth.VerifyPassword(in.Current, u.PasswordHash) {
		s.lim.Fail(clientIP(r))
		writeErr(w, http.StatusForbidden, "current password is wrong")
		return
	}
	if len(in.New) < minPasswordLen {
		writeErr(w, http.StatusBadRequest, "new password must be at least 10 characters")
		return
	}
	h, err := auth.HashPassword(in.New)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not hash password")
		return
	}
	u.PasswordHash = h
	if err := s.st.UpdateUser(u); err != nil {
		logf("update user: %v", err)
		writeErr(w, http.StatusInternalServerError, "could not save")
		return
	}
	s.sess.DeleteForUser(u.ID, tokenFrom(r)) // sign out other devices
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handle2FASetup(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if u.TOTPEnabled {
		writeErr(w, http.StatusConflict, "2FA is already enabled")
		return
	}
	u.TOTPSecret = auth.NewTOTPSecret()
	if err := s.st.UpdateUser(u); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"secret": u.TOTPSecret,
		"uri":    auth.TOTPURI("Appanel", u.Username, u.TOTPSecret),
	})
}

func (s *Server) handle2FAEnable(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var in struct {
		Code string `json:"code"`
	}
	if !decode(w, r, maxJSON, &in) {
		return
	}
	if u.TOTPSecret == "" || u.TOTPEnabled {
		writeErr(w, http.StatusConflict, "start 2FA setup first")
		return
	}
	if !auth.VerifyTOTP(u.TOTPSecret, in.Code, time.Now()) {
		writeErr(w, http.StatusBadRequest, "invalid code")
		return
	}
	u.TOTPEnabled = true
	if err := s.st.UpdateUser(u); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handle2FADisable(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if s.throttled(w, clientIP(r)) {
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, maxJSON, &in) {
		return
	}
	if !auth.VerifyPassword(in.Password, u.PasswordHash) {
		s.lim.Fail(clientIP(r))
		writeErr(w, http.StatusForbidden, "password is wrong")
		return
	}
	u.TOTPEnabled, u.TOTPSecret = false, ""
	if err := s.st.UpdateUser(u); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSystem(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.sys.Snapshot())
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
