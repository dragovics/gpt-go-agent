package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dragovics/gpt-go-agent/internal/audit"
)

const protocolVersion = "2025-06-18"

type Config struct {
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
	cfg.Workspace = filepath.Clean(workspace)
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
			"capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{"name": "gpt-go-agent", "version": "0.2.0"},
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
	return strings.HasPrefix(got, prefix) && strings.TrimSpace(strings.TrimPrefix(got, prefix)) == expected
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
		{"name": "exec_command", "description": "Run an allowlisted executable with arguments inside the configured workspace. Command execution must be explicitly enabled.", "inputSchema": map[string]any{"type": "object", "required": []string{"command"}, "properties": map[string]any{"command": map[string]any{"type": "string"}, "args": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "cwd": map[string]any{"type": "string"}}}},
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
	if err != nil {
		if s.audit != nil {
			_ = s.audit.Record(audit.Event{Action: name, Target: stringArg(args, "path"), Allowed: false, Detail: err.Error()})
		}
		writeRPC(w, id, map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": err.Error()}}})
		return
	}
	if s.audit != nil {
		_ = s.audit.Record(audit.Event{Action: name, Target: stringArg(args, "path"), Allowed: true})
	}
	writeRPC(w, id, map[string]any{"content": []map[string]any{{"type": "text", "text": out}}})
}

func (s *Server) resolve(rel string) (string, error) {
	if rel == "" {
		rel = "."
	}
	if filepath.IsAbs(rel) {
		return "", errors.New("absolute paths are not allowed")
	}
	root := s.cfg.Workspace
	p := filepath.Clean(filepath.Join(root, rel))
	relToRoot, err := filepath.Rel(root, p)
	if err != nil || relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes workspace")
	}
	return p, nil
}

func (s *Server) listDir(args map[string]any) (string, error) {
	p, err := s.resolve(stringArg(args, "path"))
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() {
			fmt.Fprintf(&b, "%s/\\n", e.Name())
		} else {
			fmt.Fprintf(&b, "%s\\n", e.Name())
		}
	}
	return b.String(), nil
}

func (s *Server) readFile(args map[string]any) (string, error) {
	p, err := s.resolve(stringArg(args, "path"))
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
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
	p, err := s.resolve(stringArg(args, "path"))
	if err != nil {
		return "", err
	}
	content, ok := args["content"].(string)
	if !ok {
		return "", errors.New("content must be a string")
	}
	if len(content) > s.cfg.MaxOutputBytes*16 {
		return "", errors.New("content too large")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return "", err
	}
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		return "", err
	}
	return "written " + filepath.Clean(stringArg(args, "path")), nil
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
	cwd, err := s.resolve(stringArg(args, "cwd"))
	if err != nil {
		return "", err
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
	ctx, cancel := context.WithTimeout(parent, s.cfg.CommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, argv...)
	cmd.Dir = cwd
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
	blocked := map[string]bool{"OPENAI_API_KEY": true, "OPENAI_EXECUTOR_API_KEY": true, "OPENAI_WEBHOOK_SECRET": true, "AGENT_MCP_TOKEN": true}
	var env []string
	for _, item := range os.Environ() {
		name, _, ok := strings.Cut(item, "=")
		if ok && !blocked[name] {
			env = append(env, item)
		}
	}
	return env
}

func stringArg(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}
