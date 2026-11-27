// Package databases manages MariaDB/MySQL and PostgreSQL on the same server:
// installing them, creating databases with their own user, resetting
// passwords and dropping. Every identifier is validated against a strict
// allowlist before it reaches SQL, and passwords are generated alphanumeric,
// so no value can break out of a statement.
package databases

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/usmanawa2153-beep/appanel/internal/execx"
)

var (
	ErrNotInstalled = errors.New("this database server is not installed; install it first")
	ErrExists       = errors.New("a database or user with that name already exists")
	ErrReserved     = errors.New("that name is reserved for the database server")
)

// Engines supported.
const (
	MySQL    = "mysql"
	Postgres = "postgres"
)

var identRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

var reserved = map[string]bool{
	"mysql": true, "root": true, "postgres": true, "information_schema": true, "performance_schema": true,
	"sys": true, "template0": true, "template1": true, "mariadb": true, "mariadb_sys": true, "debian_sys_maint": true,
	"pg_catalog": true, "public": true, "admin": true, "default": true,
}

// ValidateName checks a database/user name.
func ValidateName(n string) error {
	if !identRe.MatchString(n) {
		return errors.New("name must start with a letter and contain only lowercase letters, digits and underscores (32 characters max)")
	}
	if reserved[n] || strings.HasPrefix(n, "pg_") {
		return ErrReserved
	}
	return nil
}

// ValidEngine reports whether e is a known engine.
func ValidEngine(e string) bool { return e == MySQL || e == Postgres }

const pwAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// GeneratePassword returns a random 24-character alphanumeric password.
func GeneratePassword() (string, error) {
	b := make([]byte, 24)
	max := big.NewInt(int64(len(pwAlphabet)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = pwAlphabet[n.Int64()]
	}
	return string(b), nil
}

// Database is one entry in the list.
type Database struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

// EngineStatus is what the UI shows per engine.
type EngineStatus struct {
	Engine    string     `json:"engine"`
	Installed bool       `json:"installed"`
	Running   bool       `json:"running"`
	Version   string     `json:"version,omitempty"`
	Databases []Database `json:"databases"`
	Error     string     `json:"error,omitempty"`
}

// Manager runs the database CLIs.
type Manager struct {
	Runner   execx.Runner
	LookPath func(string) (string, error)
	mu       sync.Mutex
}

// NewManager returns a Manager with production settings.
func NewManager() *Manager { return &Manager{Runner: execx.Exec{}, LookPath: exec.LookPath} }

func (m *Manager) installed(engine string) bool {
	bin := "mysql"
	if engine == Postgres {
		bin = "psql"
	}
	_, err := m.LookPath(bin)
	return err == nil
}

// sql runs statements through the engine's own admin CLI as the local superuser.
// Statements are sent on stdin, never on the command line.
func (m *Manager) sql(ctx context.Context, engine, stmts string) (string, error) {
	if engine == Postgres {
		return m.Runner.RunInput(ctx, stmts, "runuser", "-u", "postgres", "--", "psql", "-X", "-q", "-At", "-v", "ON_ERROR_STOP=1")
	}
	return m.Runner.RunInput(ctx, stmts, "mysql", "-N", "-B")
}

// Status reports the state of one engine.
func (m *Manager) Status(ctx context.Context, engine string) EngineStatus {
	st := EngineStatus{Engine: engine, Databases: []Database{}}
	if !m.installed(engine) {
		return st
	}
	st.Installed = true
	dbs, err := m.List(ctx, engine)
	if err != nil {
		st.Error = execx.Tail(err.Error(), 300)
		return st
	}
	st.Running, st.Databases = true, dbs
	return st
}

// Install installs the server package with apt and makes sure it is running.
func (m *Manager) Install(ctx context.Context, engine string) (string, error) {
	if !ValidEngine(engine) {
		return "", errors.New("unknown engine")
	}
	pkgs := []string{"mariadb-server"}
	svc := "mariadb"
	if engine == Postgres {
		pkgs, svc = []string{"postgresql", "postgresql-client"}, "postgresql"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var log strings.Builder
	cmds := [][]string{
		{"apt-get", "update", "-qq"},
		append([]string{"apt-get", "install", "-y", "-qq"}, pkgs...),
		{"systemctl", "enable", "--now", svc},
	}
	for _, c := range cmds {
		out, err := m.Runner.Run(ctx, c[0], c[1:]...)
		log.WriteString("$ " + strings.Join(c, " ") + "\n" + execx.Tail(out, 2500) + "\n")
		if err != nil {
			return log.String(), fmt.Errorf("%s failed", strings.Join(c[:2], " "))
		}
	}
	return log.String(), nil
}

// List returns the user databases (system ones are hidden) with their size.
func (m *Manager) List(ctx context.Context, engine string) ([]Database, error) {
	if !ValidEngine(engine) {
		return nil, errors.New("unknown engine")
	}
	if !m.installed(engine) {
		return nil, ErrNotInstalled
	}
	var q string
	if engine == MySQL {
		q = `SELECT s.SCHEMA_NAME, COALESCE(SUM(t.DATA_LENGTH+t.INDEX_LENGTH),0)
FROM information_schema.SCHEMATA s LEFT JOIN information_schema.TABLES t ON t.TABLE_SCHEMA=s.SCHEMA_NAME
WHERE s.SCHEMA_NAME NOT IN ('information_schema','performance_schema','mysql','sys')
GROUP BY s.SCHEMA_NAME ORDER BY s.SCHEMA_NAME;`
	} else {
		q = `SELECT datname || '|' || pg_database_size(datname) FROM pg_database WHERE NOT datistemplate AND datname <> 'postgres' ORDER BY datname;`
	}
	out, err := m.sql(ctx, engine, q)
	if err != nil {
		return nil, errors.New(execx.Tail(out, 300))
	}
	return ParseList(out), nil
}

// ParseList parses "name<TAB or |>bytes" lines.
func ParseList(out string) []Database {
	dbs := []Database{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, size := line, ""
		if i := strings.IndexAny(line, "\t|"); i >= 0 {
			name, size = line[:i], strings.TrimSpace(line[i+1:])
		}
		n, _ := strconv.ParseInt(size, 10, 64)
		dbs = append(dbs, Database{Name: strings.TrimSpace(name), Bytes: n})
	}
	return dbs
}

// Created is returned once; the password is not stored anywhere.
type Created struct {
	Engine   string `json:"engine"`
	Name     string `json:"name"`
	User     string `json:"user"`
	Password string `json:"password"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
}

// Create makes a database plus a user with the same name that owns it, and
// returns the generated password.
func (m *Manager) Create(ctx context.Context, engine, name string) (Created, error) {
	if !ValidEngine(engine) {
		return Created{}, errors.New("unknown engine")
	}
	if err := ValidateName(name); err != nil {
		return Created{}, err
	}
	if !m.installed(engine) {
		return Created{}, ErrNotInstalled
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, err := m.List(ctx, engine)
	if err != nil {
		return Created{}, err
	}
	for _, d := range existing {
		if d.Name == name {
			return Created{}, ErrExists
		}
	}
	pw, err := GeneratePassword()
	if err != nil {
		return Created{}, err
	}
	res := Created{Engine: engine, Name: name, User: name, Password: pw, Host: "127.0.0.1", Port: 3306}
	var stmts string
	if engine == MySQL {
		stmts = fmt.Sprintf("CREATE DATABASE `%[1]s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;\n"+
			"CREATE USER '%[1]s'@'localhost' IDENTIFIED BY '%[2]s';\n"+
			"GRANT ALL PRIVILEGES ON `%[1]s`.* TO '%[1]s'@'localhost';\nFLUSH PRIVILEGES;\n", name, pw)
	} else {
		res.Port = 5432
		stmts = fmt.Sprintf("CREATE ROLE \"%[1]s\" LOGIN PASSWORD '%[2]s';\nCREATE DATABASE \"%[1]s\" OWNER \"%[1]s\";\n", name, pw)
	}
	if out, err := m.sql(ctx, engine, stmts); err != nil {
		// Roll back a half-made setup (e.g. the user already existed).
		_, _ = m.sql(ctx, engine, dropStmts(engine, name))
		if strings.Contains(strings.ToLower(out), "already exists") || strings.Contains(out, "1396") {
			return Created{}, ErrExists
		}
		return Created{}, fmt.Errorf("could not create the database: %s", execx.Tail(strings.ReplaceAll(out, pw, "***"), 300))
	}
	return res, nil
}

