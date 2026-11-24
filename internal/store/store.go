// Package store persists panel data. V1 uses a single JSON file behind a small
// API so it can be swapped for SQLite later without touching callers.
package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
)

// User is a panel account. Role is "admin" today; the field exists so
// multi-user (cPanel-style accounts) can be added without a data migration.
type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	Role         string    `json:"role"`
	PasswordHash string    `json:"password_hash"`
	TOTPSecret   string    `json:"totp_secret,omitempty"`
	TOTPEnabled  bool      `json:"totp_enabled"`
	CreatedAt    time.Time `json:"created_at"`
}

type data struct {
	NextID int64  `json:"next_id"`
	Users  []User `json:"users"`
}

// Store is a concurrency-safe JSON-file store.
type Store struct {
	mu   sync.RWMutex
	path string
	d    data
}

// Open loads (or initialises) the store in dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dir, "appanel.json"), d: data{NextID: 1}}
	b, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return s, nil
	case err != nil:
		return nil, err
	}
	if err := json.Unmarshal(b, &s.d); err != nil {
		return nil, err
	}
	if s.d.NextID < 1 {
		s.d.NextID = 1
	}
	return s, nil
}

func (s *Store) save() error {
	b, err := json.MarshalIndent(s.d, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".store-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

// CountUsers returns the number of accounts.
func (s *Store) CountUsers() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.d.Users)
}

// UserByName finds a user, case-insensitively.
func (s *Store) UserByName(name string) (User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.d.Users {
		if strings.EqualFold(u.Username, name) {
			return u, nil
		}
	}
	return User{}, ErrNotFound
}

// UserByID finds a user by id.
func (s *Store) UserByID(id int64) (User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.d.Users {
		if u.ID == id {
			return u, nil
		}
	}
	return User{}, ErrNotFound
}

// CreateUser adds a user and returns it with its assigned id.
func (s *Store) CreateUser(u User) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.d.Users {
		if strings.EqualFold(e.Username, u.Username) {
			return User{}, ErrExists
		}
	}
	u.ID = s.d.NextID
	s.d.NextID++
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}
	s.d.Users = append(s.d.Users, u)
	if err := s.save(); err != nil {
		s.d.Users = s.d.Users[:len(s.d.Users)-1]
		s.d.NextID--
		return User{}, err
	}
	return u, nil
}

// UpdateUser replaces the stored user with the same id.
func (s *Store) UpdateUser(u User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.d.Users {
		if s.d.Users[i].ID == u.ID {
			old := s.d.Users[i]
			s.d.Users[i] = u
			if err := s.save(); err != nil {
				s.d.Users[i] = old
				return err
			}
			return nil
		}
	}
	return ErrNotFound
}
