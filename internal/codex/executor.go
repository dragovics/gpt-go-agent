package codex

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
)

var ErrNotConfigured = errors.New("codex executor is not configured")

// Executor describes a local codex exec-server process.
// Credentials must be supplied through the process environment, never argv.
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
// CODEX_API_KEY may be supplied directly, or via OPENAI_EXECUTOR_API_KEY,
// which is mapped into the restricted executor environment.
func ExecServer(remoteURL, environmentID, workspace string) *Executor {
	e := NewExecutor("codex", "exec-server", "--remote", remoteURL, "--environment-id", environmentID)
	if workspace != "" {
		e.Env = append(e.Env, "CODEX_WORKSPACE="+workspace)
	}
	if key := os.Getenv("CODEX_API_KEY"); key != "" {
		e.Env = append(e.Env, "CODEX_API_KEY="+key)
	} else if key := os.Getenv("OPENAI_EXECUTOR_API_KEY"); key != "" {
		e.Env = append(e.Env, "CODEX_API_KEY="+key)
	}
	return e
}

// inheritedEnvironment deliberately strips application credentials before
// starting the executor. The executor only needs the restricted CODEX_API_KEY.
func inheritedEnvironment() []string {
	deny := map[string]struct{}{
		"OPENAI_API_KEY":           {},
		"OPENAI_EXECUTOR_API_KEY": {},
		"OPENAI_WEBHOOK_SECRET":    {},
	}
	base := make([]string, 0, len(os.Environ()))
	for _, item := range os.Environ() {
		name, _, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		if _, blocked := deny[name]; blocked {
			continue
		}
		base = append(base, item)
	}
	return base
}

func (e *Executor) Start(ctx context.Context) (*exec.Cmd, error) {
	if len(e.Args) == 0 {
		return nil, ErrNotConfigured
	}
	cmd := exec.CommandContext(ctx, e.Command, e.Args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(inheritedEnvironment(), e.Env...)
	if e.Dir != "" {
		cmd.Dir = e.Dir
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}
