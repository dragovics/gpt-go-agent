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
	journal *Journal
}

func NewQueue(size int, journal ...*Journal) *Queue {
	if size < 1 { size = 128 }
	q := &Queue{ch: make(chan Job, size), seen: make(map[string]struct{})}
	if len(journal) > 0 { q.journal = journal[0] }
	return q
}

func (q *Queue) Enqueue(j Job) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if j.ID == "" { return false }
	if _, ok := q.seen[j.ID]; ok { return true }
	if q.journal != nil { if err := q.journal.Enqueue(j); err != nil { return false } }
	select {
	case q.ch <- j:
		q.seen[j.ID] = struct{}{}
		return true
	default:
		return false
	}
}

func (q *Queue) Next() <-chan Job { return q.ch }

func (q *Queue) Recover() error {
	if q.journal == nil { return nil }
	jobs, err := q.journal.Pending(); if err != nil { return err }
	for _, j := range jobs {
		if _, ok := q.seen[j.ID]; ok { continue }
		select { case q.ch <- j: q.seen[j.ID] = struct{}{}; default: return nil }
	}
	return nil
}

func (q *Queue) Complete(j Job) { if q.journal != nil { _ = q.journal.Done(j) } }
func (q *Queue) Dead(j Job, err error) { if q.journal != nil { _ = q.journal.Dead(j, err) } }
