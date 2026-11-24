package config

import (
	"os"
	"strings"
	"testing"
)

func TestLoadOrCreate(t *testing.T) {
	dir := t.TempDir()
	c, created, err := LoadOrCreate(dir, Options{Listen: "127.0.0.1"})
	if err != nil || !created {
		t.Fatalf("create: %v created=%v", err, created)
	}
	if c.Port < 20000 || c.Port > 59999 {
		t.Errorf("port %d out of range", c.Port)
	}
	if !strings.HasPrefix(c.SecretPath, "/") || len(c.SecretPath) != 21 {
		t.Errorf("secret path %q", c.SecretPath)
	}
	if st, _ := os.Stat(Path(dir)); st.Mode().Perm() != 0o600 {
		t.Errorf("config mode %v, want 0600", st.Mode().Perm())
	}
	c2, created2, err := LoadOrCreate(dir, Options{})
	if err != nil || created2 {
		t.Fatalf("reload: %v created=%v", err, created2)
	}
	if c2.Port != c.Port || c2.SecretPath != c.SecretPath {
		t.Error("reload returned different values")
	}
}

func TestSecretPathsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		p, err := RandomSecretPath()
		if err != nil || seen[p] {
			t.Fatalf("dup or error: %q %v", p, err)
		}
		seen[p] = true
	}
}

func TestValidate(t *testing.T) {
	good := Config{Port: 8080, SecretPath: "/abcdefgh1234"}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]Config{
		"port zero":     {Port: 0, SecretPath: "/abcdefgh1234"},
		"short secret":  {Port: 80, SecretPath: "/abc"},
		"no slash":      {Port: 80, SecretPath: "abcdefgh1234"},
		"traversal":     {Port: 80, SecretPath: "/../etc/passwd"},
		"half tls":      {Port: 80, SecretPath: "/abcdefgh1234", TLSCert: "x"},
		"nested secret": {Port: 80, SecretPath: "/abcdefgh/1234"},
	} {
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
