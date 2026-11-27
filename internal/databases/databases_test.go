package databases

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fake struct {
	list  string
	fail  string // substring of stdin that makes the call fail
	calls []string
	stdin []string
}

func (f *fake) Run(_ context.Context, name string, args ...string) (string, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	return "", nil
}
func (f *fake) RunInput(_ context.Context, in, name string, args ...string) (string, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	f.stdin = append(f.stdin, in)
	if f.fail != "" && strings.Contains(in, f.fail) {
		return "ERROR: role already exists", errors.New("exit 1")
	}
	if strings.Contains(in, "SCHEMATA") || strings.Contains(in, "pg_database") {
		return f.list, nil
	}
	return "", nil
}

func mgr(f *fake) *Manager {
	return &Manager{Runner: f, LookPath: func(string) (string, error) { return "/usr/bin/x", nil }}
}

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"shop", "a", "wp_site1", strings.Repeat("a", 32)} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "1a", "Shop", "a-b", "a b", "a;drop", "a`b", "a'b", `a"b`, strings.Repeat("a", 33), "../x", "root", "postgres", "mysql", "pg_x", "sys"} {
		if ValidateName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestGeneratePassword(t *testing.T) {
	a, _ := GeneratePassword()
	b, _ := GeneratePassword()
	if len(a) != 24 || a == b {
		t.Fatalf("bad passwords %q %q", a, b)
	}
	for _, r := range a {
		if !strings.ContainsRune(pwAlphabet, r) {
			t.Fatalf("unexpected char %q", r)
		}
	}
}

func TestParseList(t *testing.T) {
	got := ParseList("shop\t1024\nblog|0\n\nplain\n")
	if len(got) != 3 || got[0] != (Database{"shop", 1024}) || got[1].Name != "blog" || got[2].Name != "plain" {
		t.Fatalf("%+v", got)
	}
}

func TestCreateMySQL(t *testing.T) {
	f := &fake{}
	res, err := mgr(f).Create(context.Background(), MySQL, "shop")
	if err != nil {
		t.Fatal(err)
	}
	last := f.stdin[len(f.stdin)-1]
	for _, want := range []string{"CREATE DATABASE `shop`", "CREATE USER 'shop'@'localhost' IDENTIFIED BY '" + res.Password + "'", "GRANT ALL PRIVILEGES ON `shop`.*"} {
		if !strings.Contains(last, want) {
			t.Errorf("missing %q in %q", want, last)
		}
	}
	for _, c := range f.calls {
		if strings.Contains(c, res.Password) {
			t.Errorf("password leaked into argv: %s", c)
		}
	}
	if res.Port != 3306 || res.User != "shop" {
		t.Errorf("%+v", res)
	}
}

func TestCreatePostgresUsesPostgresUser(t *testing.T) {
	f := &fake{}
	res, err := mgr(f).Create(context.Background(), Postgres, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(f.calls[len(f.calls)-1], "runuser -u postgres -- psql") || res.Port != 5432 {
		t.Errorf("%v %+v", f.calls, res)
	}
}

func TestCreateExistingAndRollback(t *testing.T) {
	f := &fake{list: "shop\t0\n"}
	if _, err := mgr(f).Create(context.Background(), MySQL, "shop"); !errors.Is(err, ErrExists) {
		t.Errorf("err = %v", err)
	}
	f = &fake{fail: "CREATE ROLE"}
	_, err := mgr(f).Create(context.Background(), Postgres, "shop")
	if !errors.Is(err, ErrExists) {
		t.Errorf("err = %v", err)
	}
	if !strings.Contains(f.stdin[len(f.stdin)-1], "DROP DATABASE IF EXISTS") {
		t.Error("no rollback after failed create")
	}
}

func TestCreateRejectsInjection(t *testing.T) {
	f := &fake{}
	if _, err := mgr(f).Create(context.Background(), MySQL, "a`; DROP DATABASE x; --"); err == nil {
		t.Fatal("accepted")
	}
	if len(f.calls) != 0 {
		t.Errorf("ran %v", f.calls)
	}
}

func TestDropOnlyListed(t *testing.T) {
	f := &fake{list: "shop\t0\n"}
	m := mgr(f)
	if err := m.Drop(context.Background(), MySQL, "other"); err == nil {
		t.Error("dropped an unlisted database")
	}
	if err := m.Drop(context.Background(), MySQL, "shop"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.stdin[len(f.stdin)-1], "DROP USER IF EXISTS 'shop'@'localhost'") {
		t.Errorf("%v", f.stdin)
	}
	if err := m.Drop(context.Background(), MySQL, "mysql"); err == nil {
		t.Error("dropped reserved name")
	}
}

func TestNotInstalled(t *testing.T) {
	m := &Manager{Runner: &fake{}, LookPath: func(string) (string, error) { return "", errors.New("no") }}
	if _, err := m.Create(context.Background(), MySQL, "shop"); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("%v", err)
	}
	if st := m.Status(context.Background(), Postgres); st.Installed || st.Running {
		t.Errorf("%+v", st)
	}
}

func TestResetPassword(t *testing.T) {
	f := &fake{list: "shop\t0\n"}
	res, err := mgr(f).ResetPassword(context.Background(), Postgres, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.stdin[len(f.stdin)-1], `ALTER ROLE "shop" WITH PASSWORD '`+res.Password+`'`) {
		t.Errorf("%v", f.stdin)
	}
}

func TestDumpCommandKeepsArgsOutOfShell(t *testing.T) {
	a := DumpCommand(MySQL, "shop", "/var/backups/x y.sql.gz")
	if a[0] != "bash" || a[3] != "appanel-dump" || a[4] != "shop" || a[5] != "/var/backups/x y.sql.gz" {
		t.Fatalf("%v", a)
	}
	if strings.Contains(a[2], "shop") || strings.Contains(a[2], "x y") {
		t.Error("values interpolated into the script")
	}
	if !strings.Contains(a[2], "pipefail") {
		t.Error("no pipefail: a failed dump would look successful")
	}
}
