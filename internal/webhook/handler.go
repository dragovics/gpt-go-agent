package webhook

import (
	"encoding/json"
	"io"
	"net/http"
	"time"
)

type Event struct {
	ID string `json:"id"`
	Type string `json:"type"`
	Data struct {
		ID string `json:"id"`
		EnvironmentID string `json:"environment_id"`
		EnvironmentType string `json:"environment_type"`
		RequiredAction struct {
			Type string `json:"type"`
		} `json:"required_action"`
		Connect struct {
			RemoteURL string `json:"remote_url"`
		} `json:"connect"`
	} `json:"data"`
}

type Handler struct {
	Secret string
	Queue *Queue
	Tolerance time.Duration
	Now func() time.Time
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20+1))
	if err != nil { http.Error(w, "invalid body", http.StatusBadRequest); return }
	if len(body) > 2<<20 { http.Error(w, "body too large", http.StatusRequestEntityTooLarge); return }
	now := time.Now()
	if h.Now != nil { now = h.Now() }
	if err := Verify(h.Secret, r.Header.Get("webhook-id"), r.Header.Get("webhook-timestamp"), r.Header.Get("webhook-signature"), body, now, h.Tolerance); err != nil {
		http.Error(w, "invalid signature", http.StatusBadRequest); return
	}
	var e Event
	if err := json.Unmarshal(body, &e); err != nil {
		http.Error(w, "invalid event", http.StatusBadRequest); return
	}
	if e.Type != "agent.session.created" && e.Type != "agent.session.action_required" && e.Type != "agent.session.failed" {
		w.WriteHeader(http.StatusOK); return
	}
	if e.Type == "agent.session.action_required" && e.Data.RequiredAction.Type != "environment_connection" {
		w.WriteHeader(http.StatusOK); return
	}
	if h.Queue == nil || !h.Queue.Enqueue(Job{
		ID:e.ID, Type:e.Type, SessionID:e.Data.ID,
		EnvironmentID:e.Data.EnvironmentID, RemoteURL:e.Data.Connect.RemoteURL,
		CreatedAt:now,
	}) {
		http.Error(w, "queue unavailable", http.StatusServiceUnavailable); return
	}
	w.WriteHeader(http.StatusAccepted)
}
