package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dragovics/gpt-go-agent/internal/agent"
	"github.com/dragovics/gpt-go-agent/internal/audit"
	"github.com/dragovics/gpt-go-agent/internal/config"
	"github.com/dragovics/gpt-go-agent/internal/mcp"
	"github.com/dragovics/gpt-go-agent/internal/server"
)

func main() {
	listen := flag.String("listen", getenv("AGENT_LISTEN_ADDR", "127.0.0.1:8787"), "HTTP listen address")
	workspace := flag.String("workspace", getenv("AGENT_WORKSPACE", "."), "execution workspace")
	token := flag.String("token", os.Getenv("AGENT_MCP_TOKEN"), "MCP bearer token")
	allowWrite := flag.Bool("allow-write", os.Getenv("AGENT_ALLOW_WRITE") == "1", "enable workspace writes")
	allowExec := flag.Bool("allow-command-exec", os.Getenv("AGENT_ALLOW_COMMAND_EXEC") == "1", "enable allowlisted command execution")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := config.Default()
	cfg.Workspace = *workspace

	a := agent.New(cfg.Version)
	auditPath := getenv("AGENT_AUDIT_PATH", cfg.AuditPath)
	auditLog := audit.New(auditPath)

	mcpServer, err := mcp.New(mcp.Config{
		Workspace: *workspace,
		Token: *token,
		AllowWrite: *allowWrite,
		AllowCommandExec: *allowExec,
		CommandTimeout: cfg.CommandTimeout,
		MaxOutputBytes: cfg.MaxOutputBytes,
	}, auditLog)
	if err != nil { log.Fatal(err) }

	status := server.New(a)
	status.Ready = func() bool { return *token != "" || strings.HasPrefix(*listen, "127.0.0.1:") || strings.HasPrefix(*listen, "localhost:") || strings.HasPrefix(*listen, "[::1]:") }
	handler := http.NewServeMux()
	handler.Handle("/healthz", status.Handler())
	handler.Handle("/readyz", status.Handler())
	handler.Handle("/metrics", status.Handler())
	handler.Handle("/mcp", mcpServer.Handler())

	srv := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 2 * time.Minute, IdleTimeout: 2 * time.Minute}

	go func() {
		log.Printf("gpt-go-agent listening on %s workspace=%s write=%t exec=%t", *listen, cfg.Workspace, *allowWrite, *allowExec)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed { log.Printf("server: %v", err) }
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func getenv(name, fallback string) string {
	if v := os.Getenv(name); v != "" { return v }
	return fallback
}
