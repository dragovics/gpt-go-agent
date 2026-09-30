package security

import (
	"strings"
	"testing"
)

func TestSanitizedEnvironment(t *testing.T) {
	got := SanitizedEnvironment([]string{
		"PATH=/usr/bin",
		"AGENT_MIDDLEMAN_KEY=secret",
		"OPENAI_WEBHOOK_SECRET=secret",
		"GITHUB_TOKEN=secret",
		"DB_PASSWORD=secret",
		"SSH_AUTH_SOCK=/tmp/agent.sock",
		"GIT_ASKPASS=/tmp/helper",
		"NORMAL_VALUE=ok",
	})
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "secret") || strings.Contains(joined, "SSH_AUTH_SOCK") || strings.Contains(joined, "GIT_ASKPASS") {
		t.Fatalf("sensitive environment leaked: %s", joined)
	}
	if !strings.Contains(joined, "PATH=/usr/bin") || !strings.Contains(joined, "NORMAL_VALUE=ok") {
		t.Fatalf("expected safe values missing: %s", joined)
	}
}

func TestMinimalEnvironment(t *testing.T) {
	got := MinimalEnvironment([]string{
		"PATH=/usr/bin",
		"LANG=C.UTF-8",
		"LC_ALL=C",
		"HOME=/home/service",
		"NORMAL_VALUE=ok",
	})
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "HOME=") || strings.Contains(joined, "NORMAL_VALUE=") {
		t.Fatalf("unexpected variable in minimal environment: %s", joined)
	}
	if !strings.Contains(joined, "PATH=/usr/bin") || !strings.Contains(joined, "LANG=C.UTF-8") {
		t.Fatalf("expected process basics missing: %s", joined)
	}
}
