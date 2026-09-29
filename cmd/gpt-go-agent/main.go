package main

import (
	"log"
	"net/http"
	"os"

	"github.com/dragovics/gpt-go-agent/internal/agent"
	"github.com/dragovics/gpt-go-agent/internal/config"
	"github.com/dragovics/gpt-go-agent/internal/server"
)

func main() {
	cfg := config.Default()
	if v := os.Getenv("GPT_GO_AGENT_VERSION"); v != "" {
		cfg.Version = v
	}
	a := agent.New(cfg.Version)
	s := server.New(a)
	log.Printf("gpt-go-agent %s listening on %s", cfg.Version, cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, s.Handler()); err != nil {
		log.Fatal(err)
	}
}
