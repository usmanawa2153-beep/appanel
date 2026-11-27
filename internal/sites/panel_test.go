package sites

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/usmanawa2153-beep/appanel/internal/store"
)

func panelMgr(t *testing.T) (*Manager, *fakeRunner) {
	m, fr, _ := newMgr(t)
	m.LookupHost = func(context.Context, string) ([]string, error) { return []string{"203.0.113.5"}, nil }
	m.Store.SetSettings(store.Settings{ACMEEmail: "a@example.com"})
	return m, fr
}

func TestAttachPanel(t *testing.T) {
	m, _ := panelMgr(t)
	ctx := context.Background()
	if _, err := m.AttachPanel(ctx, "panel.example.com", 23456); err != nil {
		t.Fatal(err)
	}
	s, ok := m.PanelSite()
	if !ok || !s.Panel || !s.SSL || !s.ForceHTTPS || s.ProxyURL != "http://127.0.0.1:23456" {
		t.Fatalf("%+v", s)
	}
	conf, _ := os.ReadFile(m.availPath("panel.example.com"))
	for _, want := range []string{"proxy_pass http://127.0.0.1:23456", "client_max_body_size 0", "proxy_read_timeout 1d", "proxy_set_header X-Real-IP $remote_addr", "return 301 https://"} {
		if !strings.Contains(string(conf), want) {
			t.Errorf("vhost lacks %q:\n%s", want, conf)
		}
	}
	// The panel's own site cannot be edited or removed from the Websites page.
	f := true
	if _, err := m.Update(ctx, s.ID, UpdateInput{Enabled: &f}); !errors.Is(err, ErrPanelSite) {
		t.Errorf("update: %v", err)
	}
	if err := m.Delete(ctx, s.ID); !errors.Is(err, ErrPanelSite) {
		t.Errorf("delete: %v", err)
	}
	if _, err := m.AttachPanel(ctx, "other.example.com", 23456); err == nil {
		t.Error("second panel domain accepted")
	}
	if err := m.DetachPanel(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.PanelSite(); ok || len(m.Store.ListSites()) != 0 {
		t.Error("panel site not removed")
	}
	if _, err := os.Stat(m.availPath("panel.example.com")); err == nil {
		t.Error("nginx config left behind")
	}
}

func TestAttachPanelRollsBackOnFailure(t *testing.T) {
	m, fr := panelMgr(t)
	fr.failCB = true
	if _, err := m.AttachPanel(context.Background(), "panel.example.com", 23456); err == nil {
		t.Fatal("expected certbot failure")
	}
	if n := len(m.Store.ListSites()); n != 0 {
		t.Errorf("%d site(s) left behind", n)
	}
	if _, err := os.Stat(m.availPath("panel.example.com")); err == nil {
		t.Error("nginx config left behind")
	}
}

func TestAttachPanelNeedsDNS(t *testing.T) {
	m, fr := panelMgr(t)
	m.LookupHost = func(context.Context, string) ([]string, error) { return nil, errors.New("no such host") }
	if _, err := m.AttachPanel(context.Background(), "panel.example.com", 23456); !errors.Is(err, ErrNoResolve) {
		t.Errorf("%v", err)
	}
	if fr.ran("certbot") || len(m.Store.ListSites()) != 0 {
		t.Error("work done before DNS resolved")
	}
}
