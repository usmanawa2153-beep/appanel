package firewall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const sample = `Status: active

     To                         Action      From
     --                         ------      ----
[ 1] 22/tcp                     ALLOW IN    Anywhere
[ 2] 8443/tcp                   ALLOW IN    Anywhere
[ 3] 80,443/tcp                 ALLOW IN    Anywhere
[ 4] 3306/tcp                   ALLOW IN    203.0.113.0/24
[ 5] Anywhere                   DENY IN     198.51.100.7
[ 6] 22/tcp (v6)                ALLOW IN    Anywhere (v6)
[ 7] 5000:5100/udp              LIMIT IN    Anywhere
`

func TestParseStatus(t *testing.T) {
	active, rules := ParseStatus(sample)
	if !active || len(rules) != 7 {
		t.Fatalf("active=%v rules=%d", active, len(rules))
	}
	want := Rule{Number: 4, To: "3306/tcp", Action: "ALLOW", From: "203.0.113.0/24"}
	if rules[3] != want {
		t.Errorf("rule 4 = %+v", rules[3])
	}
	if !rules[5].V6 || rules[5].To != "22/tcp (v6)" {
		t.Errorf("v6 rule = %+v", rules[5])
	}
	if rules[6].Action != "LIMIT" || rules[4].Action != "DENY" || rules[4].From != "198.51.100.7" {
		t.Errorf("rules 5/7 = %+v %+v", rules[4], rules[6])
	}
	if a, r := ParseStatus("Status: inactive\n"); a || len(r) != 0 || r == nil {
		t.Errorf("inactive = %v %v", a, r)
	}
}

func TestRuleCovers(t *testing.T) {
	cases := []struct {
		to   string
		p    int
		want bool
	}{
		{"22/tcp", 22, true}, {"22", 22, true}, {"22/udp", 22, false}, {"80,443/tcp", 443, true}, {"8000:9000/tcp", 8443, true},
		{"8000:9000/tcp", 7999, false}, {"22/tcp (v6)", 22, true}, {"Anywhere", 22, true}, {"3306/tcp", 22, false},
	}
	for _, c := range cases {
		if got := ruleCovers(c.to, c.p); got != c.want {
			t.Errorf("ruleCovers(%q,%d) = %v", c.to, c.p, got)
		}
	}
}

func TestSSHPorts(t *testing.T) {
	d := t.TempDir()
	cfg := filepath.Join(d, "sshd_config")
	os.WriteFile(cfg, []byte("# Port 2222\nPort 2200\n  Port 22 # main\nPasswordAuthentication no\n"), 0o644)
	os.MkdirAll(cfg+".d", 0o755)
	os.WriteFile(filepath.Join(cfg+".d", "x.conf"), []byte("Port 2201\nPort 99999\n"), 0o644)
	if got := SSHPorts(cfg); !reflect.DeepEqual(got, []int{22, 2200, 2201}) {
		t.Errorf("ports = %v", got)
	}
	if got := SSHPorts(filepath.Join(d, "missing")); !reflect.DeepEqual(got, []int{22}) {
		t.Errorf("default = %v", got)
	}
}

func TestBuildArgs(t *testing.T) {
	ok := []struct {
		in   RuleInput
		want []string
	}{
		{RuleInput{"allow", "443", "tcp", ""}, []string{"allow", "443/tcp"}},
		{RuleInput{"ALLOW", " 53 ", "any", "any"}, []string{"allow", "53"}},
		{RuleInput{"deny", "8000:8100", "udp", ""}, []string{"deny", "8000:8100/udp"}},
		{RuleInput{"allow", "3306", "tcp", "203.0.113.0/24"}, []string{"allow", "proto", "tcp", "from", "203.0.113.0/24", "to", "any", "port", "3306"}},
		{RuleInput{"allow", "22", "", "2001:db8::1"}, []string{"allow", "proto", "tcp", "from", "2001:db8::1", "to", "any", "port", "22"}},
		{RuleInput{"limit", "22", "tcp", ""}, []string{"limit", "22/tcp"}},
	}
	for _, c := range ok {
		got, err := BuildArgs(c.in)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%+v -> %v, %v (want %v)", c.in, got, err, c.want)
		}
	}
	bad := []RuleInput{
		{"open", "80", "tcp", ""}, {"allow", "0", "tcp", ""}, {"allow", "70000", "tcp", ""}, {"allow", "80; rm -rf /", "tcp", ""},
		{"allow", "9000:8000", "tcp", ""}, {"allow", "8000:9000", "any", ""}, {"allow", "80", "icmp", ""},
		{"allow", "80", "tcp", "not-an-ip"}, {"allow", "80", "tcp", "1.2.3.4; reboot"}, {"allow", "", "tcp", ""}, {"allow", "80", "tcp", "999.1.1.1"},
	}
	for _, in := range bad {
		if got, err := BuildArgs(in); err == nil {
			t.Errorf("%+v accepted as %v", in, got)
		}
	}
}

