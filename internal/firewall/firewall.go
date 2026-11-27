// Package firewall drives ufw. It refuses actions that would lock the admin out
// of the server (closing SSH or the panel's own port).
package firewall

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/usmanawa2153-beep/appanel/internal/execx"
)

var (
	ErrNotInstalled = errors.New("ufw is not installed; use \"Install firewall\" first")
	ErrProtected    = errors.New("this rule keeps SSH or the panel reachable; removing it would lock you out")
	ErrChanged      = errors.New("the rule list changed, refresh and try again")
)

// Rule is one line of `ufw status numbered`.
type Rule struct {
	Number int    `json:"number"`
	To     string `json:"to"`
	Action string `json:"action"`
	From   string `json:"from"`
	V6     bool   `json:"v6"`
}

// Status is what the UI shows.
type Status struct {
	Installed bool   `json:"installed"`
	Active    bool   `json:"active"`
	Rules     []Rule `json:"rules"`
	Protected []int  `json:"protected_ports"`
}

var ruleRe = regexp.MustCompile(`^\[\s*(\d+)\]\s+(.+?)\s+(ALLOW|DENY|REJECT|LIMIT)(?:\s+(?:IN|OUT|FWD))?\s+(.+?)\s*$`)

// ParseStatus parses the output of `ufw status numbered`.
func ParseStatus(out string) (active bool, rules []Rule) {
	rules = []Rule{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "Status:") {
			active = strings.Contains(line, "active") && !strings.Contains(line, "inactive")
			continue
		}
		m := ruleRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		r := Rule{Number: n, To: m[2], Action: m[3], From: m[4]}
		if strings.Contains(r.To, "(v6)") {
			r.V6 = true
		}
		rules = append(rules, r)
	}
	return
}

// ruleCovers reports whether a rule's "To" column includes the TCP port p.
func ruleCovers(to string, p int) bool {
	to = strings.TrimSpace(strings.ReplaceAll(to, "(v6)", ""))
	if strings.EqualFold(to, "anywhere") {
		return true
	}
	if i := strings.Index(to, "/"); i >= 0 {
		if proto := strings.TrimSpace(to[i+1:]); proto != "tcp" && proto != "" {
			return false
		}
		to = to[:i]
	}
	for _, part := range strings.Split(to, ",") {
		part = strings.TrimSpace(part)
		lo, hi, isRange := strings.Cut(part, ":")
		a, err := strconv.Atoi(lo)
		if err != nil {
			continue
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(hi); err != nil {
				continue
			}
		}
		if p >= a && p <= b {
			return true
		}
	}
	return false
}

var sshPortRe = regexp.MustCompile(`(?mi)^\s*Port\s+(\d{1,5})\s*(?:#.*)?$`)

// SSHPorts reads the SSH daemon's configured ports (default 22).
func SSHPorts(configPath string) []int {
	files := []string{configPath}
	if extra, _ := filepath.Glob(configPath + ".d/*.conf"); len(extra) > 0 {
		files = append(files, extra...)
	}
	seen := map[int]bool{}
	var ports []int
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, m := range sshPortRe.FindAllStringSubmatch(string(b), -1) {
			if p, _ := strconv.Atoi(m[1]); p >= 1 && p <= 65535 && !seen[p] {
				seen[p] = true
				ports = append(ports, p)
			}
		}
	}
	if len(ports) == 0 {
		ports = []int{22}
	}
	sort.Ints(ports)
	return ports
}

// Manager runs ufw commands.
type Manager struct {
	Runner    execx.Runner
	LookPath  func(string) (string, error)
	SSHConfig string
	PanelPort func() int

	mu sync.Mutex
}

// NewManager returns a Manager with production settings.
func NewManager(panelPort func() int) *Manager {
	return &Manager{Runner: execx.Exec{}, LookPath: exec.LookPath, SSHConfig: "/etc/ssh/sshd_config", PanelPort: panelPort}
}

// Protected lists TCP ports that must stay reachable.
func (m *Manager) Protected() []int {
	ports := SSHPorts(m.SSHConfig)
	if m.PanelPort != nil {
		if p := m.PanelPort(); p > 0 {
			ports = append(ports, p)
		}
	}
	sort.Ints(ports)
	return ports
}

func (m *Manager) installed() bool { _, err := m.LookPath("ufw"); return err == nil }

// Status reports whether ufw is installed/active and lists its rules.
func (m *Manager) Status(ctx context.Context) Status {
	st := Status{Installed: m.installed(), Rules: []Rule{}, Protected: m.Protected()}
	if !st.Installed {
		return st
	}
	out, err := m.Runner.Run(ctx, "ufw", "status", "numbered")
	if err == nil {
		st.Active, st.Rules = ParseStatus(out)
	}
	return st
}

// Install installs ufw with apt (it is not enabled).
func (m *Manager) Install(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var log strings.Builder
	for _, c := range [][]string{{"apt-get", "update", "-qq"}, {"apt-get", "install", "-y", "-qq", "ufw"}} {
		out, err := m.Runner.Run(ctx, c[0], c[1:]...)
		log.WriteString("$ " + strings.Join(c, " ") + "\n" + execx.Tail(out, 3000) + "\n")
		if err != nil {
			return log.String(), fmt.Errorf("%s failed", strings.Join(c[:2], " "))
		}
	}
	return log.String(), nil
}

