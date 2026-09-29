package mcp

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestResolveStaysInsideWorkspace(t *testing.T) {
	s, err := New(Config{Workspace: t.TempDir(), CommandTimeout: time.Second}, nil)
	if err != nil { t.Fatal(err) }
	if _, err := s.resolve("../escape"); err == nil { t.Fatal("expected path escape rejection") }
	if _, err := s.resolve("ok/file.txt"); err != nil { t.Fatal(err) }
}

func TestUnauthenticatedRemoteRequestRejected(t *testing.T) {
	s, err := New(Config{Workspace: t.TempDir()}, nil)
	if err != nil { t.Fatal(err) }
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.RemoteAddr = "10.0.0.2:1234"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 401 { t.Fatalf("got %d", w.Code) }
}

func TestTokenAuth(t *testing.T) {
	s, err := New(Config{Workspace: t.TempDir(), Token: "secret"}, nil)
	if err != nil { t.Fatal(err) }
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 { t.Fatalf("got %d", w.Code) }
}
