package mcp

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

const protocolVersion = "2025-06-18"

type rpcRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
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

func stringArg(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}
