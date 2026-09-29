package codex

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Factory func(remoteURL, environmentID, workspace string) *Executor

type Supervisor struct {
	mu sync.Mutex
	running map[string]*execHandle
	factory Factory
	workspace string
	restartDelay time.Duration
	maxRestarts int
}

type execHandle struct {
	cmdDone chan error
	cancel context.CancelFunc
}

func NewSupervisor(workspace string, factory Factory) *Supervisor {
	if factory == nil { factory = ExecServer }
	return &Supervisor{running: make(map[string]*execHandle), factory: factory, workspace: workspace, restartDelay: 2*time.Second, maxRestarts: 3}
}

func (s *Supervisor) Start(ctx context.Context, environmentID, remoteURL string) error {
	if environmentID == "" || remoteURL == "" { return fmt.Errorf("environment id and remote url are required") }
	s.mu.Lock()
	if _, ok := s.running[environmentID]; ok { s.mu.Unlock(); return nil }
	runCtx, cancel := context.WithCancel(ctx)
	e := s.factory(remoteURL, environmentID, s.workspace)
	cmd, err := e.Start(runCtx)
	if err != nil { cancel(); s.mu.Unlock(); return err }
	h := &execHandle{cmdDone: make(chan error,1), cancel: cancel}
	s.running[environmentID] = h
	s.mu.Unlock()
	go func() {
		err := cmd.Wait()
		h.cmdDone <- err
		s.mu.Lock()
		cur, ok := s.running[environmentID]
		s.mu.Unlock()
		if !ok || cur != h || runCtx.Err() != nil { return }
		for attempt := 1; attempt <= s.maxRestarts; attempt++ {
			select { case <-runCtx.Done(): return; case <-time.After(s.restartDelay): }
			if err := s.Start(runCtx, environmentID, remoteURL); err == nil { return }
		}
		s.mu.Lock()
		if cur, ok := s.running[environmentID]; ok && cur == h { delete(s.running, environmentID) }
		s.mu.Unlock()
	}()
	return nil
}

func (s *Supervisor) Stop(environmentID string) {
	s.mu.Lock()
	h, ok := s.running[environmentID]
	if ok { delete(s.running, environmentID) }
	s.mu.Unlock()
	if ok { h.cancel() }
}

func (s *Supervisor) StopAll() {
	s.mu.Lock()
	hs := make([]*execHandle,0,len(s.running))
	for id,h := range s.running { delete(s.running,id); hs=append(hs,h) }
	s.mu.Unlock()
	for _,h := range hs { h.cancel() }
}
