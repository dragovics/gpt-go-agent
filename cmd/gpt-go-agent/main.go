package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"
	"os"
	"os/signal"
	"syscall"

	"github.com/dragovics/gpt-go-agent/internal/agent"
	"github.com/dragovics/gpt-go-agent/internal/codex"
	"github.com/dragovics/gpt-go-agent/internal/config"
	"github.com/dragovics/gpt-go-agent/internal/server"
	openaiagent "github.com/dragovics/gpt-go-agent/internal/openai"
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
	a := agent.New(cfg.Version)
	health := &http.Server{Addr: cfg.ListenAddr, Handler: server.New(a).Handler()}
	go func() {
		if err := health.ListenAndServe(); err != nil && err != http.ErrServerClosed { log.Printf("health server: %v", err) }
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = health.Shutdown(shutdownCtx)
	}()

	client:=openaiagent.NewClient()
	if *sessionID!="" {
		if *input=="" { fmt.Fprintln(os.Stderr,"-input is required with -session"); os.Exit(2) }
		if err:=client.SubmitInput(ctx,*sessionID,*input); err!=nil { log.Fatal(err) }
		return
	}

	if *model=="" { log.Fatal("CODEX_MODEL or -model is required") }
	s,err:=client.CreateSelfHostedSession(ctx,*model,"Work in the provided environment and report concrete results.","/workspace",*input)
	if err!=nil { log.Fatal(err) }
	log.Printf("session=%s environment=%s",s.ID,s.Environment.ID)

	if s.Environment.RemoteURL=="" || s.Environment.ID=="" { log.Fatal("session did not return self-hosted environment connection details") }
	if *workspace == "" { *workspace = "/workspace" }
	supervisor := codex.NewSupervisor(*workspace, nil)
	if err := supervisor.Start(ctx, s.Environment.ID, s.Environment.RemoteURL); err != nil { log.Fatal(err) }
	<-ctx.Done()
	supervisor.StopAll()
}
