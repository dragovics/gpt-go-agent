//go:build !linux

package security

import "os/exec"

// ConfigureProcessGroup is a no-op on non-Linux development builds. Production
// release artifacts target Linux where process-group cancellation is enforced.
func ConfigureProcessGroup(_ *exec.Cmd) {}
