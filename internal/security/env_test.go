package security

import "testing"

func TestSanitizedEnvironment(t *testing.T) {
	got := SanitizedEnvironment([]string{
		"PATH=/usr/bin",
		"AGENT_MIDDLEMAN_KEY=secret",
		"OPENAI_WEBHOOK_SECRET=secret",
		"GITHUB_TOKEN=secret",
		"DB_PASSWORD=secret",
		"NORMAL_VALUE=ok",
	})
	joined := "\n"
	for _, v := range got {
		joined += v + "\n"
	}
	if len(got) != 2 || got[0] != "PATH=/usr/bin" || got[1] != "NORMAL_VALUE=ok" {
		t.Fatalf("unexpected sanitized environment: %s", joined)
	}
}
