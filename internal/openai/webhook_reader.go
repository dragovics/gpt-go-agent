package openai

import "context"

type WebhookReader struct { Client *Client }

func (r WebhookReader) GetSession(ctx context.Context, id string) (struct {
	ID string
	EnvironmentID string
	RemoteURL string
	RequiresConnection bool
	Failed bool
}, error) {
	s, err := r.Client.RetrieveSession(ctx, id)
	if err != nil { return struct {
		ID string; EnvironmentID string; RemoteURL string; RequiresConnection bool; Failed bool
	}{}, err }
	state := struct {
		ID string; EnvironmentID string; RemoteURL string; RequiresConnection bool; Failed bool
	}{ID:s.ID, EnvironmentID:s.Environment.ID, RemoteURL:s.Environment.RemoteURL}
	for _, a := range s.RequiredActions {
		if a.Type == "environment_connection" { state.RequiresConnection = true }
	}
	return state, nil
}
