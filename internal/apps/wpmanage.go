package apps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/usmanawa2153-beep/appanel/internal/execx"
	"github.com/usmanawa2153-beep/appanel/internal/store"
)

var (
	ErrNotWordPress = errors.New("this website does not have a WordPress installed by Appanel")
	slugRe          = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,99}$`)
	termRe          = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} ._-]{0,59}$`)
)

// permalinkStructures is the set offered in the UI; anything else is refused.
var permalinkStructures = map[string]bool{
	"": true, "/%postname%/": true, "/%year%/%monthnum%/%day%/%postname%/": true,
	"/%year%/%monthnum%/%postname%/": true, "/archives/%post_id%": true,
}

// WP drives WP-CLI for WordPress sites installed by Appanel. Every value that
// reaches the command line is validated first (slugs and ids by pattern,
// choices from fixed lists) and nothing can begin with "-", so no input can be
// read as a flag.
type WP struct{ *WordPress }

// Manage returns the manager that shares the installer's settings.
func (w *WordPress) Manage() *WP { return &WP{w} }

func (m *WP) site(id int64) (store.Site, error) {
	s, err := m.Store.SiteByID(id)
	if err != nil || s.App != "wordpress" {
		return store.Site{}, ErrNotWordPress
	}
	return s, nil
}

// cli runs `wp <args>` as www-data against the site's folder.
func (m *WP) cli(ctx context.Context, s store.Site, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	full := append([]string{"-u", "www-data", "--", "php", "-d", "memory_limit=512M", m.CLIPath, "--path=" + s.Root, "--no-color"}, args...)
	out, err := m.Runner.Run(ctx, "runuser", full...)
	if err != nil {
		return out, errors.New(cleanErr(out))
	}
	return out, nil
}

// cleanErr keeps the part of WP-CLI's output a person can act on.
func cleanErr(out string) string {
	out = strings.TrimSpace(out)
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "Error:") {
			return strings.TrimSpace(strings.TrimPrefix(l, "Error:"))
		}
	}
	if out == "" {
		return "WP-CLI failed"
	}
	return execx.Tail(out, 300)
}

func parseJSON(out string, v any) error {
	// WP-CLI may print PHP notices before the JSON; start at the first bracket.
	i := strings.IndexAny(out, "[{")
	if i < 0 {
		return errors.New("unexpected output from WP-CLI")
	}
	return json.Unmarshal([]byte(out[i:]), v)
}

