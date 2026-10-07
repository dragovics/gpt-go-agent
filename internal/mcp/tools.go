package mcp

import (
	"context"
	"net/http"

	"github.com/dragovics/gpt-go-agent/internal/audit"
)

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
