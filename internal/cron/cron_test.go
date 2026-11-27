package cron

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usmanawa2153-beep/appanel/internal/store"
)

func TestValidateSchedule(t *testing.T) {
	good := map[string]string{
		"30 2 * * *": "30 2 * * *", "  */5   *  * * * ": "*/5 * * * *", "0 0 1,15 * *": "0 0 1,15 * *",
		"0 9-17 * * 1-5": "0 9-17 * * 1-5", "@Daily": "@daily", "@reboot": "@reboot", "0 0 * * 7": "0 0 * * 7",
	}
	for in, want := range good {
		got, err := ValidateSchedule(in)
		if err != nil || got != want {
			t.Errorf("%q -> %q, %v (want %q)", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "* * * *", "* * * * * *", "60 * * * *", "* 24 * * *", "* * 0 * *", "* * * 13 *", "* * * * 8", "*/0 * * * *",
		"5-1 * * * *", "a * * * *", "* * * * * root", "@sometimes", "0 0 * * *\n* * * * *", "1,,2 * * * *", "-1 * * * *"} {
		if got, err := ValidateSchedule(bad); err == nil {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}
}

func TestValidateCommandAndName(t *testing.T) {
	if _, err := ValidateCommand("echo hi\n* * * * * root evil"); err == nil {
		t.Error("newline accepted")
	}
	if _, err := ValidateCommand("   "); err == nil {
		t.Error("empty accepted")
	}
	if _, err := ValidateCommand(strings.Repeat("a", 2001)); err == nil {
		t.Error("too long accepted")
	}
	if c, err := ValidateCommand("  /usr/bin/php /var/www/x/cron.php >> /var/log/x.log 2>&1 "); err != nil || strings.HasPrefix(c, " ") {
		t.Errorf("good command: %q %v", c, err)
	}
	if _, err := ValidateName("bad\nname"); err == nil {
		t.Error("newline in name accepted")
	}
}

func TestRenderEscapesPercentAndSkipsDisabled(t *testing.T) {
	out := Render([]store.CronJob{
		{ID: 1, Name: "backup", Schedule: "0 3 * * *", Command: "tar czf /b/x-$(date +%F).tgz /var/www", Enabled: true},
		{ID: 2, Name: "off", Schedule: "* * * * *", Command: "echo no", Enabled: false},
	})
	if !strings.Contains(out, `0 3 * * * root tar czf /b/x-$(date +\%F).tgz /var/www`) {
		t.Errorf("percent not escaped:\n%s", out)
	}
	if strings.Contains(out, "echo no") {
		t.Error("disabled job rendered")
	}
	if !strings.Contains(out, "SHELL=/bin/sh") {
		t.Error("environment header missing")
	}
}

type fake struct{ lastArgs []string }

func (f *fake) Run(_ context.Context, name string, args ...string) (string, error) {
	f.lastArgs = append([]string{name}, args...)
	return "ran: " + strings.Join(args, " "), nil
}
func (f *fake) RunInput(ctx context.Context, _ string, name string, args ...string) (string, error) {
	return f.Run(ctx, name, args...)
}

func newMgr(t *testing.T) (*Manager, *fake) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "d"))
	if err != nil {
		t.Fatal(err)
	}
	f := &fake{}
	m := NewManager(st)
	m.Runner = f
	m.Path = filepath.Join(t.TempDir(), "cron.d", "appanel")
	return m, f
}

func TestManagerLifecycle(t *testing.T) {
	m, f := newMgr(t)
	j, err := m.Create(Input{Name: "hello", Schedule: "*/10 * * * *", Command: "echo hi", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(m.Path)
	if !strings.Contains(string(b), "*/10 * * * * root echo hi") {
		t.Fatalf("file:\n%s", b)
	}
	if st, _ := os.Stat(m.Path); st.Mode().Perm() != 0o644 {
		t.Errorf("mode %v, cron requires 0644", st.Mode().Perm())
	}
	if _, err := m.Update(j.ID, Input{Name: "hello", Schedule: "0 1 * * *", Command: "echo bye", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(m.Path)
	if strings.Contains(string(b), "echo bye") {
		t.Error("disabled job still in file")
	}
	out, err := m.RunNow(context.Background(), j.ID)
	if err != nil || !strings.Contains(out, "echo bye") || f.lastArgs[0] != "/bin/sh" || f.lastArgs[1] != "-c" {
		t.Fatalf("run now: %q %v %v", out, err, f.lastArgs)
	}
	if err := m.Delete(j.ID); err != nil {
		t.Fatal(err)
	}
	if len(m.List()) != 0 {
		t.Error("job remains")
	}
	if err := m.Delete(j.ID); err != ErrNotFound {
		t.Errorf("second delete: %v", err)
	}
	if _, err := m.Create(Input{Name: "x", Schedule: "bad", Command: "x"}); err == nil {
		t.Error("invalid schedule accepted")
	}
}
