package webhook

import (
	"sort"
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
	mu      sync.Mutex
	ch      chan Job
	seen    map[string]time.Time
	journal *Journal
	maxSeen int
	seenTTL time.Duration
}

func NewQueue(size int, journal ...*Journal) *Queue {
	if size < 1 {
		size = 128
	}
	q := &Queue{
		ch:      make(chan Job, size),
		seen:    make(map[string]time.Time),
		maxSeen: 10000,
		seenTTL: 24 * time.Hour,
	}
	if len(journal) > 0 {
		q.journal = journal[0]
	}
	return q
}

func (q *Queue) pruneSeen(now time.Time) {
	cutoff := now.Add(-q.seenTTL)
	for id, at := range q.seen {
		if at.Before(cutoff) {
			delete(q.seen, id)
		}
	}
	if len(q.seen) <= q.maxSeen {
		return
	}
	type entry struct {
		id string
		at time.Time
	}
	entries := make([]entry, 0, len(q.seen))
	for id, at := range q.seen {
		entries = append(entries, entry{id: id, at: at})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].at.Before(entries[j].at) })
	for _, e := range entries[:len(entries)-q.maxSeen] {
		delete(q.seen, e.id)
	}
}

func (q *Queue) Enqueue(j Job) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if j.ID == "" {
		return false
	}
	now := time.Now().UTC()
	q.pruneSeen(now)
	if _, ok := q.seen[j.ID]; ok {
		return true
	}
	if q.journal != nil {
		if err := q.journal.Enqueue(j); err != nil {
			return false
		}
	}
	select {
	case q.ch <- j:
		q.seen[j.ID] = now
		return true
	default:
		return false
	}
}

func (q *Queue) Next() <-chan Job { return q.ch }

func (q *Queue) Recover() error {
	if q.journal == nil {
		return nil
	}
	jobs, err := q.journal.Pending()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, j := range jobs {
		if _, ok := q.seen[j.ID]; ok {
			continue
		}
		select {
		case q.ch <- j:
			q.seen[j.ID] = now
		default:
			return nil
		}
	}
	q.pruneSeen(now)
	return nil
}

func (q *Queue) Complete(j Job) {
	if q.journal != nil {
		_ = q.journal.Done(j)
	}
}

func (q *Queue) Dead(j Job, err error) {
	if q.journal != nil {
		_ = q.journal.Dead(j, err)
	}
}

func (q *Queue) Len() int {
	return len(q.ch)
}
