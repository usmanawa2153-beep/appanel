package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/usmanawa2153-beep/appanel/internal/cron"
	"github.com/usmanawa2153-beep/appanel/internal/firewall"
)

type opsRunner struct {
	status string
	calls  []string
}

func (r *opsRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	line := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, line)
	if name == "ufw" && len(args) >= 2 && args[0] == "status" {
		return r.status, nil
	}
	if name == "/bin/sh" {
		return "hello\n", nil
	}
	return "", nil
}
func (r *opsRunner) RunInput(ctx context.Context, _ string, name string, args ...string) (string, error) {
	return r.Run(ctx, name, args...)
}

const ufwActive = `Status: active

     To                         Action      From
     --                         ------      ----
[ 1] 22/tcp                     ALLOW IN    Anywhere
[ 2] 8080/tcp                   ALLOW IN    203.0.113.5
[ 3] 443/tcp                    ALLOW IN    Anywhere
`

func newOpsEnv(t *testing.T) (*env, *http.Cookie, *opsRunner, string) {
	t.Helper()
	dir := t.TempDir()
	rn := &opsRunner{status: ufwActive}
	e := newEnvWith(t, func(s *Server) {
		cm := cron.NewManager(s.st)
		cm.Runner = rn
		cm.Path = filepath.Join(dir, "cron.d", "appanel")
		s.SetCronManager(cm)
		fm := firewall.NewManager(func() int { return 8080 })
		fm.Runner = rn
		fm.LookPath = func(string) (string, error) { return "/usr/sbin/ufw", nil }
		fm.SSHConfig = filepath.Join(dir, "no-sshd")
		s.SetFirewallManager(fm)
	})
	return e, e.login(), rn, dir
}

func decodeBody(t *testing.T, res *http.Response, v any) {
	t.Helper()
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

func TestOpsRequireAuthAndCSRF(t *testing.T) {
	e, ck, _, _ := newOpsEnv(t)
	for _, p := range []string{"/api/cron", "/api/firewall"} {
		res := e.do("GET", secret+p, nil, nil, false)
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s unauthenticated = %d", p, res.StatusCode)
		}
	}
	res := e.do("POST", secret+"/api/cron", map[string]any{"name": "x"}, ck, false)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("POST without CSRF header = %d, want 403", res.StatusCode)
	}
	res = e.do("POST", secret+"/api/firewall/disable", nil, ck, false)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("firewall disable without CSRF = %d, want 403", res.StatusCode)
	}
}

func TestCronAPILifecycle(t *testing.T) {
	e, ck, _, dir := newOpsEnv(t)
	bad := map[string]any{"name": "n", "schedule": "61 * * * *", "command": "true", "enabled": true}
	res := e.do("POST", secret+"/api/cron", bad, ck, true)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid schedule = %d, want 400", res.StatusCode)
	}
	good := map[string]any{"name": "Backup", "schedule": "0 3 * * *", "command": "echo 100%", "enabled": true}
	res = e.do("POST", secret+"/api/cron", good, ck, true)
	var job struct {
		ID int64 `json:"id"`
	}
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d", res.StatusCode)
	}
	decodeBody(t, res, &job)
	b, err := os.ReadFile(filepath.Join(dir, "cron.d", "appanel"))
	if err != nil || !strings.Contains(string(b), `0 3 * * * root echo 100\%`) {
		t.Fatalf("cron file wrong: %v %q", err, b)
	}
	// Run now.
	res = e.do("POST", secret+"/api/cron/"+i64(job.ID)+"/run", nil, ck, true)
	var run struct {
		OK     bool   `json:"ok"`
		Output string `json:"output"`
	}
	decodeBody(t, res, &run)
	if !run.OK || strings.TrimSpace(run.Output) != "hello" {
		t.Errorf("run result = %+v", run)
	}
	// Pause removes it from the file.
	good["enabled"] = false
	res = e.do("PUT", secret+"/api/cron/"+i64(job.ID), good, ck, true)
	res.Body.Close()
	b, _ = os.ReadFile(filepath.Join(dir, "cron.d", "appanel"))
	if strings.Contains(string(b), "echo") {
		t.Errorf("paused job still in cron file: %q", b)
	}
	// Delete, then 404.
	res = e.do("DELETE", secret+"/api/cron/"+i64(job.ID), nil, ck, true)
	res.Body.Close()
	res = e.do("DELETE", secret+"/api/cron/"+i64(job.ID), nil, ck, true)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("second delete = %d, want 404", res.StatusCode)
	}
}

func TestFirewallAPI(t *testing.T) {
	e, ck, rn, _ := newOpsEnv(t)
	res := e.do("GET", secret+"/api/firewall", nil, ck, false)
	var st firewall.Status
	decodeBody(t, res, &st)
	if !st.Installed || !st.Active || len(st.Rules) != 3 {
		t.Fatalf("status = %+v", st)
	}

	// Invalid rule rejected without touching ufw.
	n := len(rn.calls)
	res = e.do("POST", secret+"/api/firewall/rules", map[string]string{"action": "allow", "port": "99999"}, ck, true)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || len(rn.calls) != n {
		t.Errorf("bad port: status %d, extra calls %v", res.StatusCode, rn.calls[n:])
	}
	// Valid rule.
	res = e.do("POST", secret+"/api/firewall/rules", map[string]string{"action": "allow", "port": "3306", "proto": "tcp", "source": "10.0.0.0/8"}, ck, true)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(strings.Join(rn.calls, "|"), "ufw allow proto tcp from 10.0.0.0/8 to any port 3306") {
		t.Errorf("add rule: %d %v", res.StatusCode, rn.calls)
	}

	// SSH rule is protected.
	res = e.do("DELETE", secret+"/api/firewall/rules/1?to="+"22%2Ftcp", nil, ck, true)
	res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Errorf("deleting SSH rule = %d, want 409", res.StatusCode)
	}
	// Panel port rule restricted to one IP is not "anywhere", so it may go.
	// Stale expectation is refused.
	res = e.do("DELETE", secret+"/api/firewall/rules/3?to=80%2Ftcp", nil, ck, true)
	res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Errorf("stale delete = %d, want 409", res.StatusCode)
	}
	for _, c := range rn.calls {
		if strings.Contains(c, "delete") {
			t.Fatalf("a delete reached ufw: %s", c)
		}
	}
	// Legit delete of 443.
	res = e.do("DELETE", secret+"/api/firewall/rules/3?to=443%2Ftcp", nil, ck, true)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(strings.Join(rn.calls, "|"), "ufw --force delete 3") {
		t.Errorf("delete 443: %d %v", res.StatusCode, rn.calls)
	}

	// Enable opens SSH and panel port first.
	rn.calls = nil
	res = e.do("POST", secret+"/api/firewall/enable", nil, ck, true)
	res.Body.Close()
	joined := strings.Join(rn.calls, "|")
	iAllow := strings.Index(joined, "ufw allow 22/tcp")
	iPanel := strings.Index(joined, "ufw allow 8080/tcp")
	iEnable := strings.Index(joined, "ufw --force enable")
	if iAllow < 0 || iPanel < 0 || iEnable < 0 || iAllow > iEnable || iPanel > iEnable {
		t.Errorf("enable order wrong: %v", rn.calls)
	}
}

func i64(n int64) string { return strconv.FormatInt(n, 10) }
