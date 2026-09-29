package session

import (
	"testing"
	"time"
)

func TestStore(t *testing.T) {
	s := NewStore()
	now := time.Now()
	s.Put(Session{ID:"s1", State:Active, CreatedAt:now})
	got, ok := s.Get("s1")
	if !ok || got.State != Active { t.Fatalf("unexpected session: %#v %v", got, ok) }
}
