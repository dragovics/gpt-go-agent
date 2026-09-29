package security

import "strings"

// SanitizedEnvironment removes credentials and credential-like variables before
// invoking untrusted/LLM-selected child commands.
func SanitizedEnvironment(base []string) []string {
	out := make([]string, 0, len(base))
	for _, item := range base {
		name, _, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		upper := strings.ToUpper(name)
		if strings.Contains(upper, "SECRET") ||
			strings.Contains(upper, "TOKEN") ||
			strings.Contains(upper, "PASSWORD") ||
			strings.Contains(upper, "CREDENTIAL") ||
			strings.HasSuffix(upper, "_KEY") {
			continue
		}
		out = append(out, item)
	}
	return out
}
