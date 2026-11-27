// Package cron manages scheduled jobs. Jobs live in the panel's store and are
// rendered into /etc/cron.d/appanel, which the system cron daemon picks up by
// itself (no reload needed).
package cron

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/usmanawa2153-beep/appanel/internal/execx"
	"github.com/usmanawa2153-beep/appanel/internal/store"
)

var ErrNotFound = errors.New("job not found")

var specials = map[string]bool{
	"@reboot": true, "@hourly": true, "@daily": true, "@weekly": true,
	"@monthly": true, "@yearly": true, "@annually": true, "@midnight": true,
}

// ValidateSchedule accepts the five-field cron syntax (numbers, ranges, lists,
// steps and *) or one of the @keywords, and returns it with single spaces.
func ValidateSchedule(s string) (string, error) {
	f := strings.Fields(s)
	if len(f) == 1 && specials[strings.ToLower(f[0])] {
		return strings.ToLower(f[0]), nil
	}
	if len(f) != 5 {
		return "", errors.New("schedule needs 5 fields: minute hour day-of-month month day-of-week (example: 30 2 * * *)")
	}
	limits := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	names := [5]string{"minute", "hour", "day of month", "month", "day of week"}
	for i, field := range f {
		if err := validField(field, limits[i][0], limits[i][1]); err != nil {
			return "", fmt.Errorf("%s field %q: %v", names[i], field, err)
		}
	}
	return strings.Join(f, " "), nil
}

func validField(f string, lo, hi int) error {
	for _, item := range strings.Split(f, ",") {
		base, step, hasStep := strings.Cut(item, "/")
		if hasStep {
			n, err := strconv.Atoi(step)
			if err != nil || n < 1 || n > hi {
				return errors.New("invalid step")
			}
		}
		if base == "*" {
			continue
		}
		a, b, isRange := strings.Cut(base, "-")
		x, err := strconv.Atoi(a)
		if err != nil || x < lo || x > hi {
			return fmt.Errorf("must be between %d and %d", lo, hi)
		}
		if isRange {
			y, err := strconv.Atoi(b)
			if err != nil || y < lo || y > hi || y < x {
				return fmt.Errorf("invalid range, values must be %d-%d", lo, hi)
			}
		}
	}
	return nil
}

// ValidateCommand rejects anything that could break out of a single cron line.
func ValidateCommand(c string) (string, error) {
	c = strings.TrimSpace(c)
	if c == "" {
		return "", errors.New("command is required")
	}
	if len(c) > 2000 {
		return "", errors.New("command is too long (2000 characters max)")
	}
	if strings.ContainsAny(c, "\n\r\x00") {
		return "", errors.New("command must be a single line (put multi-line scripts in a file and run that)")
	}
	return c, nil
}

// ValidateName checks the display name.
func ValidateName(n string) (string, error) {
	n = strings.TrimSpace(n)
	if n == "" {
		return "", errors.New("name is required")
	}
	if len(n) > 60 {
		return "", errors.New("name is too long (60 characters max)")
	}
	for _, r := range n {
		if r < 0x20 || r == 0x7f {
			return "", errors.New("name contains control characters")
		}
	}
	return n, nil
}

// Render builds the /etc/cron.d file. Percent signs are escaped because cron
// treats an unescaped % as a newline.
func Render(jobs []store.CronJob) string {
	var b strings.Builder
	b.WriteString("# Managed by Appanel. Manual edits to this file will be overwritten.\n")
	b.WriteString("SHELL=/bin/sh\nPATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin\n")
	for _, j := range jobs {
		if !j.Enabled {
			continue
		}
		fmt.Fprintf(&b, "\n# job %d: %s\n", j.ID, strings.NewReplacer("\n", " ", "\r", " ").Replace(j.Name))
		fmt.Fprintf(&b, "%s root %s\n", j.Schedule, strings.ReplaceAll(j.Command, "%", `\%`))
	}
	return b.String()
}

// Manager owns the cron file.
type Manager struct {
	Store  *store.Store
	Runner execx.Runner
	Path   string // /etc/cron.d/appanel
	mu     sync.Mutex
}

// NewManager returns a Manager with production settings.
func NewManager(st *store.Store) *Manager {
	return &Manager{Store: st, Runner: execx.Exec{}, Path: "/etc/cron.d/appanel"}
}

func (m *Manager) write() error {
	if err := os.MkdirAll(filepath.Dir(m.Path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(m.Path), ".appanel-cron-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(Render(m.Store.ListCronJobs())); err != nil {
		tmp.Close()
		return err
	}
	// cron ignores files that are group/world writable or not owned by root.
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), m.Path)
}

// Input is a job as submitted from the UI.
type Input struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	Enabled  bool   `json:"enabled"`
}

func validate(in Input) (store.CronJob, error) {
	var j store.CronJob
	var err error
	if j.Name, err = ValidateName(in.Name); err != nil {
		return j, err
	}
	if j.Schedule, err = ValidateSchedule(in.Schedule); err != nil {
		return j, err
	}
	if j.Command, err = ValidateCommand(in.Command); err != nil {
		return j, err
	}
	j.Enabled = in.Enabled
	return j, nil
}

// List returns all jobs.
func (m *Manager) List() []store.CronJob {
	jobs := m.Store.ListCronJobs()
	if jobs == nil {
		jobs = []store.CronJob{}
	}
	return jobs
}

// Create adds a job and rewrites the cron file.
func (m *Manager) Create(in Input) (store.CronJob, error) {
	j, err := validate(in)
	if err != nil {
		return j, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	j, err = m.Store.CreateCronJob(j)
	if err != nil {
		return j, err
	}
	if err := m.write(); err != nil {
		_ = m.Store.DeleteCronJob(j.ID)
		return store.CronJob{}, fmt.Errorf("could not write %s: %w", m.Path, err)
	}
	return j, nil
}

// Update replaces a job's fields.
func (m *Manager) Update(id int64, in Input) (store.CronJob, error) {
	j, err := validate(in)
	if err != nil {
		return j, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old, err := m.Store.CronJobByID(id)
	if err != nil {
		return j, ErrNotFound
	}
	j.ID, j.CreatedAt = id, old.CreatedAt
	if err := m.Store.UpdateCronJob(j); err != nil {
		return j, err
	}
	if err := m.write(); err != nil {
		_ = m.Store.UpdateCronJob(old)
		return store.CronJob{}, fmt.Errorf("could not write %s: %w", m.Path, err)
	}
	return j, nil
}

// Delete removes a job.
func (m *Manager) Delete(id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, err := m.Store.CronJobByID(id)
	if err != nil {
		return ErrNotFound
	}
	if err := m.Store.DeleteCronJob(id); err != nil {
		return err
	}
	if err := m.write(); err != nil {
		_, _ = m.Store.CreateCronJob(old) // best effort restore (gets a new ID)
		return fmt.Errorf("could not write %s: %w", m.Path, err)
	}
	return nil
}

// RunNow runs the job's command once, right now, as the panel's user (root),
// and returns its output. It gives up after five minutes.
func (m *Manager) RunNow(ctx context.Context, id int64) (string, error) {
	j, err := m.Store.CronJobByID(id)
	if err != nil {
		return "", ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	out, err := m.Runner.Run(ctx, "/bin/sh", "-c", j.Command)
	return execx.Tail(out, 8000), err
}
