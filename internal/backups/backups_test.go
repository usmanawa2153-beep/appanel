package backups

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usmanawa2153-beep/appanel/internal/databases"
	"github.com/usmanawa2153-beep/appanel/internal/store"
)

// fakeRunner "runs" tar and the dump script by writing the output file.
type fakeRunner struct {
	calls   []string
	fail    bool
	nothing bool
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if f.fail {
		return "boom", errors.New("exit 2")
	}
	var out string
	switch name {
	case "tar":
		if args[0] == "-czf" {
			out = args[1]
		}
	case "bash":
		if args[2] == "appanel-dump" {
			out = args[4]
		}
	}
	if out != "" && !f.nothing {
		_ = os.WriteFile(out, []byte("data"), 0o644)
	}
	return "", nil
}
func (f *fakeRunner) RunInput(ctx context.Context, _ string, name string, args ...string) (string, error) {
	return f.Run(ctx, name, args...)
}

func setup(t *testing.T) (*Manager, *fakeRunner, store.Site) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "public_html")
	os.MkdirAll(root, 0o755)
	site, err := st.CreateSite(store.Site{Domain: "example.com", Type: store.SiteStatic, Root: root, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	fr := &fakeRunner{}
	dbm := &databases.Manager{Runner: fr, LookPath: func(string) (string, error) { return "/x", nil }}
	m := &Manager{Store: st, DB: dbm, Runner: fr, Dir: filepath.Join(t.TempDir(), "bk"),
		Now: func() time.Time { return time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC) }}
	return m, fr, site
}

func TestParseName(t *testing.T) {
	for _, ok := range []string{"site-example.com-20261127-100000.tar.gz", "db-mysql-shop-20261127-100000.sql.gz", "db-postgres-a_b-20261127-100000.sql.gz"} {
		if _, good := ParseName(ok); !good {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"../site-a.com-20261127-100000.tar.gz", "site-a.com-20261127-100000.sql.gz", "db-shop-20261127-100000.sql.gz",
		"site-a/b-20261127-100000.tar.gz", "site-a.com-20261127-100000.tar.gz.part", "other.tar.gz", "site-a.com-99999999-100000.tar.gz", "", "db-mysql-shop-20261127-100000.tar.gz"} {
		if _, good := ParseName(bad); good {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestBackupSiteAndList(t *testing.T) {
	m, fr, s := setup(t)
	it, err := m.BackupSite(context.Background(), s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if it.Name != "site-example.com-20261127-100000.tar.gz" || it.Bytes != 4 {
		t.Errorf("%+v", it)
	}
	if !strings.Contains(fr.calls[0], "-C "+s.Root+" .") {
		t.Errorf("%v", fr.calls)
	}
	fi, _ := os.Stat(filepath.Join(m.Dir, it.Name))
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}
	if l := m.List(); len(l) != 1 || l[0].Target != "example.com" {
		t.Errorf("%+v", l)
	}
	if ents, _ := os.ReadDir(m.Dir); len(ents) != 1 {
		t.Errorf("leftover files: %v", ents)
	}
}

func TestFailedBackupLeavesNothing(t *testing.T) {
	m, fr, s := setup(t)
	fr.fail = true
	if _, err := m.BackupSite(context.Background(), s.ID); err == nil {
		t.Fatal("expected error")
	}
	fr.fail, fr.nothing = false, true
	if _, err := m.BackupSite(context.Background(), s.ID); err == nil {
		t.Fatal("empty backup accepted")
	}
	if ents, _ := os.ReadDir(m.Dir); len(ents) != 0 {
		t.Errorf("leftovers: %v", ents)
	}
}

func TestProxySiteHasNoFiles(t *testing.T) {
	m, _, _ := setup(t)
	p, _ := m.Store.CreateSite(store.Site{Domain: "app.example.com", Type: store.SiteProxy, ProxyURL: "http://127.0.0.1:3000"})
	if _, err := m.BackupSite(context.Background(), p.ID); !errors.Is(err, ErrNoFiles) {
		t.Errorf("%v", err)
	}
}

func TestBackupDBAndRestore(t *testing.T) {
	m, fr, s := setup(t)
	it, err := m.BackupDB(context.Background(), "mysql", "shop")
	if err != nil || it.Name != "db-mysql-shop-20261127-100000.sql.gz" {
		t.Fatalf("%v %+v", err, it)
	}
	fr.calls = nil
	if err := m.Restore(context.Background(), it.Name); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fr.calls[0], "appanel-restore shop "+filepath.Join(m.Dir, it.Name)) {
		t.Errorf("%v", fr.calls)
	}
	site, _ := m.BackupSite(context.Background(), s.ID)
	fr.calls = nil
	if err := m.Restore(context.Background(), site.Name); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(fr.calls[0], "tar -xzf") || !strings.Contains(fr.calls[0], "-C "+s.Root) {
		t.Errorf("%v", fr.calls)
	}
}

func TestPathRefusesTraversal(t *testing.T) {
	m, _, _ := setup(t)
	os.MkdirAll(m.Dir, 0o700)
	secret := filepath.Join(filepath.Dir(m.Dir), "secret.tar.gz")
	os.WriteFile(secret, []byte("x"), 0o600)
	for _, n := range []string{"../secret.tar.gz", "..%2fsecret.tar.gz", "site-../../x-20261127-100000.tar.gz", filepath.Base(secret)} {
		if _, err := m.Path(n); err == nil {
			t.Errorf("%q accepted", n)
		}
	}
	// A symlink named like a backup must not be followed.
	link := filepath.Join(m.Dir, "site-evil.com-20261127-100000.tar.gz")
	os.Symlink(secret, link)
	if _, err := m.Path(filepath.Base(link)); err == nil {
		t.Error("symlink followed")
	}
}

func TestPrune(t *testing.T) {
	m, _, s := setup(t)
	for i := 0; i < 4; i++ {
		m.Now = func() time.Time { return time.Date(2026, 11, 20+i, 3, 0, 0, 0, time.UTC) }
		if _, err := m.BackupSite(context.Background(), s.ID); err != nil {
			t.Fatal(err)
		}
	}
	n, err := m.Prune(2)
	if err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
	l := m.List()
	if len(l) != 2 || l[0].Created.Day() != 23 || l[1].Created.Day() != 22 {
		t.Errorf("%+v", l)
	}
}
