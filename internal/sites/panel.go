package sites

import (
	"context"
	"errors"
	"fmt"

	"github.com/usmanawa2153-beep/appanel/internal/store"
)

var ErrNoResolve = errors.New("that domain does not point anywhere yet; create its DNS A record (see the DNS tab) and try again in a few minutes")

// PanelSite returns the site that fronts the panel, if one exists.
func (m *Manager) PanelSite() (store.Site, bool) {
	for _, s := range m.Store.ListSites() {
		if s.Panel {
			return s, true
		}
	}
	return store.Site{}, false
}

// AttachPanel publishes the panel at https://domain/ through Nginx: a proxy
// site to the panel's loopback port, a Let's Encrypt certificate and forced
// HTTPS. Nothing is left behind if a step fails. The panel's own listener is
// not touched here; the caller changes it only after this succeeded, so a
// failure can never lock the admin out.
func (m *Manager) AttachPanel(ctx context.Context, domain string, port int) (string, error) {
	d, err := NormalizeDomain(domain)
	if err != nil {
		return "", err
	}
	if _, ok := m.PanelSite(); ok {
		return "", errors.New("a panel domain is already attached; detach it first")
	}
	if addrs, err := m.LookupHost(ctx, d); err != nil || len(addrs) == 0 {
		return "", ErrNoResolve
	}
	site, err := m.Create(ctx, CreateInput{Domain: d, Type: store.SiteProxy, ProxyURL: fmt.Sprintf("http://127.0.0.1:%d", port)})
	if err != nil {
		return "", err
	}
	rollback := func() { _ = m.detachSite(ctx, site.ID) }
	if err := m.markPanel(ctx, site.ID, false); err != nil {
		rollback()
		return "", err
	}
	_, out, err := m.IssueSSL(ctx, site.ID)
	if err != nil {
		rollback()
		return out, err
	}
	if err := m.markPanel(ctx, site.ID, true); err != nil {
		rollback()
		return out, err
	}
	return out, nil
}

// markPanel sets the Panel flag and, when forceHTTPS, redirects HTTP to HTTPS.
func (m *Manager) markPanel(ctx context.Context, id int64, forceHTTPS bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, err := m.Store.SiteByID(id)
	if err != nil {
		return ErrNotFound
	}
	s := old
	s.Panel = true
	s.ForceHTTPS = forceHTTPS
	if err := m.Store.UpdateSite(s); err != nil {
		return err
	}
	if err := m.apply(ctx, s); err != nil {
		_ = m.Store.UpdateSite(old)
		return err
	}
	return nil
}

func (m *Manager) detachSite(ctx context.Context, id int64) error {
	m.mu.Lock()
	s, err := m.Store.SiteByID(id)
	if err == nil && s.Panel {
		s.Panel = false
		_ = m.Store.UpdateSite(s)
	}
	m.mu.Unlock()
	return m.Delete(ctx, id)
}

// DetachPanel removes the Nginx site that fronts the panel (the certificate
// stays on disk).
func (m *Manager) DetachPanel(ctx context.Context) error {
	s, ok := m.PanelSite()
	if !ok {
		return nil
	}
	return m.detachSite(ctx, s.ID)
}