// Enable allows SSH and the panel port first, then turns the firewall on, so
// enabling can never cut the current session off.
func (m *Manager) Enable(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.installed() {
		return "", ErrNotInstalled
	}
	var log strings.Builder
	run := func(args ...string) error {
		out, err := m.Runner.Run(ctx, "ufw", args...)
		log.WriteString("$ ufw " + strings.Join(args, " ") + "\n" + execx.Tail(out, 2000) + "\n")
		return err
	}
	for _, p := range m.Protected() {
		if err := run("allow", fmt.Sprintf("%d/tcp", p)); err != nil {
			return log.String(), fmt.Errorf("could not allow port %d, firewall left off", p)
		}
	}
	_ = run("default", "deny", "incoming")
	_ = run("default", "allow", "outgoing")
	if err := run("--force", "enable"); err != nil {
		return log.String(), errors.New("ufw could not be enabled")
	}
	return log.String(), nil
}

// Disable turns the firewall off.
func (m *Manager) Disable(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.installed() {
		return ErrNotInstalled
	}
	_, err := m.Runner.Run(ctx, "ufw", "disable")
	return err
}

// RuleInput describes a rule to add.
type RuleInput struct {
	Action string `json:"action"` // allow | deny | reject | limit
	Port   string `json:"port"`   // 443 or 8000:8100
	Proto  string `json:"proto"`  // tcp | udp | any
	Source string `json:"source"` // empty/any, an IP or a CIDR
}

var portRe = regexp.MustCompile(`^(\d{1,5})(?::(\d{1,5}))?$`)

// BuildArgs validates a rule and returns the ufw arguments for it.
func BuildArgs(in RuleInput) ([]string, error) {
	action := strings.ToLower(strings.TrimSpace(in.Action))
	switch action {
	case "allow", "deny", "reject", "limit":
	default:
		return nil, errors.New("action must be allow, deny, reject or limit")
	}
	pm := portRe.FindStringSubmatch(strings.TrimSpace(in.Port))
	if pm == nil {
		return nil, errors.New("port must be a number (443) or a range (8000:8100)")
	}
	lo, _ := strconv.Atoi(pm[1])
	hi := lo
	isRange := pm[2] != ""
	if isRange {
		hi, _ = strconv.Atoi(pm[2])
	}
	if lo < 1 || lo > 65535 || hi < 1 || hi > 65535 || hi < lo {
		return nil, errors.New("ports must be between 1 and 65535")
	}
	port := pm[1]
	if isRange {
		port += ":" + pm[2]
	}
	proto := strings.ToLower(strings.TrimSpace(in.Proto))
	if proto == "" {
		proto = "tcp"
	}
	if proto != "tcp" && proto != "udp" && proto != "any" {
		return nil, errors.New("protocol must be tcp, udp or any")
	}
	if isRange && proto == "any" {
		return nil, errors.New("port ranges need a protocol (tcp or udp)")
	}
	src := strings.TrimSpace(in.Source)
	if strings.EqualFold(src, "any") || strings.EqualFold(src, "anywhere") {
		src = ""
	}
	if src != "" {
		if net.ParseIP(src) == nil {
			if _, _, err := net.ParseCIDR(src); err != nil {
				return nil, errors.New("source must be an IP address or a CIDR range like 203.0.113.0/24")
			}
		}
	}
	if src == "" {
		if proto == "any" {
			return []string{action, port}, nil
		}
		return []string{action, port + "/" + proto}, nil
	}
	args := []string{action}
	if proto != "any" {
		args = append(args, "proto", proto)
	}
	return append(args, "from", src, "to", "any", "port", port), nil
}

// AddRule adds a rule.
func (m *Manager) AddRule(ctx context.Context, in RuleInput) (string, error) {
	args, err := BuildArgs(in)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.installed() {
		return "", ErrNotInstalled
	}
	out, err := m.Runner.Run(ctx, "ufw", args...)
	if err != nil {
		return execx.Tail(out, 1000), fmt.Errorf("ufw refused the rule: %s", execx.Tail(out, 300))
	}
	return execx.Tail(out, 1000), nil
}

// DeleteRule removes rule number n. expectedTo must match what the UI showed,
// so a stale list can never delete a different rule than the user clicked.
func (m *Manager) DeleteRule(ctx context.Context, n int, expectedTo string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.installed() {
		return ErrNotInstalled
	}
	out, err := m.Runner.Run(ctx, "ufw", "status", "numbered")
	if err != nil {
		return fmt.Errorf("could not read the rules: %s", execx.Tail(out, 300))
	}
	active, rules := ParseStatus(out)
	var target *Rule
	for i := range rules {
		if rules[i].Number == n {
			target = &rules[i]
		}
	}
	if target == nil || target.To != expectedTo {
		return ErrChanged
	}
	if active && target.Action == "ALLOW" {
		for _, p := range m.Protected() {
			if ruleCovers(target.To, p) && strings.HasPrefix(strings.ToLower(target.From), "anywhere") {
				return ErrProtected
			}
		}
	}
	if out, err := m.Runner.Run(ctx, "ufw", "--force", "delete", strconv.Itoa(n)); err != nil {
		return fmt.Errorf("ufw could not delete the rule: %s", execx.Tail(out, 300))
	}
	return nil
}
