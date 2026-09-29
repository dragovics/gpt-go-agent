package openai

import "context"

type SessionReader struct { Client *Client }

type SessionState struct {
	ID string
	EnvironmentID string
	RemoteURL string
	RequiresConnection bool
	Failed bool
}

func (r SessionReader) GetSession(ctx context.Context, id string) (SessionState, error) {
	s, err := r.Client.RetrieveSession(ctx, id)
	if err != nil { return SessionState{}, err }
	state := SessionState{ID:s.ID, EnvironmentID:s.Environment.ID, RemoteURL:s.Environment.RemoteURL}
	for _, a := range s.RequiredActions {
		if a.Type == "environment_connection" { state.RequiresConnection = true }
	}
	return state, nil
}
