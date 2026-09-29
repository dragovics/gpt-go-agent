package webhook

import (
	"context"
	"time"
)

type SessionState struct {
	ID string
	EnvironmentID string
	RemoteURL string
	RequiresConnection bool
	Failed bool
}

type SessionReader interface {
	GetSession(context.Context, string) (SessionState, error)
}

type EnvironmentSupervisor interface {
	Start(context.Context, string, string) error
	Stop(string)
}

type Worker struct {
	Queue *Queue
	Reader SessionReader
	Supervisor EnvironmentSupervisor
}

func (w *Worker) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done(): return
		case j := <-w.Queue.Next():
			w.process(ctx, j)
			w.Queue.Complete(j)
		}
	}
}

func (w *Worker) process(ctx context.Context, j Job) {
	if w.Reader == nil || w.Supervisor == nil { return }
	s, err := w.Reader.GetSession(ctx, j.SessionID)
	if err != nil { return }
	if s.Failed || s.ID == "" {
		if s.EnvironmentID != "" { w.Supervisor.Stop(s.EnvironmentID) }
		return
	}
	if !s.RequiresConnection || s.EnvironmentID == "" || s.RemoteURL == "" { return }
	_ = w.Supervisor.Start(ctx, s.EnvironmentID, s.RemoteURL)
}

// RetryLoop is intentionally small; the API remains the source of truth.
func RetryLoop(ctx context.Context, fn func() error, attempts int, delay time.Duration) error {
	var err error
	for i:=0; i<attempts; i++ {
		if err = fn(); err == nil { return nil }
		select { case <-ctx.Done(): return ctx.Err(); case <-time.After(delay): }
	}
	return err
}
