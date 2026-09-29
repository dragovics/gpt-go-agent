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
		case <-ctx.Done():
			return
		case j, ok := <-w.Queue.Next():
			if !ok {
				return
			}
			err := RetryLoop(ctx, func() error { return w.process(ctx, j) }, 4, 2*time.Second)
			if err == nil {
				w.Queue.Complete(j)
			} else if ctx.Err() == nil {
				w.Queue.Dead(j, err)
			}
		}
	}
}

func (w *Worker) process(ctx context.Context, j Job) error {
	if w.Reader == nil || w.Supervisor == nil {
		return fmt.Errorf("worker dependencies are not configured")
	}
	s, err := w.Reader.GetSession(ctx, j.SessionID)
	if err != nil {
		return err
	}
	if s.Failed || s.ID == "" {
		if s.EnvironmentID != "" {
			w.Supervisor.Stop(s.EnvironmentID)
		}
		if w.Sessions != nil && s.ID != "" {
			if err := w.Sessions.Put(s.ID, s.EnvironmentID, s.RemoteURL, "failed"); err != nil { return err }
		}
		return nil
	}
	if !s.RequiresConnection || s.EnvironmentID == "" || s.RemoteURL == "" {
		return nil
	}
	if err := w.Supervisor.Start(ctx, s.EnvironmentID, s.RemoteURL); err != nil { return err }
	if w.Sessions != nil {
		if err := w.Sessions.Put(s.ID, s.EnvironmentID, s.RemoteURL, "active"); err != nil { return err }
	}
	return nil
}

// RetryLoop retries with exponential backoff. It intentionally does not
// resubmit user input; it only retries idempotent lifecycle handling.
func RetryLoop(ctx context.Context, fn func() error, attempts int, delay time.Duration) error {
	if attempts < 1 {
		return fmt.Errorf("attempts must be positive")
	}
	var err error
	for i := 0; i < attempts; i++ {
		if err = fn(); err == nil {
			return nil
		}
		if i == attempts-1 {
			break
		}
		backoff := delay
		for n := 0; n < i; n++ {
			backoff *= 2
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}
