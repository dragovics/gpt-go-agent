package mcp

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/dragovics/gpt-go-agent/internal/security"
)

func cleanRelative(rel string) (string, error) {
	if rel == "" {
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

func (s *Server) openRoot() (*os.Root, error) {
	return os.OpenRoot(s.cfg.Workspace)
}

func (s *Server) safeEnv() []string {
	return security.SanitizedEnvironment(os.Environ())
}

func mkdirAllInRoot(root *os.Root, dir string) error {
	clean, err := cleanRelative(dir)
	if err != nil {
		return err
	}
	if clean == "." {
		return nil
	}

	var current string
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		if current == "" {
			current = part
		} else {
			current = filepath.Join(current, part)
		}
		if err := root.Mkdir(current, 0700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
	}
	return nil
}
