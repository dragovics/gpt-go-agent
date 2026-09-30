package policy

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type NativeMode string

const (
	NativeStrict  NativeMode = "strict"
	NativeTrusted NativeMode = "trusted"
)

func ParseNativeMode(raw string) (NativeMode, error) {
	switch NativeMode(strings.ToLower(strings.TrimSpace(raw))) {
	case "", NativeStrict:
		return NativeStrict, nil
	case NativeTrusted:
		return NativeTrusted, nil
	default:
		return "", fmt.Errorf("invalid command execution mode %q", raw)
	}
}

// ValidateNativeCommand applies deterministic execution policy.
// Strict mode permits only a tiny diagnostic surface. Trusted mode still
// requires an explicit executable allowlist, but intentionally grants that
// executable the authority of the service account.
func ValidateNativeCommand(command string, args []string, mode NativeMode, allowed map[string]bool) error {
	if command == "" || filepath.Base(command) != command {
		return errors.New("native command must be a bare executable name")
	}
	if len(command) > 128 {
		return errors.New("native command too long")
	}
	if len(args) > 64 {
		return errors.New("too many command arguments")
	}
	for _, arg := range args {
		if len(arg) > 4096 {
			return errors.New("command argument too large")
		}
		if strings.IndexByte(arg, 0) >= 0 {
			return errors.New("command argument contains NUL")
		}
	}

	switch mode {
	case NativeStrict:
		return validateStrict(command, args)
	case NativeTrusted:
		if !allowed[command] {
			return fmt.Errorf("command %q is not allowlisted", command)
		}
		return nil
	default:
		return fmt.Errorf("unsupported native execution mode %q", mode)
	}
}

func validateStrict(command string, args []string) error {
	switch command {
	case "echo":
		if len(args) > 32 {
			return errors.New("too many echo arguments")
		}
		return nil
	case "uptime", "pwd", "date", "uname", "id", "ls":
		if len(args) != 0 {
			return fmt.Errorf("%s does not accept arguments in strict execution mode", command)
		}
		return nil
	default:
		return fmt.Errorf("native command %q is not permitted in strict execution mode", command)
	}
}
