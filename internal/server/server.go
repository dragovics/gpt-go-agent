package server

import ("encoding/json"; "net/http"; "github.com/dragovics/gpt-go-agent/internal/agent")

type Server struct { Agent *agent.Agent }
func New(a *agent.Agent)*Server{return &Server{Agent:a}}
func(s *Server)Handler()http.Handler{
	mux:=http.NewServeMux()
	mux.HandleFunc("/healthz",func(w http.ResponseWriter,_ *http.Request){w.Header().Set("content-type","application/json");_=json.NewEncoder(w).Encode(map[string]any{"ok":true,"version":s.Agent.Version})})
	return mux
}
