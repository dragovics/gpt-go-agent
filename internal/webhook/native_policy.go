package webhook

import (
	"errors"
	"fmt"
	"path/filepath"
)

// validateNativeCommand is a deterministic second gate after the LLM decision.
// It intentionally permits only a small set of non-shell, read-only/diagnostic
// commands. Coding interpreters, build tools, network clients, VCS commands,
// and commands with arbitrary path/options are excluded from the webhook path.
func validateNativeCommand(command string, args []string, workspace string) error {
	if command == "" || filepath.Base(command) != command {
		return errors.New("native command must be a bare executable name")
	}

	switch command {
	case "echo":
		if len(args) > 32 {
			return errors.New("too many echo arguments")
		}
		for _, arg := range args {
			if len(arg) > 4096 {
				return errors.New("echo argument too large")
			}
		}
		return nil
	case "uptime", "pwd", "date", "uname", "id", "ls":
		if len(args) != 0 {
			return fmt.Errorf("%s does not accept arguments in webhook execution", command)
		}
		return nil
	default:
		return fmt.Errorf("native command %q is not permitted by webhook policy", command)
	}
}
