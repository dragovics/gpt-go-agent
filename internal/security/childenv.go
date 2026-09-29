package security

import "strings"

// ChildEnvironment returns a reduced environment suitable for subprocesses.
func ChildEnvironment(base []string) []string {
	out := make([]string, 0, len(base))
	for _, item := range base {
		name, _, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		u := strings.ToUpper(name)
		if strings.Contains(u, "SECRET") || strings.Contains(u, "TOKEN") || strings.Contains(u, "PASSWORD") || strings.Contains(u, "CREDENTIAL") || strings.HasSuffix(u, "_KEY") {
			continue
		}
		out = append(out, item)
	}
	return out
}