type fakeRunner struct {
	status string
	calls  [][]string
	fail   map[string]bool
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	joined := strings.Join(args, " ")
	if f.fail[joined] {
		return "boom", errors.New("exit 1")
	}
	if name == "ufw" && joined == "status numbered" {
		return f.status, nil
	}
	return "Rule added", nil
}
func (f *fakeRunner) RunInput(ctx context.Context, _ string, n string, a ...string) (string, error) {
	return f.Run(ctx, n, a...)
}
func (f *fakeRunner) idx(prefix string) int {
	for i, c := range f.calls {
		if strings.Join(c, " ") == prefix {
			return i
		}
	}
	return -1
}

func newMgr(status string) (*Manager, *fakeRunner) {
	f := &fakeRunner{status: status, fail: map[string]bool{}}
	m := NewManager(func() int { return 8443 })
	m.Runner = f
	m.LookPath = func(string) (string, error) { return "/usr/sbin/ufw", nil }
	m.SSHConfig = "/nonexistent"
	return m, f
}

func TestEnableAllowsSSHAndPanelFirst(t *testing.T) {
	m, f := newMgr("Status: inactive\n")
	if _, err := m.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	ssh, panel, enable := f.idx("ufw allow 22/tcp"), f.idx("ufw allow 8443/tcp"), f.idx("ufw --force enable")
	if ssh < 0 || panel < 0 || enable < 0 || !(ssh < enable && panel < enable) {
		t.Fatalf("must allow SSH+panel before enabling: %v", f.calls)
	}
}

func TestEnableAbortsIfAllowFails(t *testing.T) {
	m, f := newMgr("Status: inactive\n")
	f.fail["allow 8443/tcp"] = true
	if _, err := m.Enable(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if f.idx("ufw --force enable") >= 0 {
		t.Fatal("firewall was enabled even though the panel port could not be allowed")
	}
}

func TestDeleteRuleProtection(t *testing.T) {
	m, f := newMgr(sample)
	ctx := context.Background()
	if err := m.DeleteRule(ctx, 1, "22/tcp"); !errors.Is(err, ErrProtected) {
		t.Fatalf("SSH rule: %v", err)
	}
	if err := m.DeleteRule(ctx, 2, "8443/tcp"); !errors.Is(err, ErrProtected) {
		t.Fatalf("panel rule: %v", err)
	}
	if err := m.DeleteRule(ctx, 6, "22/tcp (v6)"); !errors.Is(err, ErrProtected) {
		t.Fatalf("v6 SSH rule: %v", err)
	}
	if err := m.DeleteRule(ctx, 4, "80,443/tcp"); !errors.Is(err, ErrChanged) {
		t.Fatalf("stale list must be refused: %v", err)
	}
	if err := m.DeleteRule(ctx, 99, "x"); !errors.Is(err, ErrChanged) {
		t.Fatalf("missing rule: %v", err)
	}
	if f.idx("ufw --force delete 4") >= 0 || f.idx("ufw --force delete 1") >= 0 {
		t.Fatal("a refused delete still ran")
	}
	if err := m.DeleteRule(ctx, 4, "3306/tcp"); err != nil {
		t.Fatal(err)
	}
	if f.idx("ufw --force delete 4") < 0 {
		t.Fatalf("delete not run: %v", f.calls)
	}
}

func TestDeleteProtectedAllowedWhenFirewallInactive(t *testing.T) {
	m, _ := newMgr(strings.Replace(sample, "Status: active", "Status: inactive", 1))
	if err := m.DeleteRule(context.Background(), 1, "22/tcp"); err != nil {
		t.Fatalf("inactive firewall can't lock anyone out: %v", err)
	}
}

func TestNotInstalled(t *testing.T) {
	m, _ := newMgr("")
	m.LookPath = func(string) (string, error) { return "", errors.New("no") }
	if st := m.Status(context.Background()); st.Installed || len(st.Rules) != 0 {
		t.Errorf("status = %+v", st)
	}
	if _, err := m.Enable(context.Background()); !errors.Is(err, ErrNotInstalled) {
		t.Error(err)
	}
	if _, err := m.AddRule(context.Background(), RuleInput{"allow", "80", "tcp", ""}); !errors.Is(err, ErrNotInstalled) {
		t.Error(err)
	}
}

func TestAddRuleRunsUfw(t *testing.T) {
	m, f := newMgr(sample)
	if _, err := m.AddRule(context.Background(), RuleInput{"allow", "3306", "tcp", "10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
	if f.idx("ufw allow proto tcp from 10.0.0.0/8 to any port 3306") < 0 {
		t.Fatalf("calls: %v", f.calls)
	}
}
