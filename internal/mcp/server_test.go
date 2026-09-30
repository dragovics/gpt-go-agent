package mcp

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dragovics/gpt-go-agent/internal/policy"
)

func newTestServer(t *testing.T, cfg Config) *Server {
	t.Helper()
	if cfg.Workspace == "" {
		cfg.Workspace = t.TempDir()
	}
	s, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestWorkspaceOperationsStayInsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	s := newTestServer(t, Config{Workspace: root, AllowWrite: true, CommandTimeout: time.Second})
	if _, err := s.readFile(map[string]any{"path": "../escape"}); err == nil {
		t.Fatal("expected traversal rejection")
	}
	if _, err := s.readFile(map[string]any{"path": "escape/secret.txt"}); err == nil {
		t.Fatal("expected symlink read escape rejection")
	}
	if _, err := s.writeFile(map[string]any{"path": "escape/new.txt", "content": "x"}); err == nil {
		t.Fatal("expected symlink write escape rejection")
	}
	if _, err := s.listDir(map[string]any{"path": "escape"}); err == nil {
		t.Fatal("expected symlink list escape rejection")
	}
}

func TestExecCWDRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	s := newTestServer(t, Config{
		Workspace:        root,
		AllowCommandExec: true,
		CommandMode:      policy.NativeStrict,
		CommandTimeout:   time.Second,
	})
	if _, err := s.execCommand(context.Background(), map[string]any{"command": "pwd", "cwd": "escape"}); err == nil {
		t.Fatal("expected symlink cwd escape rejection")
	}
}

func TestStrictExecDeniesInterpreter(t *testing.T) {
	s := newTestServer(t, Config{
		AllowCommandExec: true,
		CommandMode:      policy.NativeStrict,
		CommandTimeout:   time.Second,
	})
	_, err := s.execCommand(context.Background(), map[string]any{
		"command": "python3",
		"args":    []any{"-c", "print(1)"},
	})
	if err == nil || !strings.Contains(err.Error(), "not permitted") {
		t.Fatalf("expected strict denial, got %v", err)
	}
}

func TestTrustedExecRequiresAllowlist(t *testing.T) {
	s := newTestServer(t, Config{
		AllowCommandExec: true,
		CommandMode:      policy.NativeTrusted,
		AllowedCommands:  map[string]bool{"echo": true},
		CommandTimeout:   time.Second,
	})
	out, err := s.execCommand(context.Background(), map[string]any{
		"command": "echo",
		"args":    []any{"ok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "ok" {
		t.Fatalf("output=%q", out)
	}
}

func TestUnauthenticatedRemoteRequestRejected(t *testing.T) {
	s := newTestServer(t, Config{})
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.RemoteAddr = "10.0.0.2:1234"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("got %d", w.Code)
	}
}

func TestTokenAuth(t *testing.T) {
	s := newTestServer(t, Config{Token: "secret"})
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("got %d", w.Code)
	}
}
