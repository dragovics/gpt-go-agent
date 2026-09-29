package webhook

import (
	"sync"
	"time"
)

type Job struct {
	ID string
	Type string
	SessionID string
	EnvironmentID string
	RemoteURL string
	CreatedAt time.Time
}

type Queue struct {
	mu sync.Mutex
	ch chan Job
	seen map[string]struct{}
}

func NewQueue(size int) *Queue {
	if size < 1 { size = 128 }
	return &Queue{ch: make(chan Job, size), seen: make(map[string]struct{})}
}

func (q *Queue) Enqueue(j Job) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if j.ID == "" { return false }
	if _, ok := q.seen[j.ID]; ok { return true }
	select {
	case q.ch <- j:
		q.seen[j.ID] = struct{}{}
		return true
	default:
		return false
	}
}

func (q *Queue) Next() <-chan Job { return q.ch }