func (m *WP) option(ctx context.Context, s store.Site, name string) string {
	out, err := m.cli(ctx, s, "option", "get", name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// ---------- overview and settings ----------

// Overview is the top of the manager page.
type Overview struct {
	Version       string `json:"version"`
	Update        string `json:"update,omitempty"`
	Title         string `json:"title"`
	Tagline       string `json:"tagline"`
	URL           string `json:"url"`
	AdminEmail    string `json:"admin_email"`
	SearchVisible bool   `json:"search_visible"`
	Maintenance   bool   `json:"maintenance"`
	Debug         bool   `json:"debug"`
	Permalinks    string `json:"permalinks"`
	PluginUpdates int    `json:"plugin_updates"`
	ThemeUpdates  int    `json:"theme_updates"`
}

func (m *WP) Overview(ctx context.Context, id int64) (Overview, error) {
	s, err := m.site(id)
	if err != nil {
		return Overview{}, err
	}
	var o Overview
	out, err := m.cli(ctx, s, "core", "version")
	if err != nil {
		return o, err
	}
	o.Version = strings.TrimSpace(out)
	if out, err := m.cli(ctx, s, "core", "check-update", "--field=version"); err == nil {
		if f := strings.Fields(out); len(f) > 0 && regexp.MustCompile(`^\d+(\.\d+)*$`).MatchString(f[0]) {
			o.Update = f[0]
		}
	}
	o.Title = m.option(ctx, s, "blogname")
	o.Tagline = m.option(ctx, s, "blogdescription")
	o.URL = m.option(ctx, s, "home")
	o.AdminEmail = m.option(ctx, s, "admin_email")
	o.SearchVisible = m.option(ctx, s, "blog_public") != "0"
	o.Permalinks = m.option(ctx, s, "permalink_structure")
	if out, err := m.cli(ctx, s, "maintenance-mode", "status"); err == nil {
		o.Maintenance = strings.Contains(out, "is active") && !strings.Contains(out, "not active")
	}
	if out, err := m.cli(ctx, s, "config", "get", "WP_DEBUG"); err == nil {
		o.Debug = strings.TrimSpace(out) == "1" || strings.EqualFold(strings.TrimSpace(out), "true")
	}
	o.PluginUpdates = m.count(ctx, s, "plugin")
	o.ThemeUpdates = m.count(ctx, s, "theme")
	return o, nil
}

func (m *WP) count(ctx context.Context, s store.Site, kind string) int {
	out, err := m.cli(ctx, s, kind, "list", "--update=available", "--format=count")
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n
}

// SettingsInput holds optional changes; nil fields are left alone.
type SettingsInput struct {
	Title         *string `json:"title"`
	Tagline       *string `json:"tagline"`
	AdminEmail    *string `json:"admin_email"`
	SearchVisible *bool   `json:"search_visible"`
	Permalinks    *string `json:"permalinks"`
	Debug         *bool   `json:"debug"`
}

func plainText(v string, max int) error {
	if len(v) > max {
		return fmt.Errorf("too long (%d characters max)", max)
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return errors.New("must not contain control characters")
		}
	}
	return nil
}

// SetSettings applies the changes, validating everything first.
func (m *WP) SetSettings(ctx context.Context, id int64, in SettingsInput) error {
	s, err := m.site(id)
	if err != nil {
		return err
	}
	if in.Title != nil {
		if t := strings.TrimSpace(*in.Title); t == "" || plainText(t, 100) != nil {
			return errors.New("site title is required (100 characters max, no control characters)")
		}
	}
	if in.Tagline != nil {
		if err := plainText(*in.Tagline, 200); err != nil {
			return fmt.Errorf("tagline: %w", err)
		}
	}
	if in.AdminEmail != nil && !emailRe.MatchString(strings.TrimSpace(*in.AdminEmail)) {
		return errors.New("admin email does not look valid")
	}
	if in.Permalinks != nil && !permalinkStructures[*in.Permalinks] {
		return errors.New("unknown permalink style")
	}
	set := func(name, value string) error {
		_, err := m.cli(ctx, s, "option", "update", name, value)
		return err
	}
	if in.Title != nil {
		if err := set("blogname", strings.TrimSpace(*in.Title)); err != nil {
			return err
		}
	}
	if in.Tagline != nil {
		if err := set("blogdescription", *in.Tagline); err != nil {
			return err
		}
	}
	if in.AdminEmail != nil {
		if err := set("admin_email", strings.TrimSpace(*in.AdminEmail)); err != nil {
			return err
		}
	}
	if in.SearchVisible != nil {
		v := "0"
		if *in.SearchVisible {
			v = "1"
		}
		if err := set("blog_public", v); err != nil {
			return err
		}
	}
	if in.Permalinks != nil {
		if _, err := m.cli(ctx, s, "rewrite", "structure", *in.Permalinks, "--hard"); err != nil {
			return err
		}
	}
	if in.Debug != nil {
		v := "false"
		if *in.Debug {
			v = "true"
		}
		if _, err := m.cli(ctx, s, "config", "set", "WP_DEBUG", v, "--raw"); err != nil {
			return err
		}
	}
	return nil
}

// Maintenance switches maintenance mode.
func (m *WP) Maintenance(ctx context.Context, id int64, on bool) error {
	s, err := m.site(id)
	if err != nil {
		return err
	}
	a := "deactivate"
	if on {
		a = "activate"
	}
	_, err = m.cli(ctx, s, "maintenance-mode", a)
	return err
}

// UpdateCore updates WordPress itself and its database.
func (m *WP) UpdateCore(ctx context.Context, id int64) (string, error) {
	s, err := m.site(id)
	if err != nil {
		return "", err
	}
	out, err := m.cli(ctx, s, "core", "update")
	if err != nil {
		return out, err
	}
	out2, err := m.cli(ctx, s, "core", "update-db")
	return strings.TrimSpace(out + "\n" + out2), err
}

// FlushCache clears the object cache and rewrite rules.
func (m *WP) FlushCache(ctx context.Context, id int64) error {
	s, err := m.site(id)
	if err != nil {
		return err
	}
	if _, err := m.cli(ctx, s, "cache", "flush"); err != nil {
		return err
	}
	_, err = m.cli(ctx, s, "rewrite", "flush", "--hard")
	return err
}

// ---------- users ----------

type User struct {
	ID    int    `json:"ID"`
	Login string `json:"user_login"`
	Email string `json:"user_email"`
	Name  string `json:"display_name"`
	Roles string `json:"roles"`
}

func (m *WP) Users(ctx context.Context, id int64) ([]User, error) {
	s, err := m.site(id)
	if err != nil {
		return nil, err
	}
	out, err := m.cli(ctx, s, "user", "list", "--format=json", "--fields=ID,user_login,user_email,display_name,roles")
	if err != nil {
		return nil, err
	}
	users := []User{}
	if err := parseJSON(out, &users); err != nil {
		return nil, err
	}
	return users, nil
}

// SetPassword sets a user's password. An empty password generates a strong
// one, which is returned (and never stored).
func (m *WP) SetPassword(ctx context.Context, id int64, userID int, password string) (string, error) {
	s, err := m.site(id)
	if err != nil {
		return "", err
	}
	if userID < 1 {
		return "", errors.New("unknown user")
	}
	if password == "" {
		if password, err = randString("abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789", 20); err != nil {
			return "", err
		}
	}
	if len(password) < 10 || len(password) > 128 || strings.HasPrefix(password, "-") {
		return "", errors.New("password must be 10-128 characters and must not start with \"-\"")
	}
	for _, r := range password {
		if r < 0x20 || r == 0x7f {
			return "", errors.New("password must not contain control characters")
		}
	}
	// The user is checked to exist first, so a typo cannot hit another account.
	users, err := m.Users(ctx, id)
	if err != nil {
		return "", err
	}
	found := false
	for _, u := range users {
		found = found || u.ID == userID
	}
	if !found {
		return "", errors.New("that user does not exist")
	}
	if _, err := m.cli(ctx, s, "user", "update", strconv.Itoa(userID), "--user_pass="+password); err != nil {
		return "", errors.New(strings.ReplaceAll(err.Error(), password, "***"))
	}
	return password, nil
}

// ---------- plugins and themes ----------

type Item struct {
	Name          string `json:"name"`
	Title         string `json:"title"`
	Status        string `json:"status"`
	Version       string `json:"version"`
	Update        string `json:"update"`
	UpdateVersion string `json:"update_version"`
}

func (m *WP) list(ctx context.Context, id int64, kind string) ([]Item, error) {
	s, err := m.site(id)
	if err != nil {
		return nil, err
	}
	out, err := m.cli(ctx, s, kind, "list", "--format=json", "--fields=name,title,status,version,update,update_version")
	if err != nil {
		return nil, err
	}
	items := []Item{}
	if err := parseJSON(out, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (m *WP) Plugins(ctx context.Context, id int64) ([]Item, error) { return m.list(ctx, id, "plugin") }
func (m *WP) Themes(ctx context.Context, id int64) ([]Item, error)  { return m.list(ctx, id, "theme") }

// Result is one hit from the WordPress.org directory.
type Result struct {
	Name           string  `json:"name"`
	Slug           string  `json:"slug"`
	Version        string  `json:"version"`
	Rating         float64 `json:"rating"`
	ActiveInstalls int     `json:"active_installs"`
	Description    string  `json:"short_description"`
}

func (m *WP) search(ctx context.Context, id int64, kind, q string) ([]Result, error) {
	s, err := m.site(id)
	if err != nil {
		return nil, err
	}
	q = strings.TrimSpace(q)
	if !termRe.MatchString(q) {
		return nil, errors.New("type a search word (letters, digits, spaces, . _ -)")
	}
	out, err := m.cli(ctx, s, kind, "search", q, "--per-page=12", "--format=json", "--fields=name,slug,version,rating,active_installs,short_description")
	if err != nil {
		return nil, err
	}
	res := []Result{}
	if err := parseJSON(out, &res); err != nil {
		return nil, err
	}
	return res, nil
}

func (m *WP) SearchPlugins(ctx context.Context, id int64, q string) ([]Result, error) {
	return m.search(ctx, id, "plugin", q)
}
func (m *WP) SearchThemes(ctx context.Context, id int64, q string) ([]Result, error) {
	return m.search(ctx, id, "theme", q)
}

func checkSlug(slug string) error {
	if !slugRe.MatchString(slug) {
		return errors.New("invalid name")
	}
	return nil
}

// Install installs a plugin or theme from the WordPress.org directory.
func (m *WP) Install(ctx context.Context, id int64, kind, slug string, activate bool) (string, error) {
	s, err := m.site(id)
	if err != nil {
		return "", err
	}
	if kind != "plugin" && kind != "theme" {
		return "", errors.New("unknown kind")
	}
	if err := checkSlug(slug); err != nil {
		return "", err
	}
	args := []string{kind, "install", slug}
	if activate {
		args = append(args, "--activate")
	}
	return m.cli(ctx, s, args...)
}

// Action runs activate, deactivate, update or delete on a plugin or theme.
func (m *WP) Action(ctx context.Context, id int64, kind, slug, action string) (string, error) {
	s, err := m.site(id)
	if err != nil {
		return "", err
	}
	allowed := map[string]map[string]bool{
		"plugin": {"activate": true, "deactivate": true, "update": true, "delete": true},
		"theme":  {"activate": true, "update": true, "delete": true},
	}
	if !allowed[kind][action] {
		return "", errors.New("that action is not available")
	}
	if err := checkSlug(slug); err != nil {
		return "", err
	}
	args := []string{kind, action, slug}
	if action == "delete" && kind == "plugin" {
		// An active plugin is switched off first, so delete never leaves it half-loaded.
		_, _ = m.cli(ctx, s, "plugin", "deactivate", slug)
	}
	return m.cli(ctx, s, args...)
}

// UpdateAll updates every plugin or theme that has an update.
func (m *WP) UpdateAll(ctx context.Context, id int64, kind string) (string, error) {
	s, err := m.site(id)
	if err != nil {
		return "", err
	}
	if kind != "plugin" && kind != "theme" {
		return "", errors.New("unknown kind")
	}
	return m.cli(ctx, s, kind, "update", "--all")
}
