package apps

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/usmanawa2153-beep/appanel/internal/store"
)

type wpRunner struct {
	calls   []string
	replies map[string]string // substring of the command line -> output
	fail    string            // substring that makes the call fail
}

func (r *wpRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	line := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, line)
	if r.fail != "" && strings.Contains(line, r.fail) {
		return "Warning: noise\nError: Could not do it.", errors.New("exit 1")
	}
	for k, v := range r.replies {
		if strings.Contains(line, k) {
			return v, nil
		}
	}
	return "", nil
}
func (r *wpRunner) RunInput(ctx context.Context, _ string, n string, a ...string) (string, error) {
	return r.Run(ctx, n, a...)
}

func wpSetup(t *testing.T) (*WP, *wpRunner, store.Site) {
	t.Helper()
	e := setup(t)
	site, err := e.st.CreateSite(store.Site{Domain: "blog.example.com", Type: store.SitePHP, Root: "/var/www/blog.example.com/public_html", App: "wordpress", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	r := &wpRunner{replies: map[string]string{}}
	e.w.Runner = r
	return e.w.Manage(), r, site
}

func TestOnlyWordPressSites(t *testing.T) {
	m, r, _ := wpSetup(t)
	plain, _ := m.Store.CreateSite(store.Site{Domain: "plain.example.com", Type: store.SitePHP, Root: "/var/www/p", Enabled: true})
	if _, err := m.Plugins(context.Background(), plain.ID); !errors.Is(err, ErrNotWordPress) {
		t.Errorf("%v", err)
	}
	if _, err := m.Plugins(context.Background(), 9999); !errors.Is(err, ErrNotWordPress) {
		t.Errorf("%v", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("commands ran for a non-WordPress site: %v", r.calls)
	}
}

func TestCommandShapeRunsAsWebUserInSiteFolder(t *testing.T) {
	m, r, s := wpSetup(t)
	r.replies["plugin list"] = `[{"name":"akismet","title":"Akismet","status":"active","version":"5.3","update":"available","update_version":"5.4"}]`
	items, err := m.Plugins(context.Background(), s.ID)
	if err != nil || len(items) != 1 || items[0].Update != "available" {
		t.Fatalf("%v %+v", err, items)
	}
	want := "runuser -u www-data -- php -d memory_limit=512M " + m.CLIPath + " --path=" + s.Root + " --no-color plugin list"
	if !strings.HasPrefix(r.calls[0], want) {
		t.Errorf("call = %s", r.calls[0])
	}
}

func TestPHPNoiseBeforeJSON(t *testing.T) {
	m, r, s := wpSetup(t)
	r.replies["user list"] = "PHP Notice:  something in /x.php\n" + `[{"ID":1,"user_login":"siteadmin","user_email":"a@b.co","display_name":"Admin","roles":"administrator"}]`
	u, err := m.Users(context.Background(), s.ID)
	if err != nil || len(u) != 1 || u[0].Login != "siteadmin" {
		t.Fatalf("%v %+v", err, u)
	}
}

func TestSetPassword(t *testing.T) {
	m, r, s := wpSetup(t)
	r.replies["user list"] = `[{"ID":1,"user_login":"siteadmin","user_email":"a@b.co","display_name":"A","roles":"administrator"}]`
	ctx := context.Background()
	pw, err := m.SetPassword(ctx, s.ID, 1, "")
	if err != nil || len(pw) != 20 {
		t.Fatalf("generated: %v %q", err, pw)
	}
	if !strings.Contains(strings.Join(r.calls, "\n"), "user update 1 --user_pass="+pw) {
		t.Errorf("calls: %v", r.calls)
	}
	// Own password.
	if got, err := m.SetPassword(ctx, s.ID, 1, "my-long-passphrase-9"); err != nil || got != "my-long-passphrase-9" {
		t.Errorf("%v %q", err, got)
	}
	n := len(r.calls)
	for _, bad := range []string{"short", "has\nnewline-in-it", strings.Repeat("a", 129)} {
		if _, err := m.SetPassword(ctx, s.ID, 1, bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if _, err := m.SetPassword(ctx, s.ID, 77, ""); err == nil {
		t.Error("unknown user accepted")
	}
	if _, err := m.SetPassword(ctx, s.ID, 0, ""); err == nil {
		t.Error("user 0 accepted")
	}
	for _, c := range r.calls[n:] {
		if strings.Contains(c, "user update") {
			t.Errorf("update ran for a refused request: %s", c)
		}
	}
	// A failing command never echoes the password back.
	r.fail = "user update"
	_, err = m.SetPassword(ctx, s.ID, 1, "another-long-passphrase")
	if err == nil || strings.Contains(err.Error(), "another-long-passphrase") {
		t.Errorf("error %v", err)
	}
}

func TestInstallAndActionsValidateSlugs(t *testing.T) {
	m, r, s := wpSetup(t)
	ctx := context.Background()
	if _, err := m.Install(ctx, s.ID, "plugin", "wordpress-seo", true); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(r.calls[len(r.calls)-1], "plugin install wordpress-seo --activate") {
		t.Errorf("%v", r.calls)
	}
	n := len(r.calls)
	for _, bad := range []string{"--url=http://evil", "../x", "Has Space", "a;b", "-x", "", "UPPER", strings.Repeat("a", 101)} {
		if _, err := m.Install(ctx, s.ID, "plugin", bad, false); err == nil {
			t.Errorf("install accepted %q", bad)
		}
		if _, err := m.Action(ctx, s.ID, "plugin", bad, "activate"); err == nil {
			t.Errorf("action accepted %q", bad)
		}
	}
	if _, err := m.Install(ctx, s.ID, "core", "x", false); err == nil {
		t.Error("kind core accepted")
	}
	if _, err := m.Action(ctx, s.ID, "theme", "twentytwentyfour", "deactivate"); err == nil {
		t.Error("theme deactivate accepted")
	}
	if _, err := m.Action(ctx, s.ID, "plugin", "akismet", "eval"); err == nil {
		t.Error("arbitrary action accepted")
	}
	if len(r.calls) != n {
		t.Errorf("commands ran for refused input: %v", r.calls[n:])
	}
	// Deleting a plugin deactivates it first.
	if _, err := m.Action(ctx, s.ID, "plugin", "akismet", "delete"); err != nil {
		t.Fatal(err)
	}
	tail := r.calls[len(r.calls)-2:]
	if !strings.HasSuffix(tail[0], "plugin deactivate akismet") || !strings.HasSuffix(tail[1], "plugin delete akismet") {
		t.Errorf("%v", tail)
	}
}

func TestSearchTermValidation(t *testing.T) {
	m, r, s := wpSetup(t)
	r.replies["plugin search"] = `[{"name":"Yoast SEO","slug":"wordpress-seo","version":"22","rating":92,"active_installs":5000000,"short_description":"SEO"}]`
	got, err := m.SearchPlugins(context.Background(), s.ID, "seo")
	if err != nil || len(got) != 1 || got[0].Slug != "wordpress-seo" {
		t.Fatalf("%v %+v", err, got)
	}
	n := len(r.calls)
	for _, bad := range []string{"", "--format=table", "a;b", "$(id)", strings.Repeat("a", 61)} {
		if _, err := m.SearchPlugins(context.Background(), s.ID, bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if len(r.calls) != n {
		t.Error("search ran for refused input")
	}
}

func TestSettings(t *testing.T) {
	m, r, s := wpSetup(t)
	ctx := context.Background()
	title, vis, dbg, perma := "New title", false, true, "/%postname%/"
	if err := m.SetSettings(ctx, s.ID, SettingsInput{Title: &title, SearchVisible: &vis, Debug: &dbg, Permalinks: &perma}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(r.calls, "\n")
	for _, want := range []string{"option update blogname New title", "option update blog_public 0", "config set WP_DEBUG true --raw", "rewrite structure /%postname%/ --hard"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in\n%s", want, joined)
		}
	}
	r.calls = nil
	badTitle, badMail, badPerma := "", "nope", "/%evil%/"
	for _, in := range []SettingsInput{{Title: &badTitle}, {AdminEmail: &badMail}, {Permalinks: &badPerma}} {
		if err := m.SetSettings(ctx, s.ID, in); err == nil {
			t.Errorf("accepted %+v", in)
		}
	}
	if len(r.calls) != 0 {
		t.Errorf("commands ran for refused settings: %v", r.calls)
	}
}

func TestOverview(t *testing.T) {
	m, r, s := wpSetup(t)
	r.replies["core version"] = "6.7.1\n"
	r.replies["core check-update"] = "6.8\n"
	r.replies["option get blogname"] = "My Blog\n"
	r.replies["option get blog_public"] = "0\n"
	r.replies["maintenance-mode status"] = "Maintenance mode is active.\n"
	r.replies["config get WP_DEBUG"] = "1\n"
	r.replies["plugin list"] = "3\n"
	o, err := m.Overview(context.Background(), s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if o.Version != "6.7.1" || o.Update != "6.8" || o.Title != "My Blog" || o.SearchVisible || !o.Maintenance || !o.Debug || o.PluginUpdates != 3 {
		t.Errorf("%+v", o)
	}
	r.replies["maintenance-mode status"] = "Maintenance mode is not active.\n"
	if o, _ = m.Overview(context.Background(), s.ID); o.Maintenance {
		t.Error("'not active' read as active")
	}
}

func TestErrorIsTheWPCLIErrorLine(t *testing.T) {
	m, r, s := wpSetup(t)
	r.fail = "plugin install"
	_, err := m.Install(context.Background(), s.ID, "plugin", "nope", false)
	if err == nil || err.Error() != "Could not do it." {
		t.Errorf("%v", err)
	}
}
