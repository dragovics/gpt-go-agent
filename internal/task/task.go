package task

import (
	"sync"
	"time"
)

type State string

const (
	Pending   State = "pending"
	Running   State = "running"
	Waiting   State = "waiting"
	Failed    State = "failed"
	Completed State = "completed"
)

type Task struct {
	ID        string
	State     State
	CreatedAt time.Time
	UpdatedAt time.Time
}
type Store struct {
	mu    sync.RWMutex
	tasks map[string]Task
}

func NewStore() *Store { return &Store{tasks: make(map[string]Task)} }
func (s *Store) Put(t Task) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t.UpdatedAt = time.Now().UTC()
	s.tasks[t.ID] = t
}
func (s *Store) Get(id string) (Task, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[id]
	return t, ok
}
