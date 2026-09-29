package session

import (
	"path/filepath"
	"testing"
)

func TestDurableStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	s := NewDurableStore(path)
	if err := s.Put(Session{ID: "s1", EnvironmentID: "e1", RemoteURL: "wss://example.invalid", State: Active}); err != nil {
		t.Fatal(err)
	}
	loaded := NewDurableStore(path)
	if err := loaded.Load(); err != nil {
		t.Fatal(err)
	}
	got, err := loaded.Get("s1")
	if err != nil {
		t.Fatal(err)
	}
	if got.EnvironmentID != "e1" || got.State != Active {
		t.Fatalf("unexpected session: %#v", got)
	}
}