func dropStmts(engine, name string) string {
	if engine == MySQL {
		return fmt.Sprintf("DROP DATABASE IF EXISTS `%[1]s`;\nDROP USER IF EXISTS '%[1]s'@'localhost';\nFLUSH PRIVILEGES;\n", name)
	}
	return fmt.Sprintf("DROP DATABASE IF EXISTS \"%[1]s\";\nDROP ROLE IF EXISTS \"%[1]s\";\n", name)
}

// Drop removes a database and the user of the same name. It only drops a
// database that is in the listing, so a typo cannot touch anything else.
func (m *Manager) Drop(ctx context.Context, engine, name string) error {
	if !ValidEngine(engine) {
		return errors.New("unknown engine")
	}
	if err := ValidateName(name); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	dbs, err := m.List(ctx, engine)
	if err != nil {
		return err
	}
	found := false
	for _, d := range dbs {
		found = found || d.Name == name
	}
	if !found {
		return errors.New("database not found")
	}
	if out, err := m.sql(ctx, engine, dropStmts(engine, name)); err != nil {
		return fmt.Errorf("could not drop the database: %s", execx.Tail(out, 300))
	}
	return nil
}

// ResetPassword sets a new random password for the database's user.
func (m *Manager) ResetPassword(ctx context.Context, engine, name string) (Created, error) {
	if !ValidEngine(engine) {
		return Created{}, errors.New("unknown engine")
	}
	if err := ValidateName(name); err != nil {
		return Created{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	dbs, err := m.List(ctx, engine)
	if err != nil {
		return Created{}, err
	}
	found := false
	for _, d := range dbs {
		found = found || d.Name == name
	}
	if !found {
		return Created{}, errors.New("database not found")
	}
	pw, err := GeneratePassword()
	if err != nil {
		return Created{}, err
	}
	res := Created{Engine: engine, Name: name, User: name, Password: pw, Host: "127.0.0.1", Port: 3306}
	stmt := fmt.Sprintf("ALTER USER '%s'@'localhost' IDENTIFIED BY '%s';\nFLUSH PRIVILEGES;\n", name, pw)
	if engine == Postgres {
		res.Port = 5432
		stmt = fmt.Sprintf("ALTER ROLE \"%s\" WITH PASSWORD '%s';\n", name, pw)
	}
	if out, err := m.sql(ctx, engine, stmt); err != nil {
		return Created{}, fmt.Errorf("could not change the password: %s", execx.Tail(strings.ReplaceAll(out, pw, "***"), 300))
	}
	return res, nil
}

// DumpCommand returns the argv that writes a gzip-compressed dump of one
// database to outPath. Arguments are passed positionally, never interpolated
// into the shell script.
func DumpCommand(engine, db, outPath string) []string {
	script := `set -o pipefail; mysqldump --single-transaction --routines --events -- "$1" | gzip > "$2"`
	if engine == Postgres {
		script = `set -o pipefail; runuser -u postgres -- pg_dump --clean --if-exists -- "$1" | gzip > "$2"`
	}
	return []string{"bash", "-c", script, "appanel-dump", db, outPath}
}

// RestoreCommand returns the argv that loads a gzip dump into a database.
func RestoreCommand(engine, db, inPath string) []string {
	script := `set -o pipefail; gunzip -c -- "$2" | mysql -- "$1"`
	if engine == Postgres {
		script = `set -o pipefail; gunzip -c -- "$2" | runuser -u postgres -- psql -X -q -v ON_ERROR_STOP=1 -d "$1"`
	}
	return []string{"bash", "-c", script, "appanel-restore", db, inPath}
}

// Dump writes the dump file for db.
func (m *Manager) Dump(ctx context.Context, engine, db, outPath string) error {
	if err := ValidateName(db); err != nil {
		return err
	}
	if !ValidEngine(engine) {
		return errors.New("unknown engine")
	}
	a := DumpCommand(engine, db, outPath)
	if out, err := m.Runner.Run(ctx, a[0], a[1:]...); err != nil {
		return fmt.Errorf("dump failed: %s", execx.Tail(out, 300))
	}
	return nil
}

// Restore loads a dump into an existing database.
func (m *Manager) Restore(ctx context.Context, engine, db, inPath string) error {
	if err := ValidateName(db); err != nil {
		return err
	}
	if !ValidEngine(engine) {
		return errors.New("unknown engine")
	}
	a := RestoreCommand(engine, db, inPath)
	if out, err := m.Runner.Run(ctx, a[0], a[1:]...); err != nil {
		return fmt.Errorf("restore failed: %s", execx.Tail(out, 300))
	}
	return nil
}
