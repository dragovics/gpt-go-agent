package webhook

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// JobStore persists webhook jobs using atomic temp-file + fsync + rename writes.
type JobStore struct {
	mu   sync.Mutex
	path string
	jobs map[string]Job
}

func NewJobStore(path string) *JobStore {
	return &JobStore{path: path, jobs: make(map[string]Job)}
}

func (s *JobStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	primary, primaryErr := os.ReadFile(s.path)
	if primaryErr == nil {
		var jobs map[string]Job
		if err := json.Unmarshal(primary, &jobs); err == nil {
			if jobs == nil {
				jobs = make(map[string]Job)
			}
			s.jobs = jobs
			return nil
		}
	} else if !os.IsNotExist(primaryErr) {
		return primaryErr
	}

	backup, backupErr := os.ReadFile(s.path + ".bak")
	if backupErr != nil {
		if os.IsNotExist(primaryErr) && os.IsNotExist(backupErr) {
			return nil
		}
		if primaryErr != nil && !os.IsNotExist(primaryErr) {
			return primaryErr
		}
		return backupErr
	}
	var jobs map[string]Job
	if err := json.Unmarshal(backup, &jobs); err != nil {
		if primaryErr == nil {
			return errors.New("primary and backup webhook stores are corrupt")
		}
		return err
	}
	if jobs == nil {
		jobs = make(map[string]Job)
	}
	s.jobs = jobs
	return nil
}

func (s *JobStore) Put(job Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job.ID == "" {
		return errors.New("job id is required")
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	job.UpdatedAt = time.Now().UTC()
	if s.jobs == nil {
		s.jobs = make(map[string]Job)
	}
	previous, existed := s.jobs[job.ID]
	s.jobs[job.ID] = job
	if err := s.writeLocked(); err != nil {
		if existed {
			s.jobs[job.ID] = previous
		} else {
			delete(s.jobs, job.ID)
		}
		return err
	}
	return nil
}

func (s *JobStore) List() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		out = append(out, job)
	}
	return out
}

func (s *JobStore) PruneBefore(cutoff time.Time) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := make([]string, 0)
	for id, job := range s.jobs {
		terminal := job.Status == StatusCompleted || job.Status == StatusRejected || job.Status == StatusFailed
		if terminal && job.UpdatedAt.Before(cutoff) {
			removed = append(removed, id)
		}
	}
	if len(removed) == 0 {
		return removed, nil
	}
	backup := make(map[string]Job, len(s.jobs))
	for id, job := range s.jobs {
		backup[id] = job
	}
	for _, id := range removed {
		delete(s.jobs, id)
	}
	if err := s.writeLocked(); err != nil {
		s.jobs = backup
		return nil, err
	}
	return removed, nil
}

func (s *JobStore) writeLocked() error {
	data, err := json.MarshalIndent(s.jobs, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if dir != "." {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(dir, ".jobs-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	rotated := false
	if _, err := os.Stat(s.path); err == nil {
		if err := os.Rename(s.path, s.path+".bak"); err != nil {
			return err
		}
		rotated = true
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		if rotated {
			_ = os.Rename(s.path+".bak", s.path)
		}
		return err
	}
	// #nosec G304 -- dir is derived from the service-configured store path and is not request-controlled.
	dirHandle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer dirHandle.Close()
	return dirHandle.Sync()
}
