package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dragovics/gpt-go-agent/internal/audit"
	"github.com/dragovics/gpt-go-agent/internal/config"
	"github.com/dragovics/gpt-go-agent/internal/mcp"
	"github.com/dragovics/gpt-go-agent/internal/middleman"
	"github.com/dragovics/gpt-go-agent/internal/server"
	"github.com/dragovics/gpt-go-agent/internal/webhook"
)

func main() {
	listen := flag.String("listen", getenv("AGENT_LISTEN_ADDR", "127.0.0.1:8787"), "HTTP listen address")
	workspace := flag.String("workspace", getenv("AGENT_WORKSPACE", "."), "execution workspace")
	token := flag.String("token", os.Getenv("AGENT_MCP_TOKEN"), "MCP bearer token")
	allowWrite := flag.Bool("allow-write", getenvBool("AGENT_ALLOW_WRITE", false), "enable workspace writes")
	allowExec := flag.Bool("allow-command-exec", getenvBool("AGENT_ALLOW_COMMAND_EXEC", false), "enable restricted command execution")

	webhookEnabled := flag.Bool("webhook", getenvBool("AGENT_WEBHOOK_ENABLED", false), "enable durable webhook execution pipeline")
	middlemanURL := flag.String("middleman-url", getenv("AGENT_MIDDLEMAN_URL", "http://127.0.0.1:20128/v1"), "Middleman LLM OpenAI-compatible base URL")
	middlemanKey := flag.String("middleman-key", os.Getenv("AGENT_MIDDLEMAN_KEY"), "Middleman LLM API key")
	middlemanModel := flag.String("middleman-model", getenv("AGENT_MIDDLEMAN_MODEL", "glm-5.3"), "Middleman LLM model identifier")
	codexBin := flag.String("codex-bin", getenv("AGENT_CODEX_BIN", "codex"), "Path to Codex CLI binary")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := config.Default()
	cfg.Workspace = *workspace

	auditPath := getenv("AGENT_AUDIT_PATH", cfg.AuditPath)
	auditLog := audit.New(auditPath)

	mcpServer, err := mcp.New(mcp.Config{
		Version:          cfg.Version,
		Workspace:        *workspace,
		Token:            *token,
		AllowWrite:       *allowWrite,
		AllowCommandExec: *allowExec,
		CommandTimeout:   cfg.CommandTimeout,
		MaxOutputBytes:   cfg.MaxOutputBytes,
	}, auditLog)
	if err != nil {
		log.Fatal(err)
	}

	status := server.New(cfg.Version)
	handler := http.NewServeMux()
	handler.Handle("/healthz", status.Handler())
	handler.Handle("/readyz", status.Handler())
	handler.Handle("/metrics", status.Handler())
	handler.Handle("/mcp", mcpServer.Handler())

	var worker *webhook.Worker
	if *webhookEnabled {
		webhookSecret := getenv("AGENT_WEBHOOK_TOKEN", os.Getenv("OPENAI_WEBHOOK_SECRET"))
		if strings.TrimSpace(webhookSecret) == "" {
			log.Fatal("AGENT_WEBHOOK_TOKEN is required when AGENT_WEBHOOK_ENABLED=1")
		}

		gk := middleman.New(middleman.Config{
			BaseURL:                 *middlemanURL,
			APIKey:                  *middlemanKey,
			Model:                   *middlemanModel,
			Timeout:                 getenvDuration("AGENT_MIDDLEMAN_TIMEOUT", 30*time.Second),
			MaxAttempts:             getenvInt("AGENT_MIDDLEMAN_MAX_ATTEMPTS", 3),
			RetryBaseDelay:          getenvDuration("AGENT_MIDDLEMAN_RETRY_BASE", 250*time.Millisecond),
			CircuitFailureThreshold: getenvInt("AGENT_MIDDLEMAN_CIRCUIT_THRESHOLD", 3),
			CircuitOpenDuration:     getenvDuration("AGENT_MIDDLEMAN_CIRCUIT_OPEN", 10*time.Second),
		})

		executor := &webhook.DefaultExecutor{
			Workspace:       *workspace,
			CodexBin:        *codexBin,
			CodexAllowWrite: getenvBool("AGENT_CODEX_ALLOW_WRITE", false),
			MaxOutputBytes:  getenvInt("AGENT_WEBHOOK_MAX_OUTPUT_BYTES", 64*1024),
		}
		jobStorePath := getenv("AGENT_WEBHOOK_STORE", "/var/lib/gpt-go-agent/jobs/store.json")
		jobStore := webhook.NewJobStore(jobStorePath)
		worker = webhook.NewWorker(gk, executor, webhook.Config{
			Workers:              getenvInt("AGENT_WEBHOOK_WORKERS", 4),
			Store:                jobStore,
			WebhookSecret:        webhookSecret,
			RequireWebhookAuth:   true,
			Retention:            getenvDuration("AGENT_WEBHOOK_RETENTION", 7*24*time.Hour),
			CleanupInterval:      getenvDuration("AGENT_WEBHOOK_CLEANUP_INTERVAL", time.Hour),
			MaxGatekeeperRetries: getenvInt("AGENT_WEBHOOK_MAX_GATEKEEPER_RETRIES", 2),
			RetryBaseDelay:       getenvDuration("AGENT_WEBHOOK_RETRY_BASE", 500*time.Millisecond),
			Auditor:              auditLog,
			MaxOutputBytes:       getenvInt("AGENT_WEBHOOK_MAX_OUTPUT_BYTES", 64*1024),
		})
		defer worker.Close()

		status.Metrics = worker.Metrics
		status.Ready = func() bool {
			return strings.TrimSpace(*middlemanURL) != "" && strings.TrimSpace(*middlemanModel) != ""
		}

		webhookHandler := worker.Handler()
		handler.Handle("/webhook", webhookHandler)
		handler.Handle("/webhook/", webhookHandler)
	} else {
		status.Ready = func() bool { return true }
	}

	srv := &http.Server{
		Addr:              *listen,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}

	go func() {
		log.Printf("gpt-go-agent HTTP server starting on %s (webhook=%t)", *listen, *webhookEnabled)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server fatal error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Printf("shutdown signal received, shutting down gracefully")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("server forced to shutdown: %v", err)
	} else {
		log.Printf("server exited gracefully")
	}
}

func getenv(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func getenvBool(name string, fallback bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	case "":
		return fallback
	default:
		return fallback
	}
}

func getenvDuration(name string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

func getenvInt(name string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
