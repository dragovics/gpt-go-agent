package security

import "strings"

// SanitizedEnvironment removes credential-like variables before invoking
// model-selected child commands. It is intended for explicitly trusted command
// mode; strict execution should prefer MinimalEnvironment.
func SanitizedEnvironment(base []string) []string {
	out := make([]string, 0, len(base))
	for _, item := range base {
		name, _, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		upper := strings.ToUpper(name)
		if isSensitiveName(upper) {
			continue
		}
		out = append(out, item)
	}
	return out
}

// MinimalEnvironment exposes only non-secret process basics. This is the
// default for strict native commands.
func MinimalEnvironment(base []string) []string {
	out := make([]string, 0, 8)
	for _, item := range base {
		name, _, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		upper := strings.ToUpper(name)
		switch {
		case upper == "PATH", upper == "LANG", upper == "TERM", upper == "TMPDIR", strings.HasPrefix(upper, "LC_"):
			out = append(out, item)
		}
	}
	return out
}

func isSensitiveName(upper string) bool {
	return strings.Contains(upper, "SECRET") ||
		strings.Contains(upper, "TOKEN") ||
		strings.Contains(upper, "PASSWORD") ||
		strings.Contains(upper, "CREDENTIAL") ||
		strings.HasSuffix(upper, "_KEY") ||
		upper == "SSH_AUTH_SOCK" ||
		upper == "GIT_ASKPASS" ||
		upper == "SSH_ASKPASS"
}

// ChildEnvironment is kept as the Codex subprocess compatibility environment.
func ChildEnvironment(base []string) []string {
	return SanitizedEnvironment(base)
}
