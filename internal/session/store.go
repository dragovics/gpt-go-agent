package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type DurableStore struct {
	mu       sync.Mutex
	path     string
	sessions map[string]Session
}

func NewDurableStore(path string) *DurableStore {
	return &DurableStore{path: path, sessions: make(map[string]Session)}
}

func (s *DurableStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var stored map[string]Session
	if err := json.Unmarshal(data, &stored); err != nil {
		return err
	}
	s.sessions = stored
	return nil
}

func (s *DurableStore) Put(v Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v.ID == "" {
		return errors.New("session id is required")
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now().UTC()
	}
	v.UpdatedAt = time.Now().UTC()
	if s.sessions == nil {
		s.sessions = make(map[string]Session)
	}
	s.sessions[v.ID] = v
	return s.writeLocked()
}

func (s *DurableStore) Get(id string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.sessions[id]
	if !ok {
		return Session{}, errors.New("session not found")
	}
	return v, nil
}

func (s *DurableStore) List() []Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Session, 0, len(s.sessions))
	for _, v := range s.sessions {
		out = append(out, v)
	}
	return out
}

func (s *DurableStore) writeLocked() error {
	data, err := json.Marshal(s.sessions)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if dir != "." {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(dir, ".session-*.tmp")
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
	return os.Rename(tmpName, s.path)
}
