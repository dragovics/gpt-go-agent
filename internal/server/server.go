package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/dragovics/gpt-go-agent/internal/agent"
)

type Server struct {
	Agent   *agent.Agent
	Metrics func() map[string]int64
	Ready   func() bool
}

func New(a *agent.Agent) *Server {
	return &Server{Agent: a}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "version": s.Agent.Version})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		ready := s.Ready == nil || s.Ready()
		w.Header().Set("content-type", "application/json")
		if !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ready": ready})
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/plain; version=0.0.4")
		metrics := map[string]int64{}
		if s.Metrics != nil {
			metrics = s.Metrics()
		}
		for name, value := range metrics {
			_, _ = fmt.Fprintf(w, "gpt_go_agent_%s %d\n", name, value)
		}
	})
	return mux
}
