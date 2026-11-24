// Command appanel is the Appanel server and admin CLI.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/usmanawa2153-beep/appanel/internal/auth"
	"github.com/usmanawa2153-beep/appanel/internal/config"
	"github.com/usmanawa2153-beep/appanel/internal/server"
	"github.com/usmanawa2153-beep/appanel/internal/store"
	"github.com/usmanawa2153-beep/appanel/internal/tlsutil"
	"github.com/usmanawa2153-beep/appanel/web"
)

// version is set at build time: -ldflags "-X main.version=v0.1.0".
var version = "dev"

const usage = `Appanel ` + "%s" + ` - self-hosted hosting control panel

Usage:
  appanel init [--port N] [--listen ADDR] [--no-tls] [--host NAME_OR_IP]...
  appanel serve
  appanel reset-password [--user NAME]
  appanel disable-2fa [--user NAME]
  appanel info
  appanel version

Data directory: $APPANEL_DATA (default /var/lib/appanel as root, ~/.appanel otherwise)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, usage, version)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "init":
		err = cmdInit(os.Args[2:])
	case "serve":
		err = cmdServe()
	case "reset-password":
		err = cmdReset(os.Args[2:])
	case "disable-2fa":
		err = cmdDisable2FA(os.Args[2:])
	case "info":
		err = cmdInfo(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println("appanel", version)
	case "help", "--help", "-h":
		fmt.Printf(usage, version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		fmt.Fprintf(os.Stderr, usage, version)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type multiFlag []string

func (m *multiFlag) String() string     { return fmt.Sprint(*m) }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	port := fs.Int("port", 0, "port to listen on (default: random free port)")
	listen := fs.String("listen", "0.0.0.0", "address to listen on")
	noTLS := fs.Bool("no-tls", false, "serve plain HTTP (only behind a TLS-terminating reverse proxy)")
	var hosts multiFlag
	fs.Var(&hosts, "host", "extra DNS name or IP for the self-signed certificate (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir := config.DefaultDataDir()
	cfg, created, err := config.LoadOrCreate(dir, config.Options{Listen: *listen, Port: *port})
	if err != nil {
		return err
	}
	if created {
		if len(hosts) > 0 {
			cfg.Host = hosts[0]
		}
		if !*noTLS {
			cert, key, err := tlsutil.EnsureSelfSigned(dir, hosts)
			if err != nil {
				return err
			}
			cfg.TLSCert, cfg.TLSKey = cert, key
		}
		if err := cfg.Save(dir); err != nil {
			return err
		}
	}
	st, err := store.Open(dir)
	if err != nil {
		return err
	}
	if st.CountUsers() == 0 {
		pw := auth.RandomPassword(16)
		hash, err := auth.HashPassword(pw)
		if err != nil {
			return err
		}
		if _, err := st.CreateUser(store.User{Username: "admin", Role: "admin", PasswordHash: hash}); err != nil {
			return err
		}
		fmt.Println("Appanel initialised.")
		printAccess(cfg)
		fmt.Printf("  Username: admin\n  Password: %s\n\n", pw)
		fmt.Println("Save this password now; it is not stored anywhere in plain text.")
		fmt.Println("Change it after signing in, and enable 2FA under Settings.")
		return nil
	}
	fmt.Println("Appanel is already initialised.")
	printAccess(cfg)
	return nil
}

func scheme(cfg *config.Config) string {
	if cfg.TLSCert != "" {
		return "https"
	}
	return "http"
}

func printAccess(cfg *config.Config) {
	host := cfg.Host
	if host == "" {
		host = cfg.Listen
	}
	if host == "0.0.0.0" || host == "::" || host == "" {
		host = "<your-server-ip>"
	}
	fmt.Printf("\n  URL:      %s://%s%s/\n", scheme(cfg), net.JoinHostPort(host, fmt.Sprint(cfg.Port)), cfg.SecretPath)
	if cfg.TLSCert != "" {
		fmt.Println("  (self-signed certificate: your browser will warn once; replace it in the config for a real one)")
	}
}

func cmdInfo(args []string) error {
	fs := flag.NewFlagSet("info", flag.ContinueOnError)
	portOnly := fs.Bool("port", false, "print only the listening port (for scripts)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(config.DefaultDataDir())
	if err != nil {
		return fmt.Errorf("not initialised (run `appanel init`): %w", err)
	}
	if *portOnly {
		fmt.Println(cfg.Port)
		return nil
	}
	printAccess(cfg)
	fmt.Println()
	return nil
}

func cmdServe() error {
	dir := config.DefaultDataDir()
	cfg, err := config.Load(dir)
	if err != nil {
		return fmt.Errorf("cannot load config (run `appanel init` first): %w", err)
	}
	st, err := store.Open(dir)
	if err != nil {
		return err
	}
	if st.CountUsers() == 0 {
		return errors.New("no admin account yet; run `appanel init`")
	}
	srv, err := server.New(cfg, st, web.UI(), version)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("appanel %s listening on %s (%s)", version, cfg.Addr(), scheme(cfg))
	if err := srv.Run(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	log.Println("shut down cleanly")
	return nil
}

func cmdReset(args []string) error {
	fs := flag.NewFlagSet("reset-password", flag.ContinueOnError)
	user := fs.String("user", "admin", "account name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := store.Open(config.DefaultDataDir())
	if err != nil {
		return err
	}
	u, err := st.UserByName(*user)
	if err != nil {
		return fmt.Errorf("user %q not found", *user)
	}
	pw := auth.RandomPassword(16)
	if u.PasswordHash, err = auth.HashPassword(pw); err != nil {
		return err
	}
	if err := st.UpdateUser(u); err != nil {
		return err
	}
	fmt.Printf("New password for %s: %s\n", u.Username, pw)
	fmt.Println("Restart the panel (systemctl restart appanel) to sign out existing sessions.")
	return nil
}

func cmdDisable2FA(args []string) error {
	fs := flag.NewFlagSet("disable-2fa", flag.ContinueOnError)
	user := fs.String("user", "admin", "account name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := store.Open(config.DefaultDataDir())
	if err != nil {
		return err
	}
	u, err := st.UserByName(*user)
	if err != nil {
		return fmt.Errorf("user %q not found", *user)
	}
	u.TOTPEnabled, u.TOTPSecret = false, ""
	if err := st.UpdateUser(u); err != nil {
		return err
	}
	fmt.Printf("2FA disabled for %s.\n", u.Username)
	return nil
}
