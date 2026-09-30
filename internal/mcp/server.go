package mcp

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/dragovics/gpt-go-agent/internal/audit"
	"github.com/dragovics/gpt-go-agent/internal/policy"
	"github.com/dragovics/gpt-go-agent/internal/security"
	"github.com/dragovics/gpt-go-agent/internal/workspace"
)

const protocolVersion = "2025-06-18"

type Config struct {
	Version          string
	Workspace        string
	Token            string
	AllowWrite       bool
	AllowCommandExec bool
	CommandMode      policy.NativeMode
	AllowedCommands  map[string]bool
	CommandTimeout   time.Duration
	MaxOutputBytes   int
}

type Server struct {
	cfg       Config
	audit     *audit.Logger
	workspace *workspace.Workspace
}

func New(cfg Config, auditLog *audit.Logger) (*Server, error) {
	if cfg.Workspace == "" {
		return nil, errors.New("workspace is required")
	}
	if cfg.CommandTimeout <= 0 {
		cfg.CommandTimeout = 60 * time.Second
	}
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = 16 << 10
	}
	if cfg.CommandMode == "" {
		cfg.CommandMode = policy.NativeStrict
	}
	if cfg.CommandMode != policy.NativeStrict && cfg.CommandMode != policy.NativeTrusted {
		return nil, fmt.Errorf("invalid command mode %q", cfg.CommandMode)
	}
	if cfg.AllowedCommands == nil {
		cfg.AllowedCommands = map[string]bool{}
	}
	ws, err := workspace.Open(cfg.Workspace)
	if err != nil {
		return nil, err
	}
	cfg.Workspace = ws.Path()
	return &Server{cfg: cfg, audit: auditLog, workspace: ws}, nil
}

func (s *Server) Close() error {
	return s.workspace.Close()
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
		version := s.cfg.Version
		if version == "" {
			version = "dev"
		}
		writeRPC(w, req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "gpt-go-agent", "version": version},
		})
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		writeRPC(w, req.ID, map[string]any{"tools": s.tools()})
	case "tools/call":
		s.callTool(r.Context(), w, req.ID, req.Params)
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
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func (s *Server) tools() []map[string]any {
	execDescription := "Run a native executable inside the configured workspace. Command execution must be explicitly enabled."
	if s.cfg.CommandMode == policy.NativeStrict {
		execDescription += " Strict mode permits only a small deterministic diagnostic command set."
	} else {
		execDescription += " Trusted mode grants allowlisted executables the authority of the service account."
	}
	return []map[string]any{
		{"name": "list_dir", "description": "List entries inside a workspace-relative directory.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}},
		{"name": "read_file", "description": "Read a UTF-8 text file inside the configured workspace.", "inputSchema": map[string]any{"type": "object", "required": []string{"path"}, "properties": map[string]any{"path": map[string]any{"type": "string"}}}},
		{"name": "write_file", "description": "Write a UTF-8 text file inside the configured workspace. Requires write access.", "inputSchema": map[string]any{"type": "object", "required": []string{"path", "content"}, "properties": map[string]any{"path": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}}}},
		{"name": "exec_command", "description": execDescription, "inputSchema": map[string]any{"type": "object", "required": []string{"command"}, "properties": map[string]any{"command": map[string]any{"type": "string"}, "args": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "cwd": map[string]any{"type": "string"}}}},
	}
}

func (s *Server) callTool(ctx context.Context, w http.ResponseWriter, id any, params map[string]any) {
	name, _ := params["name"].(string)
	args, _ := params["arguments"].(map[string]any)
	if args == nil {
		args = map[string]any{}
	}

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
		out, err = s.execCommand(ctx, args)
	default:
		writeRPCError(w, id, -32602, "unknown tool")
		return
	}

	target := auditTarget(name, args)
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

func auditTarget(name string, args map[string]any) string {
	if name == "exec_command" {
		return stringArg(args, "command")
	}
	return stringArg(args, "path")
}

func (s *Server) listDir(args map[string]any) (string, error) {
	entries, err := s.workspace.ListDir(stringArg(args, "path"))
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		if b.Len()+len(name)+1 > s.cfg.MaxOutputBytes {
			b.WriteString("[output truncated]\n")
			break
		}
		b.WriteString(name)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

func (s *Server) readFile(args map[string]any) (string, error) {
	data, truncated, err := s.workspace.ReadFile(stringArg(args, "path"), s.cfg.MaxOutputBytes)
	if err != nil {
		return "", err
	}
	out := string(data)
	if truncated {
		out += "\n[output truncated]"
	}
	return out, nil
}

func (s *Server) writeFile(args map[string]any) (string, error) {
	if !s.cfg.AllowWrite {
		return "", errors.New("writes are disabled")
	}
	path := stringArg(args, "path")
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}
	content, ok := args["content"].(string)
	if !ok {
		return "", errors.New("content must be a string")
	}
	if len(content) > s.cfg.MaxOutputBytes*16 {
		return "", errors.New("content too large")
	}
	if err := s.workspace.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return "written " + path, nil
}

func (s *Server) execCommand(parent context.Context, args map[string]any) (string, error) {
	if !s.cfg.AllowCommandExec {
		return "", errors.New("command execution is disabled")
	}
	command, ok := args["command"].(string)
	if !ok || command == "" {
		return "", errors.New("command is required")
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
	} else if _, exists := args["args"]; exists {
		return "", errors.New("args must be an array")
	}
	if err := policy.ValidateNativeCommand(command, argv, s.cfg.CommandMode, s.cfg.AllowedCommands); err != nil {
		return "", err
	}

	dirHandle, err := s.workspace.OpenDir(stringArg(args, "cwd"))
	if err != nil {
		return "", err
	}
	defer dirHandle.Close()

	ctx, cancel := context.WithTimeout(parent, s.cfg.CommandTimeout)
	defer cancel()
	// #nosec G204 -- command and arguments pass deterministic server-side policy.
	cmd := exec.CommandContext(ctx, command, argv...)
	security.ConfigureProcessGroup(cmd)
	cmd.Dir = workspace.ProcPath(dirHandle)
	if s.cfg.CommandMode == policy.NativeStrict {
		cmd.Env = security.MinimalEnvironment(os.Environ())
	} else {
		cmd.Env = security.SanitizedEnvironment(os.Environ())
	}
	out, err := cmd.CombinedOutput()
	if len(out) > s.cfg.MaxOutputBytes {
		out = append(out[:s.cfg.MaxOutputBytes], []byte("\n[output truncated]")...)
	}
	if ctx.Err() != nil {
		return string(out), ctx.Err()
	}
	if err != nil {
		return string(out), err
	}
	return string(out), nil
}

func stringArg(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}
