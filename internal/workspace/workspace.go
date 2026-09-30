package workspace

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Workspace provides filesystem operations rooted at a single directory.
// os.Root prevents symlink traversal and path races from escaping that root.
type Workspace struct {
	path string
	root *os.Root
}

func Open(path string) (*Workspace, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("workspace is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("open workspace root: %w", err)
	}
	return &Workspace{path: filepath.Clean(abs), root: root}, nil
}

func (w *Workspace) Close() error {
	if w == nil || w.root == nil {
		return nil
	}
	return w.root.Close()
}

func (w *Workspace) Path() string {
	return w.path
}

func cleanRelative(rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return ".", nil
	}
	if filepath.IsAbs(rel) {
		return "", errors.New("absolute paths are not allowed")
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes workspace")
	}
	return clean, nil
}

func (w *Workspace) ListDir(rel string) ([]fs.DirEntry, error) {
	clean, err := cleanRelative(rel)
	if err != nil {
		return nil, err
	}
	f, err := w.root.Open(clean)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.ReadDir(-1)
}

func (w *Workspace) ReadFile(rel string, maxBytes int) ([]byte, bool, error) {
	clean, err := cleanRelative(rel)
	if err != nil {
		return nil, false, err
	}
	f, err := w.root.Open(clean)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()

	if maxBytes <= 0 {
		maxBytes = 16 << 10
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(maxBytes)+1))
	if err != nil {
		return nil, false, err
	}
	truncated := len(data) > maxBytes
	if truncated {
		data = data[:maxBytes]
	}
	return data, truncated, nil
}

func (w *Workspace) WriteFile(rel string, data []byte, perm fs.FileMode) error {
	clean, err := cleanRelative(rel)
	if err != nil {
		return err
	}
	if clean == "." {
		return errors.New("file path is required")
	}
	if err := w.mkdirParents(filepath.Dir(clean), 0o700); err != nil {
		return err
	}
	f, err := w.root.OpenFile(clean, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func (w *Workspace) mkdirParents(rel string, perm fs.FileMode) error {
	if rel == "." || rel == "" {
		return nil
	}
	clean, err := cleanRelative(rel)
	if err != nil {
		return err
	}
	parts := strings.Split(clean, string(filepath.Separator))
	current := ""
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		if err := w.root.Mkdir(current, perm); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
	}
	return nil
}

// OpenDir returns a securely resolved directory handle inside the workspace.
// On Linux, ProcPath can be used as an exec.Cmd working directory while this
// handle remains open, avoiding a symlink TOCTOU window.
func (w *Workspace) OpenDir(rel string) (*os.File, error) {
	clean, err := cleanRelative(rel)
	if err != nil {
		return nil, err
	}
	f, err := w.root.Open(clean)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !info.IsDir() {
		_ = f.Close()
		return nil, errors.New("working directory is not a directory")
	}
	return f, nil
}

func ProcPath(f *os.File) string {
	return fmt.Sprintf("/proc/self/fd/%d", f.Fd())
}
