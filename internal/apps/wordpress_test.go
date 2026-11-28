package apps

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/usmanawa2153-beep/appanel/internal/databases"
	"github.com/usmanawa2153-beep/appanel/internal/sites"
	"github.com/usmanawa2153-beep/appanel/internal/store"
)

type runner struct {
	calls []string
	stdin []string
	fail  map[string]bool // command-name prefix -> fail
}

func (r *runner) Run(_ context.Context, name string, args ...string) (string, error) {
	line := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, line)
	for p := range r.fail {
		if strings.HasPrefix(line, p) {
			return "boom", errors.New("exit 1")
		}
	}
	return "", nil
}
func (r *runner) RunInput(ctx context.Context, in, name string, args ...string) (string, error) {
	r.stdin = append(r.stdin, in)
	if strings.Contains(in, "SCHEMATA") { // a database listing: report what was created so far
		var out string
		for _, prev := range r.stdin {
			if m := regexp.MustCompile("CREATE DATABASE `([a-z0-9_]+)`").FindStringSubmatch(prev); m != nil && !strings.Contains(strings.Join(r.stdin, "\n"), "DROP DATABASE IF EXISTS `"+m[1]+"`") {
				out += m[1] + "\t0\n"
			}
		}
		return out, nil
	}
	return r.Run(ctx, name, args...)
}

