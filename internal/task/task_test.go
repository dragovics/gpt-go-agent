package task

import (
	"testing"
	"time"
)

func TestStore(t *testing.T) {
	s := NewStore()
	now := time.Now()
	s.Put(Task{ID: "t1", State: Running, CreatedAt: now})
	got, ok := s.Get("t1")
	if !ok || got.State != Running {
		t.Fatalf("unexpected task: %#v %v", got, ok)
	}
}
