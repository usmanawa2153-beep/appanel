package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/usmanawa2153-beep/appanel/internal/config"
	"github.com/usmanawa2153-beep/appanel/internal/firewall"
	"github.com/usmanawa2153-beep/appanel/internal/sites"
	"github.com/usmanawa2153-beep/appanel/internal/store"
)

// stopService stops the panel service while the command edits its files (so
// the running panel cannot overwrite them) and returns a function that starts
// it again. It does nothing when the service is not running under systemd.
func stopService() func() {
	if exec.Command("systemctl", "is-active", "--quiet", "appanel").Run() != nil {
		return func() {}
	}
	fmt.Println("Stopping the panel for a moment…")
	_ = exec.Command("systemctl", "stop", "appanel").Run()
	return func() {
		fmt.Println("Starting the panel…")
		if err := exec.Command("systemctl", "start", "appanel").Run(); err != nil {
			fmt.Println("Could not start it automatically; run: systemctl start appanel")
		}
	}
}

func cmdDomain(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: appanel domain set <domain> [--email you@example.com] | unset | show")
	}
	dir := config.DefaultDataDir()
	cfg, err := config.Load(dir)
	if err != nil {
		return fmt.Errorf("cannot load config (run `appanel init` first): %w", err)
	}
	switch args[0] {
	case "show":
		if cfg.PanelDomain == "" {
			fmt.Printf("No panel domain. The panel is on %s:%d%s/\n", hostOrIP(cfg), cfg.Port, cfg.SecretPath)
		} else {
			fmt.Printf("Panel domain: https://%s%s/\n(the IP address does not open the panel)\n", cfg.PanelDomain, cfg.SecretPath)
		}
		return nil
	case "set":
		fs := flag.NewFlagSet("domain set", flag.ContinueOnError)
		email := fs.String("email", "", "contact email for Let's Encrypt (needed the first time)")
		rest := args[1:]
		if len(rest) == 0 {
			return errors.New("usage: appanel domain set <domain> [--email you@example.com]")
		}
		domain := strings.ToLower(strings.TrimSpace(rest[0]))
		if err := fs.Parse(rest[1:]); err != nil {
			return err
		}
		start := stopService()
		defer start()
		st, err := store.Open(dir)
		if err != nil {
			return err
		}
		if *email != "" {
			if !sites.ValidEmail(*email) {
				return errors.New("that email address does not look valid")
			}
			s := st.Settings()
			s.ACMEEmail = *email
			if err := st.SetSettings(s); err != nil {
				return err
			}
		}
		m := sites.NewManager(st)
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
		defer cancel()
		if status := m.Status(ctx); !status.NginxInstalled || !status.CertbotInstalled {
			fmt.Println("Installing Nginx and Certbot…")
			if out, err := m.Setup(ctx, false); err != nil {
				return fmt.Errorf("install failed: %w\n%s", err, out)
			}
		}
		fmt.Printf("Getting a certificate for %s (the domain must already point to this server)…\n", domain)
		if out, err := m.AttachPanel(ctx, domain, cfg.Port); err != nil {
			return fmt.Errorf("%w\n%s", err, out)
		}
		if fw := firewall.NewManager(func() int { return cfg.Port }); fw.Status(ctx).Active {
			for _, p := range []string{"80", "443"} {
				_, _ = fw.AddRule(ctx, firewall.RuleInput{Action: "allow", Port: p, Proto: "tcp"})
			}
		}
		cfg.PanelDomain = domain
		if err := cfg.Save(dir); err != nil {
			_ = m.DetachPanel(ctx)
			return err
		}
		fmt.Printf("\nDone. Open: https://%s%s/\nThe server's IP address no longer opens the panel.\n", domain, cfg.SecretPath)
		return nil
	case "unset":
		start := stopService()
		defer start()
		st, err := store.Open(dir)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cfg.PanelDomain = ""
		if err := cfg.Save(dir); err != nil {
			return err
		}
		if err := sites.NewManager(st).DetachPanel(ctx); err != nil {
			fmt.Println("Note: could not remove the Nginx site:", err)
		}
		fmt.Printf("Panel domain removed. The panel is back on %s:%d%s/\n", hostOrIP(cfg), cfg.Port, cfg.SecretPath)
		return nil
	}
	return errors.New("usage: appanel domain set <domain> | unset | show")
}

func cmdPath(args []string) error {
	dir := config.DefaultDataDir()
	cfg, err := config.Load(dir)
	if err != nil {
		return fmt.Errorf("cannot load config (run `appanel init` first): %w", err)
	}
	if len(args) == 0 || args[0] == "show" {
		fmt.Println("Login path:", cfg.SecretPath)
		return nil
	}
	if args[0] != "set" || len(args) < 2 {
		return errors.New("usage: appanel path show | set /your-secret-path | set --random")
	}
	p := args[1]
	if p == "--random" {
		if p, err = config.RandomSecretPath(); err != nil {
			return err
		}
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if !config.ValidSecretPath(p) {
		return errors.New("the login path must be 8-64 letters, digits, - or _ (for example /kkgwgifuiuriuriuyiug)")
	}
	cfg.SecretPath = p
	if err := cfg.Save(dir); err != nil {
		return err
	}
	fmt.Println("Login path set to", p)
	fmt.Println("Restart the panel to apply it: systemctl restart appanel")
	if cfg.PanelDomain != "" {
		fmt.Printf("Open: https://%s%s/\n", cfg.PanelDomain, p)
	}
	return nil
}

func hostOrIP(cfg *config.Config) string {
	if cfg.Host != "" {
		return cfg.Host
	}
	return "<server-ip>"
}
