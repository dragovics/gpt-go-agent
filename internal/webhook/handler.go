package webhook

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type WebhookRequest struct {
	Intent  string `json:"intent"`
	Context string `json:"context,omitempty"`
}

type WebhookResponse struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
}

type requestLimiter struct {
	mu    sync.Mutex
	start time.Time
	count int
	limit int
}

func (l *requestLimiter) allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.start.IsZero() || now.Sub(l.start) >= time.Minute {
		l.start = now
		l.count = 0
	}
	if l.count >= l.limit {
		return false
	}
	l.count++
	return true
}

// Handler returns an http.Handler that manages webhook ingestion and status queries.
func (w *Worker) Handler() http.Handler {
	mux := http.NewServeMux()
	limiter := &requestLimiter{limit: 120}

	authorized := func(rw http.ResponseWriter, req *http.Request) bool {
		if !w.requireWebhookAuth {
			return true
		}
		const prefix = "Bearer "
		header := req.Header.Get("Authorization")
		if !strings.HasPrefix(header, prefix) || w.webhookSecret == "" {
			rw.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(rw, "unauthorized", http.StatusUnauthorized)
			return false
		}
		provided := strings.TrimSpace(strings.TrimPrefix(header, prefix))
		if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(w.webhookSecret)) != 1 {
			rw.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(rw, "unauthorized", http.StatusUnauthorized)
			return false
		}
		return true
	}

	mux.HandleFunc("/webhook", func(rw http.ResponseWriter, req *http.Request) {
		if !authorized(rw, req) {
			return
		}
		switch req.Method {
		case http.MethodPost:
			if !limiter.allow(time.Now()) {
				rw.Header().Set("Retry-After", "60")
				http.Error(rw, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			var body WebhookRequest
			if err := json.NewDecoder(http.MaxBytesReader(rw, req.Body, 1<<20)).Decode(&body); err != nil {
				http.Error(rw, "invalid json body", http.StatusBadRequest)
				return
			}
			correlationID := strings.TrimSpace(req.Header.Get("X-Request-ID"))
			if len(correlationID) > 128 {
				http.Error(rw, "request id too long", http.StatusBadRequest)
				return
			}
			job, duplicate, err := w.EnqueueWithKeyAndCorrelation(body.Intent, body.Context, req.Header.Get("Idempotency-Key"), correlationID)
			if err != nil {
				status := http.StatusServiceUnavailable
				if strings.Contains(err.Error(), "too long") || strings.Contains(err.Error(), "cannot be empty") {
					status = http.StatusBadRequest
				}
				if strings.Contains(err.Error(), "idempotency key conflicts") {
					status = http.StatusConflict
				}
				http.Error(rw, err.Error(), status)
				return
			}
			rw.Header().Set("X-Request-ID", job.CorrelationID)
			rw.Header().Set("Content-Type", "application/json")
			if duplicate {
				rw.WriteHeader(http.StatusOK)
			} else {
				rw.WriteHeader(http.StatusAccepted)
			}
			status := string(StatusPending)
			if duplicate {
				status = string(job.Status)
			}
			_ = json.NewEncoder(rw).Encode(WebhookResponse{
				JobID:  job.ID,
				Status: status,
			})

		case http.MethodGet:
			if !limiter.allow(time.Now()) {
				rw.Header().Set("Retry-After", "60")
				http.Error(rw, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			limit, offset := 50, 0
			var err error
			if raw := req.URL.Query().Get("limit"); raw != "" {
				limit, err = strconv.Atoi(raw)
				if err != nil {
					http.Error(rw, "invalid limit", http.StatusBadRequest)
					return
				}
			}
			if raw := req.URL.Query().Get("offset"); raw != "" {
				offset, err = strconv.Atoi(raw)
				if err != nil {
					http.Error(rw, "invalid offset", http.StatusBadRequest)
					return
				}
			}
			if limit < 1 || limit > 200 || offset < 0 {
				http.Error(rw, "invalid pagination", http.StatusBadRequest)
				return
			}
			list, total := w.ListJobs(limit, offset)
			rw.Header().Set("X-Total-Count", strconv.Itoa(total))
			rw.Header().Set("X-Limit", strconv.Itoa(limit))
			rw.Header().Set("X-Offset", strconv.Itoa(offset))
			rw.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(rw).Encode(list)

		default:
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/webhook/", func(rw http.ResponseWriter, req *http.Request) {
		if !authorized(rw, req) {
			return
		}
		if !limiter.allow(time.Now()) {
			rw.Header().Set("Retry-After", "60")
			http.Error(rw, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
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
	})

	return mux
}
