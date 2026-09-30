package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCleanRelativeStaysInsideWorkspace(t *testing.T) {
	if _, err := cleanRelative("../escape"); err == nil {
		t.Fatal("expected path escape rejection")
	}
	if _, err := cleanRelative("ok/file.txt"); err != nil {
		t.Fatal(err)
	}
}

func TestUnauthenticatedRemoteRequestRejected(t *testing.T) {
	s, err := New(Config{Workspace: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.RemoteAddr = "10.0.0.2:1234"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("got %d", w.Code)
	}
}

func TestTokenAuth(t *testing.T) {
	s, err := New(Config{Workspace: t.TempDir(), Token: "secret"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("got %d", w.Code)
	}
}

func TestMutatingMCPRequiresToken(t *testing.T) {
	if _, err := New(Config{Workspace: t.TempDir(), AllowWrite: true}, nil); err == nil {
		t.Fatal("expected writes without token to be rejected")
	}
	if _, err := New(Config{Workspace: t.TempDir(), AllowCommandExec: true}, nil); err == nil {
		t.Fatal("expected command execution without token to be rejected")
	}
}

func TestWriteRejectsSymlinkEscape(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workspace, "escape")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	s, err := New(Config{
		Workspace:  workspace,
		Token:      "secret",
		AllowWrite: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.writeFile(map[string]any{
		"path":    "escape/pwned.txt",
		"content": "nope",
	})
	if err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
	if _, statErr := os.Stat(filepath.Join(outside, "pwned.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("file escaped workspace, stat err=%v", statErr)
	}
}

func TestListRejectsSymlinkEscape(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "escape")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	s, err := New(Config{Workspace: workspace}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.listDir(map[string]any{"path": "escape"}); err == nil {
		t.Fatal("expected list_dir symlink escape to be rejected")
	}
}

func TestExecCommandUsesRestrictedPolicy(t *testing.T) {
	s, err := New(Config{
		Workspace:        t.TempDir(),
		Token:            "secret",
		AllowCommandExec: true,
		AllowedCommands:  map[string]bool{"python3": true, "echo": true},
		CommandTimeout:   time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.execCommand(t.Context(), map[string]any{
		"command": "python3",
		"args":    []any{"-c", "print(1)"},
	})
	if err == nil || !strings.Contains(err.Error(), "not permitted") {
		t.Fatalf("expected deterministic policy rejection, got %v", err)
	}

	if _, err := s.execCommand(t.Context(), map[string]any{
		"command": "echo",
		"args":    []any{"ok"},
	}); err != nil {
		t.Fatalf("expected echo to be allowed: %v", err)
	}
}

func TestCustomExecCWDRejected(t *testing.T) {
	s, err := New(Config{
		Workspace:        t.TempDir(),
		Token:            "secret",
		AllowCommandExec: true,
		AllowedCommands:  map[string]bool{"pwd": true},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.execCommand(t.Context(), map[string]any{
		"command": "pwd",
		"cwd":     "subdir",
	})
	if err == nil {
		t.Fatal("expected custom cwd rejection")
	}
}
