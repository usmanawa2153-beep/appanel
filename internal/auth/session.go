package auth

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// Session is a signed-in browser session.
type Session struct {
	Token   string
	UserID  int64
	Expires time.Time
}

type ticket struct {
	userID   int64
	expires  time.Time
	attempts int
}

const (
	ticketTTL         = 5 * time.Minute
	ticketMaxAttempts = 5
)

// Sessions keeps sessions and short-lived 2FA tickets in memory. Restarting the
// panel signs everyone out, which is acceptable for a single-admin panel.
type Sessions struct {
	mu      sync.Mutex
	ttl     time.Duration
	byToken map[string]Session
	tickets map[string]*ticket
}

// NewSessions creates a store whose sessions live for ttl.
func NewSessions(ttl time.Duration) *Sessions {
	return &Sessions{ttl: ttl, byToken: map[string]Session{}, tickets: map[string]*ticket{}}
}

func newToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Sessions) sweep(now time.Time) {
	for k, v := range s.byToken {
		if now.After(v.Expires) {
			delete(s.byToken, k)
		}
	}
	for k, v := range s.tickets {
		if now.After(v.expires) {
			delete(s.tickets, k)
		}
	}
}

// Create starts a session for userID.
func (s *Sessions) Create(userID int64) Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.sweep(now)
	sess := Session{Token: newToken(), UserID: userID, Expires: now.Add(s.ttl)}
	s.byToken[sess.Token] = sess
	return sess
}

// Get returns a live session for token.
func (s *Sessions) Get(token string) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.byToken[token]
	if !ok || time.Now().After(sess.Expires) {
		delete(s.byToken, token)
		return Session{}, false
	}
	return sess, true
}

// Delete ends one session.
func (s *Sessions) Delete(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byToken, token)
}

// DeleteForUser ends all of a user's sessions except the one named by keep.
func (s *Sessions) DeleteForUser(userID int64, keep string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.byToken {
		if v.UserID == userID && k != keep {
			delete(s.byToken, k)
		}
	}
}

// NewTicket is issued after a correct password when 2FA is still pending.
func (s *Sessions) NewTicket(userID int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := newToken()
	s.tickets[t] = &ticket{userID: userID, expires: time.Now().Add(ticketTTL)}
	return t
}

// TicketUser returns the user a ticket belongs to.
func (s *Sessions) TicketUser(t string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tk, ok := s.tickets[t]
	if !ok || time.Now().After(tk.expires) {
		delete(s.tickets, t)
		return 0, false
	}
	return tk.userID, true
}

// TicketFail records a wrong code; the ticket dies after too many attempts.
func (s *Sessions) TicketFail(t string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tk, ok := s.tickets[t]; ok {
		tk.attempts++
		if tk.attempts >= ticketMaxAttempts {
			delete(s.tickets, t)
		}
	}
}

// TicketDone consumes a ticket after a successful 2FA check.
func (s *Sessions) TicketDone(t string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tickets, t)
}
