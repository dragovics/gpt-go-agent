package mcp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dragovics/gpt-go-agent/internal/audit"
	"github.com/dragovics/gpt-go-agent/internal/security"
)

const protocolVersion = "2025-06-18"

type Config struct {
	Version          string
	Workspace        string
	Token            string
	AllowWrite       bool
	AllowCommandExec bool
	AllowedCommands  map[string]bool
	CommandTimeout   time.Duration
	MaxOutputBytes   int
}

type Server struct {
	cfg   Config
	audit *audit.Logger
}

func New(cfg Config, auditLog *audit.Logger) (*Server, error) {
	if cfg.Workspace == "" {
		return nil, errors.New("workspace is required")
	}
	workspace, err := filepath.Abs(cfg.Workspace)
	if err != nil {
		return nil, err
	}
	if cfg.CommandTimeout <= 0 {
		cfg.CommandTimeout = 60 * time.Second
	}
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = 16 << 10
	}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	cfg.Workspace = filepath.Clean(workspace)

	if (cfg.AllowWrite || cfg.AllowCommandExec) && strings.TrimSpace(cfg.Token) == "" {
		return nil, errors.New("MCP token is required when writes or command execution are enabled")
	}

	if cfg.AllowedCommands == nil {
		cfg.AllowedCommands = map[string]bool{}
		for _, name := range strings.Fields(strings.ReplaceAll(os.Getenv("AGENT_ALLOWED_COMMANDS"), ",", " ")) {
			cfg.AllowedCommands[name] = true
		}
	}
	return &Server{cfg: cfg, audit: auditLog}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.handle)
	return mux
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.cfg.Token != "" && !validBearer(r, s.cfg.Token) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.cfg.Token == "" && !isLoopback(r) {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}

	var req rpcRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeRPCError(w, nil, -32700, "invalid JSON")
		return
	}
	if req.JSONRPC != "" && req.JSONRPC != "2.0" {
		writeRPCError(w, req.ID, -32600, "invalid JSON-RPC version")
		return
	}

	switch req.Method {
	case "initialize":
		writeRPC(w, req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "gpt-go-agent", "version": s.cfg.Version},
		})
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		writeRPC(w, req.ID, map[string]any{"tools": s.tools()})
	case "tools/call":
		s.callTool(w, req.ID, req.Params)
	default:
		writeRPCError(w, req.ID, -32601, "method not found")
	}
}

type rpcRequest struct {
	JSONRPC string
	ID      any
	Method  string
	Params  map[string]any
}

func rpcError(code int, message string) map[string]any {
	return map[string]any{"code": code, "message": message}
}

func writeRPC(w http.ResponseWriter, id any, result any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("MCP-Protocol-Version", protocolVersion)
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func writeRPCError(w http.ResponseWriter, id any, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("MCP-Protocol-Version", protocolVersion)
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": rpcError(code, message)})
}

func validBearer(r *http.Request, expected string) bool {
	const prefix = "Bearer "
	got := r.Header.Get("Authorization")
	if !strings.HasPrefix(got, prefix) {
		return false
	}
	provided := strings.TrimSpace(strings.TrimPrefix(got, prefix))
	if provided == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func isLoopback(r *http.Request) bool {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = strings.Trim(host[:i], "[]")
	}
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
}

func (s *Server) tools() []map[string]any {
	return []map[string]any{
		{"name": "list_dir", "description": "List entries inside a workspace-relative directory.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}},
		{"name": "read_file", "description": "Read a UTF-8 text file inside the configured workspace.", "inputSchema": map[string]any{"type": "object", "required": []string{"path"}, "properties": map[string]any{"path": map[string]any{"type": "string"}}}},
		{"name": "write_file", "description": "Write a UTF-8 text file inside the configured workspace. Requires write access.", "inputSchema": map[string]any{"type": "object", "required": []string{"path", "content"}, "properties": map[string]any{"path": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}}}},
		{"name": "search_files", "description": "Search text or regex pattern across workspace files.", "inputSchema": map[string]any{"type": "object", "required": []string{"pattern"}, "properties": map[string]any{"pattern": map[string]any{"type": "string"}, "path": map[string]any{"type": "string", "description": "Subdirectory to search in (defaults to workspace root)."}, "max_matches": map[string]any{"type": "integer", "description": "Maximum number of matches to return (default 50)."}}}},
		{"name": "patch_file", "description": "Targeted find-and-replace edit in a workspace file. Requires write access.", "inputSchema": map[string]any{"type": "object", "required": []string{"path", "old_string", "new_string"}, "properties": map[string]any{"path": map[string]any{"type": "string"}, "old_string": map[string]any{"type": "string"}, "new_string": map[string]any{"type": "string"}, "replace_all": map[string]any{"type": "boolean", "description": "Replace all occurrences instead of requiring unique match (default false)."}}}},
		{"name": "exec_command", "description": "Run a server-allowlisted command that also passes the restricted deterministic command policy. Execution always starts at the workspace root.", "inputSchema": map[string]any{"type": "object", "required": []string{"command"}, "properties": map[string]any{"command": map[string]any{"type": "string"}, "args": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "cwd": map[string]any{"type": "string", "description": "Only empty or '.' is accepted."}}}},
	}
}

func (s *Server) callTool(w http.ResponseWriter, id any, params map[string]any) {
	name, _ := params["name"].(string)
	args, _ := params["arguments"].(map[string]any)
	var out string
	var err error
	target := stringArg(args, "path")
	switch name {
	case "list_dir":
		out, err = s.listDir(args)
	case "read_file":
		out, err = s.readFile(args)
	case "write_file":
		out, err = s.writeFile(args)
	case "search_files":
		target = stringArg(args, "pattern")
		out, err = s.searchFiles(args)
	case "patch_file":
		out, err = s.patchFile(args)
	case "exec_command":
		target = stringArg(args, "command")
		out, err = s.execCommand(context.Background(), args)
	default:
		writeRPCError(w, id, -32602, "unknown tool")
		return
	}
	if err != nil {
		if s.audit != nil {
			_ = s.audit.Record(audit.Event{Action: name, Target: target, Allowed: false, Detail: err.Error()})
		}
		writeRPC(w, id, map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": err.Error()}}})
		return
	}
	if s.audit != nil {
		_ = s.audit.Record(audit.Event{Action: name, Target: target, Allowed: true})
	}
	writeRPC(w, id, map[string]any{"content": []map[string]any{{"type": "text", "text": out}}})
}

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
		if err := root.Mkdir(current, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	return nil
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
	// #nosec G204 -- command and arguments pass both a server-side allowlist and deterministic restricted-command policy.
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

func (s *Server) safeEnv() []string {
	return security.SanitizedEnvironment(os.Environ())
}

func stringArg(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
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
