// Package config loads and creates the Appanel configuration file.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

// Config is the on-disk panel configuration (config.json in the data dir).
type Config struct {
	Listen       string `json:"listen"`
	Host         string `json:"host,omitempty"` // public name or IP shown in the access URL
	Port         int    `json:"port"`
	SecretPath   string `json:"secret_path"`
	TLSCert      string `json:"tls_cert,omitempty"`
	TLSKey       string `json:"tls_key,omitempty"`
	FileRoot     string `json:"file_root"`
	SessionHours int    `json:"session_hours"`
}

// Options are the values that can be chosen at `appanel init` time.
type Options struct {
	Listen string
	Port   int // 0 = pick a random free port
}

var secretPathRe = regexp.MustCompile(`^/[A-Za-z0-9_-]{8,64}$`)

// DefaultDataDir returns the data directory: $APPANEL_DATA, /var/lib/appanel
// for root, otherwise ~/.appanel.
func DefaultDataDir() string {
	if v := os.Getenv("APPANEL_DATA"); v != "" {
		return v
	}
	if os.Geteuid() == 0 {
		return "/var/lib/appanel"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".appanel"
	}
	return filepath.Join(home, ".appanel")
}

// Path returns the config file path inside dir.
func Path(dir string) string { return filepath.Join(dir, "config.json") }

// Addr returns host:port for the HTTP listener.
func (c *Config) Addr() string { return net.JoinHostPort(c.Listen, strconv.Itoa(c.Port)) }

func (c *Config) applyDefaults() {
	if c.Listen == "" {
		c.Listen = "0.0.0.0"
	}
	if c.FileRoot == "" {
		c.FileRoot = "/"
	}
	if c.SessionHours <= 0 {
		c.SessionHours = 12
	}
}

// Validate checks that the configuration is usable.
func (c *Config) Validate() error {
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("config: invalid port %d", c.Port)
	}
	if !secretPathRe.MatchString(c.SecretPath) {
		return errors.New("config: secret_path must look like /abc123xyz (8-64 letters, digits, - or _)")
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return errors.New("config: tls_cert and tls_key must be set together")
	}
	return nil
}

// Load reads config.json from dir.
func Load(dir string) (*Config, error) {
	b, err := os.ReadFile(Path(dir))
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Save writes the config atomically with 0600 permissions.
func (c *Config) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), Path(dir))
}

// LoadOrCreate loads the config, creating a fresh one (random port and secret
// path) when none exists. The bool reports whether it was created.
func LoadOrCreate(dir string, o Options) (*Config, bool, error) {
	c, err := Load(dir)
	if err == nil {
		return c, false, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, false, err
	}
	c = &Config{Listen: o.Listen, Port: o.Port}
	c.applyDefaults()
	if c.Port == 0 {
		if c.Port, err = RandomPort(c.Listen); err != nil {
			return nil, false, err
		}
	}
	if c.SecretPath, err = RandomSecretPath(); err != nil {
		return nil, false, err
	}
	if err := c.Validate(); err != nil {
		return nil, false, err
	}
	if err := c.Save(dir); err != nil {
		return nil, false, err
	}
	return c, true, nil
}

// RandomSecretPath returns a path like /3f9a1c... with 80 bits of entropy.
func RandomSecretPath() (string, error) {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "/" + hex.EncodeToString(b), nil
}

// RandomPort picks a random free TCP port in 20000-59999.
func RandomPort(listen string) (int, error) {
	for i := 0; i < 100; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(40000))
		if err != nil {
			return 0, err
		}
		port := 20000 + int(n.Int64())
		l, err := net.Listen("tcp", net.JoinHostPort(listen, strconv.Itoa(port)))
		if err != nil {
			continue
		}
		l.Close()
		return port, nil
	}
	return 0, errors.New("could not find a free port")
}
