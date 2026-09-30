package mcp

import (
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
		{"name": "exec_command", "description": "Run a server-allowlisted command that also passes the restricted deterministic command policy. Execution always starts at the workspace root.", "inputSchema": map[string]any{"type": "object", "required": []string{"command"}, "properties": map[string]any{"command": map[string]any{"type": "string"}, "args": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "cwd": map[string]any{"type": "string", "description": "Only empty or '.' is accepted."}}}},
	}
}

func (s *Server) callTool(w http.ResponseWriter, id any, params map[string]any) {
	name, _ := params["name"].(string)
	args, _ := params["arguments"].(map[string]any)
	var out string
	var err error
	switch name {
	case "list_dir":
		out, err = s.listDir(args)
	case "read_file":
		out, err = s.readFile(args)
	case "write_file":
		out, err = s.writeFile(args)
	case "exec_command":
		out, err = s.execCommand(context.Background(), args)
	default:
		writeRPCError(w, id, -32602, "unknown tool")
		return
	}

	target := stringArg(args, "path")
	if name == "exec_command" {
		target = stringArg(args, "command")
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
