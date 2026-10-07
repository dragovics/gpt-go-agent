package security

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ValidateRestrictedCommand enforces the deterministic command policy used by
// execution surfaces that promise bounded native execution. It intentionally
// excludes arbitrary shells, interpreters, and untrusted execution commands.
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
	case "uptime", "pwd", "date", "uname", "id":
		if len(args) != 0 {
			return fmt.Errorf("%s does not accept arguments in restricted execution", command)
		}
		return nil
	case "ls":
		for _, arg := range args {
			if strings.HasPrefix(arg, "..") || strings.HasPrefix(arg, "/") {
				return errors.New("ls arguments must not escape workspace")
			}
			if strings.ContainsAny(arg, ";;|&`$><\n\r") {
				return errors.New("invalid characters in ls argument")
			}
		}
		return nil
	case "git":
		if len(args) == 0 {
			return errors.New("git requires a subcommand")
		}
		subcmd := args[0]
		allowedGitSubcommands := map[string]bool{
			"status":    true,
			"diff":      true,
			"log":       true,
			"show":      true,
			"branch":    true,
			"rev-parse": true,
		}
		if !allowedGitSubcommands[subcmd] {
			return fmt.Errorf("git subcommand %q is not permitted by restricted policy", subcmd)
		}
		for _, arg := range args[1:] {
			if strings.HasPrefix(arg, "--exec") || strings.HasPrefix(arg, "-c") || strings.HasPrefix(arg, "--upload-pack") {
				return fmt.Errorf("git flag %q is not permitted", arg)
			}
			if strings.ContainsAny(arg, ";;|&`$><\n\r") {
				return errors.New("invalid characters in git argument")
			}
		}
		return nil
	case "grep":
		for _, arg := range args {
			if strings.HasPrefix(arg, "/") || strings.Contains(arg, "..") {
				return errors.New("grep targets must not escape workspace")
			}
			if strings.ContainsAny(arg, ";;|&`$><\n\r") {
				return errors.New("invalid characters in grep argument")
			}
		}
		return nil
	default:
		return fmt.Errorf("command %q is not permitted by restricted execution policy", command)
	}
}
