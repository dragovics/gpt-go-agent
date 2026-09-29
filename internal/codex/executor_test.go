package codex

import "testing"

func TestExecServerDoesNotPutKeyInArgs(t *testing.T) {
	e := ExecServer("wss://example.invalid", "env_123", "/workspace")
	for _, arg := range e.Args {
		if arg == "secret" || arg == "CODEX_API_KEY" { t.Fatalf("credential leaked into args: %q", arg) }
	}
}
