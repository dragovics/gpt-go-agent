package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceReadWriteAndList(t *testing.T) {
	dir := t.TempDir()
	ws, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	if err := ws.WriteFile("nested/file.txt", []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, truncated, err := ws.ReadFile("nested/file.txt", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if truncated || string(got) != "hello" {
		t.Fatalf("read=%q truncated=%v", got, truncated)
	}
	entries, err := ws.ListDir("nested")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "file.txt" {
		t.Fatalf("entries=%v", entries)
	}
}

func TestWorkspaceRejectsTraversal(t *testing.T) {
	ws, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	if _, _, err := ws.ReadFile("../escape", 1024); err == nil {
		t.Fatal("expected traversal rejection")
	}
	if err := ws.WriteFile("/tmp/escape", []byte("x"), 0o600); err == nil {
		t.Fatal("expected absolute path rejection")
	}
}

func TestWorkspaceRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	ws, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	if _, _, err := ws.ReadFile("escape/secret.txt", 1024); err == nil {
		t.Fatal("expected symlink read escape rejection")
	}
	if err := ws.WriteFile("escape/new.txt", []byte("x"), 0o600); err == nil {
		t.Fatal("expected symlink write escape rejection")
	}
	if _, err := ws.ListDir("escape"); err == nil {
		t.Fatal("expected symlink list escape rejection")
	}
	if _, err := ws.OpenDir("escape"); err == nil {
		t.Fatal("expected symlink cwd escape rejection")
	}
	if _, err := os.Stat(filepath.Join(outside, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("outside file unexpectedly created: %v", err)
	}
}
