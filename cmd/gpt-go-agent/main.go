package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dragovics/gpt-go-agent/internal/agent"
	"github.com/dragovics/gpt-go-agent/internal/codex"
	"github.com/dragovics/gpt-go-agent/internal/config"
	openaiagent "github.com/dragovics/gpt-go-agent/internal/openai"
	"github.com/dragovics/gpt-go-agent/internal/runtime"
	"github.com/dragovics/gpt-go-agent/internal/server"
	"github.com/dragovics/gpt-go-agent/internal/webhook"
)

func main() {
	remote := flag.String("remote", os.Getenv("CODEX_REMOTE_URL"), "Codex environment remote URL")
	envID := flag.String("environment-id", os.Getenv("CODEX_ENVIRONMENT_ID"), "Codex environment ID")
	workspace := flag.String("workspace", os.Getenv("CODEX_WORKSPACE"), "workspace")
	model := flag.String("model", os.Getenv("CODEX_MODEL"), "Agents API model")
	sessionID := flag.String("session", os.Getenv("AGENT_SESSION_ID"), "existing Agents API session")
	input := flag.String("input", "", "submit input to an existing session")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := config.Default()
	if *workspace != "" { cfg.Workspace = *workspace }
	client := openaiagent.NewClient()
	a := agent.New(cfg.Version)

	journalPath := os.Getenv("AGENT_WEBHOOK_JOURNAL")
	if journalPath == "" { journalPath = "agent-webhook.jsonl" }
	journal := webhook.NewJournal(journalPath)
	queue := webhook.NewQueue(256, journal)
	if err := queue.Recover(); err != nil { log.Printf("webhook recovery: %v", err) }

	supervisor := codex.NewSupervisor(cfg.Workspace, nil)
	worker := &webhook.Worker{Queue: queue, Reader: runtime.SessionReader{Client: client}, Supervisor: supervisor}
	go worker.Run(ctx)

	handler := http.Handler(server.New(a).Handler())
	if secret := os.Getenv("OPENAI_WEBHOOK_SECRET"); secret != "" {
		webhookHandler := &webhook.Handler{Secret: secret, Queue: queue, Tolerance: 5*time.Minute}
		mux := http.NewServeMux()
		mux.Handle("/healthz", handler)
		mux.Handle("/webhooks/openai", webhookHandler)
		handler = mux
	} else {
		log.Printf("OPENAI_WEBHOOK_SECRET is not set; webhook endpoint disabled")
	}

	health := &http.Server{Addr: cfg.ListenAddr, Handler: handler}
	go func() {
		if err := health.ListenAndServe(); err != nil && err != http.ErrServerClosed { log.Printf("health server: %v", err) }
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		supervisor.StopAll()
		_ = health.Shutdown(shutdownCtx)
	}()

	if *remote != "" || *envID != "" {
		if *remote == "" || *envID == "" { log.Fatal("-remote and -environment-id must be provided together") }
		if err := supervisor.Start(ctx, *envID, *remote); err != nil { log.Fatal(err) }
		<-ctx.Done()
		return
	}

	if *sessionID != "" {
		if *input == "" { fmt.Fprintln(os.Stderr, "-input is required with -session"); os.Exit(2) }
		if err := client.SubmitInput(ctx, *sessionID, *input); err != nil { log.Fatal(err) }
		<-ctx.Done()
		return
	}

	if *model == "" {
		log.Fatal("CODEX_MODEL or -model is required")
	}

	s, err := client.CreateSelfHostedSession(ctx, *model, "Work in the provided environment and report concrete results.", "/workspace", *input)
	if err != nil { log.Fatal(err) }
	log.Printf("session=%s environment=%s", s.ID, s.Environment.ID)

	if s.Environment.RemoteURL == "" || s.Environment.ID == "" {
		log.Fatal("session did not return self-hosted environment connection details")
	}
	if err := supervisor.Start(ctx, s.Environment.ID, s.Environment.RemoteURL); err != nil { log.Fatal(err) }
	<-ctx.Done()
}
