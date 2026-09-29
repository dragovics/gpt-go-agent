package webhook

import (
	"context"
	"fmt"
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
			err := RetryLoop(ctx, func() error { return w.process(ctx, j) }, 3, 2*time.Second)
			if err == nil { w.Queue.Complete(j) } else if ctx.Err() == nil { w.Queue.Dead(j, err) }
		}
	}
}

func (w *Worker) process(ctx context.Context, j Job) error {
	if w.Reader == nil || w.Supervisor == nil { return fmt.Errorf("worker dependencies are not configured") }
	s, err := w.Reader.GetSession(ctx, j.SessionID)
	if err != nil { return err }
	if s.Failed || s.ID == "" {
		if s.EnvironmentID != "" { w.Supervisor.Stop(s.EnvironmentID) }
		return nil
	}
	if !s.RequiresConnection || s.EnvironmentID == "" || s.RemoteURL == "" { return nil }
	return w.Supervisor.Start(ctx, s.EnvironmentID, s.RemoteURL)
}

func RetryLoop(ctx context.Context, fn func() error, attempts int, delay time.Duration) error {
	var err error
	for i:=0; i<attempts; i++ {
		if err = fn(); err == nil { return nil }
		select { case <-ctx.Done(): return ctx.Err(); case <-time.After(delay): }
	}
	return err
}
