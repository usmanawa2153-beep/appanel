// Package backups creates, lists, restores and deletes backups of websites
// (a tar.gz of the site's files) and databases (a gzip SQL dump). Backups live
// in one directory that only root can read.
package backups

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/usmanawa2153-beep/appanel/internal/databases"
	"github.com/usmanawa2153-beep/appanel/internal/execx"
	"github.com/usmanawa2153-beep/appanel/internal/store"
)

var (
	ErrNotFound = errors.New("backup not found")
	ErrNoFiles  = errors.New("this site has no files of its own to back up (it is a reverse proxy)")
)

// Item is one backup file.
type Item struct {
	Name    string    `json:"name"`
	Kind    string    `json:"kind"`   // site | db
	Engine  string    `json:"engine"` // db only
	Target  string    `json:"target"` // domain or database name
	Bytes   int64     `json:"bytes"`
	Created time.Time `json:"created"`
}

var nameRe = regexp.MustCompile(`^(site|db)-(?:(mysql|postgres)-)?([a-z0-9][a-z0-9._-]{0,252})-(\d{8}-\d{6})\.(tar|sql)\.gz$`)

const stamp = "20060102-150405"

// ParseName validates a backup file name and splits it. Anything with a path
// separator or that does not match the exact pattern is rejected, so a name
// from the network can never point outside the backup directory.
func ParseName(n string) (Item, bool) {
	m := nameRe.FindStringSubmatch(n)
	if m == nil || strings.ContainsAny(n, `/\`) {
		return Item{}, false
	}
	it := Item{Name: n, Kind: m[1], Engine: m[2], Target: m[3]}
	if (it.Kind == "site") != (m[5] == "tar") || (it.Kind == "db") != (m[2] != "") {
		return Item{}, false
	}
	t, err := time.ParseInLocation(stamp, m[4], time.UTC)
	if err != nil {
		return Item{}, false
	}
	it.Created = t
	return it, true
}

// Manager owns the backup directory.
type Manager struct {
	Store  *store.Store
	DB     *databases.Manager
	Runner execx.Runner
	Dir    string // /var/backups/appanel
	Now    func() time.Time
	mu     sync.Mutex
}

// NewManager returns a Manager with production settings.
func NewManager(st *store.Store, db *databases.Manager) *Manager {
	return &Manager{Store: st, DB: db, Runner: execx.Exec{}, Dir: "/var/backups/appanel", Now: time.Now}
}

func (m *Manager) ensureDir() error { return os.MkdirAll(m.Dir, 0o700) }

// List returns all backups, newest first.
func (m *Manager) List() []Item {
	items := []Item{}
	ents, err := os.ReadDir(m.Dir)
	if err != nil {
		return items
	}
	for _, e := range ents {
		it, ok := ParseName(e.Name())
		if !ok || !e.Type().IsRegular() {
			continue
		}
		if fi, err := e.Info(); err == nil {
			it.Bytes = fi.Size()
			items = append(items, it)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Created.After(items[j].Created) })
	return items
}

// Path returns the full path of a backup after validating its name.
func (m *Manager) Path(name string) (string, error) {
	if _, ok := ParseName(name); !ok {
		return "", ErrNotFound
	}
	p := filepath.Join(m.Dir, name)
	if fi, err := os.Lstat(p); err != nil || !fi.Mode().IsRegular() {
		return "", ErrNotFound
	}
	return p, nil
}

// finish moves a finished temp file into place, or removes it on failure.
func finish(tmp, final string, err error) error {
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if fi, e := os.Stat(tmp); e != nil || fi.Size() == 0 {
		_ = os.Remove(tmp)
		return errors.New("the backup came out empty and was discarded")
	}
	if e := os.Chmod(tmp, 0o600); e != nil {
		_ = os.Remove(tmp)
		return e
	}
	return os.Rename(tmp, final)
}

// BackupSite archives the files of a website.
func (m *Manager) BackupSite(ctx context.Context, siteID int64) (Item, error) {
	s, err := m.Store.SiteByID(siteID)
	if err != nil {
		return Item{}, errors.New("site not found")
	}
	if s.Root == "" {
		return Item{}, ErrNoFiles
	}
	if fi, err := os.Stat(s.Root); err != nil || !fi.IsDir() {
		return Item{}, errors.New("the site's folder does not exist yet")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureDir(); err != nil {
		return Item{}, err
	}
	name := fmt.Sprintf("site-%s-%s.tar.gz", s.Domain, m.Now().UTC().Format(stamp))
	if _, ok := ParseName(name); !ok {
		return Item{}, errors.New("this domain name cannot be used in a backup file name")
	}
	final := filepath.Join(m.Dir, name)
	tmp := final + ".part"
	out, err := m.Runner.Run(ctx, "tar", "-czf", tmp, "-C", s.Root, ".")
	if err != nil {
		err = fmt.Errorf("tar failed: %s", execx.Tail(out, 300))
	}
	if err := finish(tmp, final, err); err != nil {
		return Item{}, err
	}
	it, _ := ParseName(name)
	if fi, err := os.Stat(final); err == nil {
		it.Bytes = fi.Size()
	}
	return it, nil
}

// BackupDB dumps one database.
func (m *Manager) BackupDB(ctx context.Context, engine, db string) (Item, error) {
	if !databases.ValidEngine(engine) {
		return Item{}, errors.New("unknown engine")
	}
	if err := databases.ValidateName(db); err != nil {
		return Item{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureDir(); err != nil {
		return Item{}, err
	}
	name := fmt.Sprintf("db-%s-%s-%s.sql.gz", engine, db, m.Now().UTC().Format(stamp))
	final := filepath.Join(m.Dir, name)
	tmp := final + ".part"
	err := m.DB.Dump(ctx, engine, db, tmp)
	if err := finish(tmp, final, err); err != nil {
		return Item{}, err
	}
	it, _ := ParseName(name)
	if fi, err := os.Stat(final); err == nil {
		it.Bytes = fi.Size()
	}
	return it, nil
}

// Delete removes a backup file.
func (m *Manager) Delete(name string) error {
	p, err := m.Path(name)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return os.Remove(p)
}

// Restore puts a backup back: files are extracted over the site's folder,
// a dump is loaded into the (already existing) database.
func (m *Manager) Restore(ctx context.Context, name string) error {
	p, err := m.Path(name)
	if err != nil {
		return err
	}
	it, _ := ParseName(name)
	m.mu.Lock()
	defer m.mu.Unlock()
	if it.Kind == "db" {
		return m.DB.Restore(ctx, it.Engine, it.Target, p)
	}
	for _, s := range m.Store.ListSites() {
		if s.Domain == it.Target && s.Root != "" {
			if fi, err := os.Stat(s.Root); err != nil || !fi.IsDir() {
				return errors.New("the site's folder no longer exists")
			}
			// --no-same-owner keeps ownership sane; absolute paths and ".."
			// are refused by tar by default, and the archive is one we made.
			out, err := m.Runner.Run(ctx, "tar", "-xzf", p, "-C", s.Root, "--no-same-owner")
			if err != nil {
				return fmt.Errorf("tar failed: %s", execx.Tail(out, 300))
			}
			return nil
		}
	}
	return fmt.Errorf("the site %s no longer exists; create it again before restoring", it.Target)
}

// Prune keeps only the newest `keep` backups of each target and kind.
func (m *Manager) Prune(keep int) (int, error) {
	if keep < 1 {
		return 0, errors.New("keep must be at least 1")
	}
	seen := map[string]int{}
	removed := 0
	for _, it := range m.List() { // newest first
		key := it.Kind + "/" + it.Engine + "/" + it.Target
		seen[key]++
		if seen[key] > keep {
			if err := m.Delete(it.Name); err != nil {
				return removed, err
			}
			removed++
		}
	}
	return removed, nil
}

// RunAll backs up every site that has files and every database of the
// installed engines, then prunes to the newest `keep` per target. It carries
// on after a failure and returns one line per problem.
func (m *Manager) RunAll(ctx context.Context, keep int) (made int, problems []string) {
	for _, s := range m.Store.ListSites() {
		if s.Root == "" {
			continue
		}
		if _, err := m.BackupSite(ctx, s.ID); err != nil {
			problems = append(problems, "site "+s.Domain+": "+err.Error())
		} else {
			made++
		}
	}
	for _, eng := range []string{databases.MySQL, databases.Postgres} {
		dbs, err := m.DB.List(ctx, eng)
		if err != nil {
			if !errors.Is(err, databases.ErrNotInstalled) {
				problems = append(problems, eng+": "+err.Error())
			}
			continue
		}
		for _, d := range dbs {
			if _, err := m.BackupDB(ctx, eng, d.Name); err != nil {
				problems = append(problems, eng+" database "+d.Name+": "+err.Error())
			} else {
				made++
			}
		}
	}
	if keep > 0 {
		if _, err := m.Prune(keep); err != nil {
			problems = append(problems, "prune: "+err.Error())
		}
	}
	return made, problems
}
