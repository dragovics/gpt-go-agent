package webhook

import (
	"encoding/json"
	"net/http"
	"strings"
)

type WebhookRequest struct {
	Intent  string `json:"intent"`
	Context string `json:"context,omitempty"`
}

type WebhookResponse struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
}

// Handler returns an http.Handler that manages webhook ingestion and status queries.
func (w *Worker) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/webhook", w.handleWebhookCollection)
	mux.HandleFunc("/webhook/", w.handleWebhookItem)
	return mux
}

func (w *Worker) handleWebhookCollection(rw http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodPost:
		w.handlePostWebhook(rw, req)
	case http.MethodGet:
		w.handleGetWebhookList(rw, req)
	default:
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (w *Worker) handlePostWebhook(rw http.ResponseWriter, req *http.Request) {
	var body WebhookRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(rw, "invalid json body", http.StatusBadRequest)
		return
	}
	job, err := w.Enqueue(body.Intent, body.Context)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusServiceUnavailable)
		return
	}
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(rw).Encode(WebhookResponse{
		JobID:  job.ID,
		Status: string(job.Status),
	})
}

func (w *Worker) handleGetWebhookList(rw http.ResponseWriter, req *http.Request) {
	w.mu.RLock()
	list := make([]*Job, 0, len(w.jobs))
	for _, j := range w.jobs {
		cp := *j
		list = append(list, &cp)
	}
	w.mu.RUnlock()
	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(list)
}

func (w *Worker) handleWebhookItem(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(req.URL.Path, "/webhook/")
	job, ok := w.GetJob(id)
	if !ok {
		http.NotFound(rw, req)
		return
	}
	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(job)
}
