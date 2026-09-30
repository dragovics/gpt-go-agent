package webhook

import "github.com/dragovics/gpt-go-agent/internal/security"

// validateNativeCommand is the deterministic second gate after the LLM
// decision. workspace is retained in the signature for compatibility with
// existing tests and callers; path-bearing commands are intentionally excluded.
func validateNativeCommand(command string, args []string, workspace string) error {
	return security.ValidateRestrictedCommand(command, args)
}
