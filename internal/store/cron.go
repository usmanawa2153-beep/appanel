package store

import "time"

// CronJob is a scheduled command managed by the panel.
type CronJob struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Schedule  string    `json:"schedule"`
	Command   string    `json:"command"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

// ListCronJobs returns a copy of all jobs.
func (s *Store) ListCronJobs() []CronJob {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]CronJob(nil), s.d.CronJobs...)
}

// CronJobByID finds a job.
func (s *Store) CronJobByID(id int64) (CronJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, j := range s.d.CronJobs {
		if j.ID == id {
			return j, nil
		}
	}
	return CronJob{}, ErrNotFound
}

// CreateCronJob adds a job.
func (s *Store) CreateCronJob(j CronJob) (CronJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j.ID = s.d.NextID
	s.d.NextID++
	if j.CreatedAt.IsZero() {
		j.CreatedAt = time.Now().UTC()
	}
	s.d.CronJobs = append(s.d.CronJobs, j)
	if err := s.save(); err != nil {
		s.d.CronJobs = s.d.CronJobs[:len(s.d.CronJobs)-1]
		s.d.NextID--
		return CronJob{}, err
	}
	return j, nil
}

// UpdateCronJob replaces the job with the same ID.
func (s *Store) UpdateCronJob(j CronJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.d.CronJobs {
		if s.d.CronJobs[i].ID == j.ID {
			old := s.d.CronJobs[i]
			s.d.CronJobs[i] = j
			if err := s.save(); err != nil {
				s.d.CronJobs[i] = old
				return err
			}
			return nil
		}
	}
	return ErrNotFound
}

// DeleteCronJob removes a job.
func (s *Store) DeleteCronJob(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.d.CronJobs {
		if s.d.CronJobs[i].ID == id {
			old := append([]CronJob(nil), s.d.CronJobs...)
			s.d.CronJobs = append(s.d.CronJobs[:i], s.d.CronJobs[i+1:]...)
			if err := s.save(); err != nil {
				s.d.CronJobs = old
				return err
			}
			return nil
		}
	}
	return ErrNotFound
}
