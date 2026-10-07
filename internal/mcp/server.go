package mcp

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dragovics/gpt-go-agent/internal/audit"
)

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
