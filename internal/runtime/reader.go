package runtime

import (
	"context"

	"github.com/dragovics/gpt-go-agent/internal/openai"
	"github.com/dragovics/gpt-go-agent/internal/webhook"
)

type SessionReader struct { Client *openai.Client }

func (r SessionReader) GetSession(ctx context.Context, id string) (webhook.SessionState, error) {
	s, err := r.Client.RetrieveSession(ctx, id)
	if err != nil { return webhook.SessionState{}, err }
	state := webhook.SessionState{
		ID: s.ID,
		EnvironmentID: s.Environment.ID,
		RemoteURL: s.Environment.RemoteURL,
		Failed: s.Status == "failed",
	}
	for _, a := range s.RequiredActions {
		if a.Type == "environment_connection" {
			state.RequiresConnection = true
		}
	}
	return state, nil
}
