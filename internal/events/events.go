package events

import "time"

type Type string
const (
	SessionCreated Type="agent.session.created"
	EnvironmentPending Type="agent.session.environment.pending"
	EnvironmentConnected Type="agent.session.environment.connected"
	EnvironmentFailed Type="agent.session.environment.failed"
	SessionRequiresAction Type="agent.session.requires_action"
	SessionFailed Type="agent.session.failed"
)

type Event struct {
	ID string `json:"id"`
	Type Type `json:"type"`
	CreatedAt time.Time `json:"created_at"`
	SessionID string `json:"session_id,omitempty"`
	EnvironmentID string `json:"environment_id,omitempty"`
	RemoteURL string `json:"remote_url,omitempty"`
	Error string `json:"error,omitempty"`
}

type Sink interface { Publish(Event) error }