func tarball(t *testing.T, files map[string]string, extra ...tar.Header) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "wordpress/", Typeflag: tar.TypeDir, Mode: 0o755})
	for n, c := range files {
		tw.WriteHeader(&tar.Header{Name: "wordpress/" + n, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(c))})
		tw.Write([]byte(c))
	}
	for _, h := range extra {
		h := h
		tw.WriteHeader(&h)
		tw.Write(make([]byte, h.Size))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

type env struct {
	w       *WordPress
	r       *runner
	st      *store.Store
	root    string
	archive []byte
	sum     string
}

func setup(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	r := &runner{fail: map[string]bool{}}
	sm := sites.NewManager(st)
	sm.Runner = r
	sm.NginxDir, sm.WebRoot, sm.ACMERoot = filepath.Join(dir, "nginx"), filepath.Join(dir, "www"), filepath.Join(dir, "www", "_acme")
	sm.LetsEncryptDir, sm.LogDir = filepath.Join(dir, "le"), filepath.Join(dir, "log")
	sm.LookPath = func(string) (string, error) { return "/usr/bin/x", nil }
	dbm := &databases.Manager{Runner: r, LookPath: func(string) (string, error) { return "/usr/bin/x", nil }}
	cli := filepath.Join(dir, "wp")
	os.WriteFile(cli, []byte("phar"), 0o755)
	e := &env{r: r, st: st, root: filepath.Join(dir, "www")}
	e.archive = tarball(t, map[string]string{"index.php": "<?php //wp", "wp-settings.php": "x", "wp-content/index.php": "silence"})
	e.setSum()
	e.w = &WordPress{Sites: sm, DB: dbm, Store: st, Runner: r, WebRoot: e.root, CLIPath: cli, PHPSocket: "/run/php/php8.3-fpm.sock",
		Fetch: func(_ context.Context, url string, _ int64) ([]byte, error) {
			switch {
			case strings.HasSuffix(url, "latest.tar.gz"):
				return e.archive, nil
			case strings.HasSuffix(url, "latest.tar.gz.sha1"):
				return []byte(e.sum + "  latest.tar.gz\n"), nil
			}
			return nil, errors.New("unexpected download " + url)
		}}
	return e
}

func (e *env) setSum() { s := sha1.Sum(e.archive); e.sum = hex.EncodeToString(s[:]) }

var good = WPInput{Domain: "Blog.Example.com", Title: "My Blog", AdminUser: "siteadmin", AdminEmail: "me@example.com"}

func TestInstallWordPress(t *testing.T) {
	e := setup(t)
	res, err := e.w.Install(context.Background(), good)
	if err != nil {
		t.Fatalf("%v\n%s", err, res.Log)
	}
	if res.URL != "http://blog.example.com/" || res.AdminURL != "http://blog.example.com/wp-admin/" || res.AdminUser != "siteadmin" || len(res.AdminPassword) != 20 || res.SetupInBrowser {
		t.Errorf("%+v", res)
	}
	site, err := e.st.SiteByDomain("blog.example.com")
	if err != nil || site.Type != store.SitePHP || site.App != "wordpress" {
		t.Fatalf("%v %+v", err, site)
	}
	for _, f := range []string{"index.php", "wp-settings.php", "wp-content/index.php", "wp-config.php"} {
		if _, err := os.Stat(filepath.Join(site.Root, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(site.Root, "index.php")); string(b) != "<?php //wp" {
		t.Error("index.php wrong")
	}
	if _, err := os.Stat(filepath.Join(site.Root, "index.html")); err == nil {
		t.Error("placeholder page left behind")
	}
	cfgPath := filepath.Join(site.Root, "wp-config.php")
	if fi, _ := os.Stat(cfgPath); fi.Mode().Perm() != 0o640 {
		t.Errorf("wp-config mode %v", fi.Mode())
	}
	cfg, _ := os.ReadFile(cfgPath)
	m := regexp.MustCompile(`DB_PASSWORD', '([A-Za-z0-9]{24})'`).FindStringSubmatch(string(cfg))
	if m == nil {
		t.Fatalf("no db password in wp-config:\n%s", cfg)
	}
	var created bool
	for _, in := range e.r.stdin {
		if strings.Contains(in, "IDENTIFIED BY '"+m[1]+"'") {
			created = true
		}
	}
	if !created {
		t.Error("wp-config password is not the one given to the database")
	}
	if strings.Count(string(cfg), "define( '") < 16 || !strings.Contains(string(cfg), "DB_HOST', 'localhost'") {
		t.Errorf("incomplete wp-config:\n%s", cfg)
	}
	joined := strings.Join(e.r.calls, "\n")
	for _, want := range []string{"apt-get install -y -qq php-fpm php-mysql", "chown -R www-data:www-data " + site.Root, "runuser -u www-data -- php " + e.w.CLIPath + " core install --path=" + site.Root, "--url=http://blog.example.com", "--admin_user=siteadmin"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing call %q", want)
		}
	}
	if strings.Contains(res.Log, res.AdminPassword) || strings.Contains(res.Log, m[1]) {
		t.Error("a password leaked into the log")
	}
	// A second install on the same domain is refused and changes nothing.
	if _, err := e.w.Install(context.Background(), good); err == nil {
		t.Error("duplicate domain accepted")
	}
}

func TestInstallRollsBackOnBadChecksum(t *testing.T) {
	e := setup(t)
	e.sum = strings.Repeat("0", 40)
	res, err := e.w.Install(context.Background(), good)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("%v", err)
	}
	if len(e.st.ListSites()) != 0 {
		t.Error("site left behind")
	}
	if _, err := os.Stat(filepath.Join(e.root, "blog.example.com")); err == nil {
		t.Error("site folder left behind")
	}
	dropped := false
	for _, in := range e.r.stdin {
		dropped = dropped || strings.Contains(in, "DROP DATABASE IF EXISTS `wp_blog_")
	}
	if !dropped {
		t.Error("database not dropped")
	}
	if !strings.Contains(res.Log, "cleaning up") {
		t.Errorf("log: %s", res.Log)
	}
}

func TestInstallRefusesUnsafeArchive(t *testing.T) {
	for name, hdr := range map[string]tar.Header{
		"symlink":   {Name: "wordpress/evil", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd", Mode: 0o777},
		"traversal": {Name: "wordpress/../../escape.php", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1},
		"outside":   {Name: "other/file.php", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1},
	} {
		e := setup(t)
		e.archive = tarball(t, map[string]string{"index.php": "x"}, hdr)
		e.setSum()
		if _, err := e.w.Install(context.Background(), good); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if len(e.st.ListSites()) != 0 {
			t.Errorf("%s: site left behind", name)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(e.root), "escape.php")); err == nil {
			t.Errorf("%s: file written outside the site", name)
		}
	}
}

func TestWPCLIFailureLeavesReadySite(t *testing.T) {
	e := setup(t)
	e.r.fail["runuser"] = true
	res, err := e.w.Install(context.Background(), good)
	if err != nil || !res.SetupInBrowser || res.AdminPassword != "" || !strings.HasSuffix(res.AdminURL, "/wp-admin/install.php") {
		t.Fatalf("%v %+v", err, res)
	}
	if s, err := e.st.SiteByDomain("blog.example.com"); err != nil || s.App != "wordpress" {
		t.Errorf("site not kept: %v", err)
	}
}

func TestSSLRequestedButFailing(t *testing.T) {
	e := setup(t)
	e.r.fail["certbot"] = true
	e.st.SetSettings(store.Settings{ACMEEmail: "a@example.com"})
	in := good
	in.SSL = true
	res, err := e.w.Install(context.Background(), in)
	if err != nil || res.SSL || !strings.HasPrefix(res.URL, "http://") || !strings.Contains(res.Log, "HTTPS could not be set up") {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestValidate(t *testing.T) {
	for _, bad := range []WPInput{
		{Title: "", AdminUser: "admin1", AdminEmail: "a@b.co"},
		{Title: "x", AdminUser: "a", AdminEmail: "a@b.co"},
		{Title: "x", AdminUser: "bad user", AdminEmail: "a@b.co"},
		{Title: "x", AdminUser: "admin1", AdminEmail: "nope"},
		{Title: "x\ny", AdminUser: "admin1", AdminEmail: "a@b.co"},
		{Title: "x", AdminUser: "admin1", AdminEmail: `a"b@c.co`},
	} {
		if bad.Validate() == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	e := setup(t)
	if _, err := e.w.Install(context.Background(), WPInput{Domain: "not a domain", Title: "x", AdminUser: "admin1", AdminEmail: "a@b.co"}); err == nil {
		t.Error("bad domain accepted")
	}
	if len(e.r.calls) != 0 {
		t.Errorf("work done before validation: %v", e.r.calls)
	}
}
