package mcp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dragovics/gpt-go-agent/internal/security"
)

func (s *Server) listDir(args map[string]any) (string, error) {
	clean, err := cleanRelative(stringArg(args, "path"))
	if err != nil {
		return "", err
	}
	root, err := s.openRoot()
	if err != nil {
		return "", err
	}
	defer root.Close()

	entries, err := fs.ReadDir(root.FS(), clean)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() {
			fmt.Fprintf(&b, "%s/\n", e.Name())
		} else {
			fmt.Fprintf(&b, "%s\n", e.Name())
		}
		if b.Len() >= s.cfg.MaxOutputBytes {
			break
		}
	}
	out := b.String()
	if len(out) > s.cfg.MaxOutputBytes {
		out = out[:s.cfg.MaxOutputBytes]
	}
	return out, nil
}

func (s *Server) readFile(args map[string]any) (string, error) {
	clean, err := cleanRelative(stringArg(args, "path"))
	if err != nil {
		return "", err
	}
	root, err := s.openRoot()
	if err != nil {
		return "", err
	}
	defer root.Close()

	b, err := fs.ReadFile(root.FS(), clean)
	if err != nil {
		return "", err
	}
	if len(b) > s.cfg.MaxOutputBytes {
		b = b[:s.cfg.MaxOutputBytes]
	}
	return string(b), nil
}

func (s *Server) writeFile(args map[string]any) (string, error) {
	if !s.cfg.AllowWrite {
		return "", errors.New("writes are disabled")
	}
	clean, err := cleanRelative(stringArg(args, "path"))
	if err != nil {
		return "", err
	}
	if clean == "." {
		return "", errors.New("path must name a file")
	}
	content, ok := args["content"].(string)
	if !ok {
		return "", errors.New("content must be a string")
	}
	if len(content) > s.cfg.MaxOutputBytes*16 {
		return "", errors.New("content too large")
	}

	root, err := s.openRoot()
	if err != nil {
		return "", err
	}
	defer root.Close()

	if err := mkdirAllInRoot(root, filepath.Dir(clean)); err != nil {
		return "", err
	}
	f, err := root.OpenFile(clean, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	if _, err := f.Write([]byte(content)); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return "written " + clean, nil
}

func (s *Server) patchFile(args map[string]any) (string, error) {
	if !s.cfg.AllowWrite {
		return "", errors.New("writes are disabled")
	}
	clean, err := cleanRelative(stringArg(args, "path"))
	if err != nil {
		return "", err
	}
	if clean == "." {
		return "", errors.New("path must name a file")
	}
	oldStr, ok := args["old_string"].(string)
	if !ok || oldStr == "" {
		return "", errors.New("old_string must be a non-empty string")
	}
	newStr, ok := args["new_string"].(string)
	if !ok {
		return "", errors.New("new_string must be a string")
	}
	replaceAll, _ := args["replace_all"].(bool)

	root, err := s.openRoot()
	if err != nil {
		return "", err
	}
	defer root.Close()

	data, err := fs.ReadFile(root.FS(), clean)
	if err != nil {
		return "", err
	}
	content := string(data)

	count := strings.Count(content, oldStr)
	if count == 0 {
		return "", fmt.Errorf("old_string not found in %s", clean)
	}
	if !replaceAll && count > 1 {
		return "", fmt.Errorf("old_string matched %d times in %s; provide more surrounding context or set replace_all=true", count, clean)
	}

	var updated string
	if replaceAll {
		updated = strings.ReplaceAll(content, oldStr, newStr)
	} else {
		updated = strings.Replace(content, oldStr, newStr, 1)
	}

	f, err := root.OpenFile(clean, os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	if _, err := f.Write([]byte(updated)); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return fmt.Sprintf("successfully patched %s (%d occurrence(s) replaced)", clean, count), nil
}

func (s *Server) searchFiles(args map[string]any) (string, error) {
	patternStr := stringArg(args, "pattern")
	if patternStr == "" {
		return "", errors.New("pattern is required")
	}
	re, err := regexp.Compile(patternStr)
	if err != nil {
		return "", fmt.Errorf("invalid regex pattern: %w", err)
	}

	cleanPath, err := cleanRelative(stringArg(args, "path"))
	if err != nil {
		return "", err
	}

	maxMatches := 50
	if m, ok := args["max_matches"].(float64); ok && m > 0 {
		maxMatches = int(m)
		if maxMatches > 200 {
			maxMatches = 200
		}
	}

	root, err := s.openRoot()
	if err != nil {
		return "", err
	}
	defer root.Close()

	var b strings.Builder
	matchCount := 0

	skipDirs := map[string]bool{
		".git":         true,
		"node_modules": true,
		".cache":       true,
		"vendor":       true,
		".next":        true,
		"dist":         true,
		"build":        true,
	}

	walkErr := fs.WalkDir(root.FS(), cleanPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if matchCount >= maxMatches || b.Len() >= s.cfg.MaxOutputBytes {
			return fs.SkipAll
		}

		info, err := d.Info()
		if err != nil || info.Size() > 1024*1024 {
			return nil
		}

		data, err := fs.ReadFile(root.FS(), path)
		if err != nil {
			return nil
		}
		checkLen := len(data)
		if checkLen > 512 {
			checkLen = 512
		}
		if bytes.IndexByte(data[:checkLen], 0) >= 0 {
			return nil
		}

		scanner := bufio.NewScanner(bytes.NewReader(data))
		lineNum := 1
		for scanner.Scan() {
			line := scanner.Text()
			if re.MatchString(line) {
				fmt.Fprintf(&b, "%s:%d: %s\n", path, lineNum, strings.TrimRight(line, "\r\n"))
				matchCount++
				if matchCount >= maxMatches || b.Len() >= s.cfg.MaxOutputBytes {
					return fs.SkipAll
				}
			}
			lineNum++
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, fs.SkipAll) {
		return "", walkErr
	}

	res := b.String()
	if len(res) > s.cfg.MaxOutputBytes {
		res = res[:s.cfg.MaxOutputBytes]
	}
	if res == "" {
		return "no matches found", nil
	}
	return res, nil
}

func (s *Server) execCommand(parent context.Context, args map[string]any) (string, error) {
	if !s.cfg.AllowCommandExec {
		return "", errors.New("command execution is disabled")
	}
	command, ok := args["command"].(string)
	if !ok || command == "" {
		return "", errors.New("command is required")
	}
	if !s.cfg.AllowedCommands[command] {
		return "", fmt.Errorf("command %q is not allowlisted", command)
	}

	cwd := strings.TrimSpace(stringArg(args, "cwd"))
	if cwd != "" && cwd != "." {
		return "", errors.New("custom cwd is disabled; command execution is confined to the workspace root")
	}

	var argv []string
	if raw, ok := args["args"].([]any); ok {
		for _, v := range raw {
			a, ok := v.(string)
			if !ok {
				return "", errors.New("args must be strings")
			}
			argv = append(argv, a)
		}
	}
	if err := security.ValidateRestrictedCommand(command, argv); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(parent, s.cfg.CommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, argv...)
	cmd.Dir = s.cfg.Workspace
	cmd.Env = s.safeEnv()
	out, err := cmd.CombinedOutput()
	if len(out) > s.cfg.MaxOutputBytes {
		out = out[:s.cfg.MaxOutputBytes]
	}
	if ctx.Err() != nil {
		return string(out), ctx.Err()
	}
	if err != nil {
		return string(out), err
	}
	return string(out), nil
}
