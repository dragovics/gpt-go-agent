package security

import (
	"errors"
	"fmt"
	"path/filepath"
)

// ValidateRestrictedCommand enforces the deterministic command policy used by
// execution surfaces that promise bounded native execution. It intentionally
// excludes shells, interpreters, package managers, VCS commands, network
// clients, and commands with arbitrary path/options.
func ValidateRestrictedCommand(command string, args []string) error {
	if command == "" || filepath.Base(command) != command {
		return errors.New("command must be a bare executable name")
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
			return fmt.Errorf("%s does not accept arguments in restricted execution", command)
		}
		return nil
	default:
		return fmt.Errorf("command %q is not permitted by restricted execution policy", command)
	}
}
