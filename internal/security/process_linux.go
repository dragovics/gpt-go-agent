//go:build linux

package security

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// ConfigureProcessGroup makes context cancellation terminate the whole process
// group rather than only the direct child. Releases target Linux/systemd.
func ConfigureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
