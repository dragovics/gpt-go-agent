package codex

import (
	"context"
	"errors"
	"os"
	"os/exec"
)

var ErrNotConfigured = errors.New("codex executor is not configured")

type Executor struct {
	Command string
	Args    []string
	Env     []string
	Dir     string
}

func NewExecutor(command string, args ...string) *Executor {
	if command == "" {
		command = "codex"
	}
	return &Executor{Command: command, Args: args}
}

// ExecServer builds the official Codex self-hosted executor invocation.
// Credentials are supplied through the process environment, never arguments.
func ExecServer(remoteURL, environmentID, workspace string) *Executor {
	e := NewExecutor("codex", "exec-server", "--remote", remoteURL, "--environment-id", environmentID)
	if workspace != "" {
		e.Env = append(e.Env, "CODEX_WORKSPACE="+workspace)
	}
	return e
}

func (e *Executor) Start(ctx context.Context) (*exec.Cmd, error) {
	if len(e.Args) == 0 {
		return nil, ErrNotConfigured
	}
	cmd := exec.CommandContext(ctx, e.Command, e.Args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if len(e.Env) > 0 {
		cmd.Env = append(os.Environ(), e.Env...)
	}
	if e.Dir != "" {
		cmd.Dir = e.Dir
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}
