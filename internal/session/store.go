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
	mu sync.Mutex
	path string
}

func NewDurableStore(path string) *DurableStore { return &DurableStore{path:path} }

func (s *DurableStore) Put(v Session) error {
	s.mu.Lock(); defer s.mu.Unlock()
	v.UpdatedAt = time.Now().UTC()
	data, err := json.Marshal(v); if err != nil { return err }
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil && filepath.Dir(s.path) != "." { return err }
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data,'\n'), 0600); err != nil { return err }
	return os.Rename(tmp, s.path)
}

func (s *DurableStore) Get(id string) (Session, error) {
	s.mu.Lock(); defer s.mu.Unlock()
	data, err := os.ReadFile(s.path); if err != nil { return Session{}, err }
	var v Session
	if err := json.Unmarshal(data, &v); err != nil { return Session{}, err }
	if v.ID != id { return Session{}, errors.New("session not found") }
	return v, nil
}
