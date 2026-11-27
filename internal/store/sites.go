package store

import (
	"strings"
	"time"
)

// Site types.
const (
	SiteStatic = "static" // serve files from Root
	SiteProxy  = "proxy"  // reverse proxy to ProxyURL (Node, Python, Docker apps...)
	SitePHP    = "php"    // PHP-FPM via PHPSocket
)

// Site is one website served by Nginx. OwnerID is set from day one so
// per-user accounts can be added later without a migration.
type Site struct {
	ID         int64    `json:"id"`
	OwnerID    int64    `json:"owner_id"`
	Domain     string   `json:"domain"`
	Aliases    []string `json:"aliases"`
	Type       string   `json:"type"`
	Root       string   `json:"root,omitempty"`
	ProxyURL   string   `json:"proxy_url,omitempty"`
	PHPSocket  string   `json:"php_socket,omitempty"`
	Enabled    bool     `json:"enabled"`
	SSL        bool     `json:"ssl"`
	ForceHTTPS bool     `json:"force_https"`
	// CertName is the domain whose certificate folder this site uses, set only
	// when it shares a parent's wildcard certificate. Empty means its own.
	CertName  string    `json:"cert_name,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Names returns the domain followed by its aliases.
func (s Site) Names() []string { return append([]string{s.Domain}, s.Aliases...) }

// Settings are panel-wide options.
type Settings struct {
	ACMEEmail string `json:"acme_email,omitempty"`
	// CloudflareToken is never sent to the browser.
	CloudflareToken string `json:"cloudflare_token,omitempty"`
}

// Settings returns the panel settings.
func (s *Store) Settings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.d.Settings
}

// SetSettings replaces the panel settings.
func (s *Store) SetSettings(v Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.d.Settings
	s.d.Settings = v
	if err := s.save(); err != nil {
		s.d.Settings = old
		return err
	}
	return nil
}

// ListSites returns a copy of all sites.
func (s *Store) ListSites() []Site {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Site(nil), s.d.Sites...)
}

// SiteByID finds a site.
func (s *Store) SiteByID(id int64) (Site, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, x := range s.d.Sites {
		if x.ID == id {
			return x, nil
		}
	}
	return Site{}, ErrNotFound
}

func (s *Store) nameTakenLocked(name string, exceptID int64) bool {
	for _, x := range s.d.Sites {
		if x.ID == exceptID {
			continue
		}
		for _, n := range x.Names() {
			if strings.EqualFold(n, name) {
				return true
			}
		}
	}
	return false
}

// CreateSite adds a site; its domain and aliases must not be used by another site.
func (s *Store) CreateSite(x Site) (Site, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range x.Names() {
		if s.nameTakenLocked(n, 0) {
			return Site{}, ErrExists
		}
	}
	x.ID = s.d.NextID
	s.d.NextID++
	if x.CreatedAt.IsZero() {
		x.CreatedAt = time.Now().UTC()
	}
	s.d.Sites = append(s.d.Sites, x)
	if err := s.save(); err != nil {
		s.d.Sites = s.d.Sites[:len(s.d.Sites)-1]
		s.d.NextID--
		return Site{}, err
	}
	return x, nil
}

// UpdateSite replaces the site with the same ID.
func (s *Store) UpdateSite(x Site) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range x.Names() {
		if s.nameTakenLocked(n, x.ID) {
			return ErrExists
		}
	}
	for i := range s.d.Sites {
		if s.d.Sites[i].ID == x.ID {
			old := s.d.Sites[i]
			s.d.Sites[i] = x
			if err := s.save(); err != nil {
				s.d.Sites[i] = old
				return err
			}
			return nil
		}
	}
	return ErrNotFound
}

// DeleteSite removes a site.
func (s *Store) DeleteSite(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.d.Sites {
		if s.d.Sites[i].ID == id {
			old := append([]Site(nil), s.d.Sites...)
			s.d.Sites = append(s.d.Sites[:i], s.d.Sites[i+1:]...)
			if err := s.save(); err != nil {
				s.d.Sites = old
				return err
			}
			return nil
		}
	}
	return ErrNotFound
}
