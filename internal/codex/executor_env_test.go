package codex

import (
	"strings"
	"testing"
)

func TestInheritedEnvironmentStripsApplicationCredentials(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "app-secret")
	t.Setenv("OPENAI_EXECUTOR_API_KEY", "executor-secret")
	t.Setenv("OPENAI_WEBHOOK_SECRET", "webhook-secret")
	env := strings.Join(inheritedEnvironment(), "\n")
	for _, secret := range []string{"OPENAI_API_KEY=app-secret", "OPENAI_EXECUTOR_API_KEY=executor-secret", "OPENAI_WEBHOOK_SECRET=webhook-secret"} {
		if strings.Contains(env, secret) {
			t.Fatalf("application credential leaked into executor environment: %q", secret)
		}
	}
}
