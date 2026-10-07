package server

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type Server struct {
	Version string
	Metrics func() map[string]int64
	Ready   func() bool
}

func New(version string) *Server {
	return &Server{Version: version}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.healthz)
	mux.HandleFunc("/readyz", s.readyz)
	mux.HandleFunc("/metrics", s.metrics)
	return mux
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "version": s.Version})
}

func (s *Server) readyz(w http.ResponseWriter, _ *http.Request) {
	if s.Ready != nil && !s.Ready() {
		http.Error(w, `{"ready":false}`, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ready": true})
}

func (s *Server) metrics(w http.ResponseWriter, _ *http.Request) {
	if s.Metrics == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	for k, v := range s.Metrics() {
		fmt.Fprintf(w, "gpt_go_agent_%s %d\n", k, v)
	}
}
