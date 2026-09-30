package main

import (
	"context"
	"flag"
	"log"
	"net"
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
	"github.com/dragovics/gpt-go-agent/internal/policy"
	"github.com/dragovics/gpt-go-agent/internal/server"
	"github.com/dragovics/gpt-go-agent/internal/webhook"
)

func main() {
	defaults := config.Default()
	listen := flag.String("listen", getenv("AGENT_LISTEN_ADDR", defaults.ListenAddr), "HTTP listen address")
	workspace := flag.String("workspace", getenv("AGENT_WORKSPACE", defaults.Workspace), "execution workspace")
	token := flag.String("token", os.Getenv("AGENT_MCP_TOKEN"), "MCP bearer token")
	allowWrite := flag.Bool("allow-write", getenvBool("AGENT_ALLOW_WRITE", false), "enable MCP workspace writes")
	allowExec := flag.Bool("allow-command-exec", getenvBool("AGENT_ALLOW_COMMAND_EXEC", false), "enable MCP native command execution")
	commandModeRaw := flag.String("command-mode", getenv("AGENT_COMMAND_EXEC_MODE", "strict"), "MCP command mode: strict or trusted")

	webhookEnabled := flag.Bool("webhook", getenvBool("AGENT_WEBHOOK_ENABLED", false), "enable durable webhook execution pipeline")
	webhookToken := flag.String("webhook-token", firstNonEmpty(os.Getenv("AGENT_WEBHOOK_TOKEN"), os.Getenv("OPENAI_WEBHOOK_SECRET")), "webhook bearer token")
	middlemanURL := flag.String("middleman-url", getenv("AGENT_MIDDLEMAN_URL", "http://127.0.0.1:20128/v1"), "Middleman OpenAI-compatible base URL")
	middlemanKey := flag.String("middleman-key", os.Getenv("AGENT_MIDDLEMAN_KEY"), "Middleman API key")
	middlemanModel := flag.String("middleman-model", getenv("AGENT_MIDDLEMAN_MODEL", "glm-5.3"), "Middleman model identifier")
	codexBin := flag.String("codex-bin", getenv("AGENT_CODEX_BIN", "codex"), "Path to Codex CLI binary")
	codexEnabled := flag.Bool("codex", getenvBool("AGENT_CODEX_ENABLED", false), "allow webhook plans to invoke Codex")
	codexAllowWrite := flag.Bool("codex-allow-write", getenvBool("AGENT_CODEX_ALLOW_WRITE", false), "allow Codex workspace-write sandbox")
	flag.Parse()

	commandMode, err := policy.ParseNativeMode(*commandModeRaw)
	if err != nil {
		log.Fatal(err)
	}
	if (*allowWrite || *allowExec) && strings.TrimSpace(*token) == "" {
		log.Fatal("AGENT_MCP_TOKEN is required when MCP write or command execution is enabled")
	}
	if commandMode == policy.NativeTrusted && *allowExec && len(parseCommandSet(os.Getenv("AGENT_ALLOWED_COMMANDS"))) == 0 {
		log.Fatal("trusted command mode requires AGENT_ALLOWED_COMMANDS")
	}
	if *webhookEnabled && strings.TrimSpace(*webhookToken) == "" {
		log.Fatal("AGENT_WEBHOOK_TOKEN is required when webhook execution is enabled")
	}
	if *codexAllowWrite && !*codexEnabled {
		log.Fatal("AGENT_CODEX_ALLOW_WRITE requires AGENT_CODEX_ENABLED")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := defaults
	cfg.Workspace = *workspace

	auditPath := getenv("AGENT_AUDIT_PATH", cfg.AuditPath)
	auditLog := audit.New(auditPath)

	mcpServer, err := mcp.New(mcp.Config{
		Version:          cfg.Version,
		Workspace:        *workspace,
		Token:            *token,
		AllowWrite:       *allowWrite,
		AllowCommandExec: *allowExec,
		CommandMode:      commandMode,
		AllowedCommands:  parseCommandSet(os.Getenv("AGENT_ALLOWED_COMMANDS")),
		CommandTimeout:   getenvDuration("AGENT_COMMAND_TIMEOUT", cfg.CommandTimeout),
		MaxOutputBytes:   getenvInt("AGENT_MCP_MAX_OUTPUT_BYTES", cfg.MaxOutputBytes),
	}, auditLog)
	if err != nil {
		log.Fatal(err)
	}
	defer mcpServer.Close()

	var worker *webhook.Worker
	if *webhookEnabled {
		gk := middleman.New(middleman.Config{
			BaseURL:                 *middlemanURL,
			APIKey:                  *middlemanKey,
			Model:                   *middlemanModel,
			UserAgent:               "gpt-go-agent/" + cfg.Version,
			CompatibilityProfile:    strings.TrimSpace(os.Getenv("AGENT_MIDDLEMAN_COMPAT_PROFILE")),
			Timeout:                 getenvDuration("AGENT_MIDDLEMAN_TIMEOUT", 30*time.Second),
			MaxAttempts:             getenvInt("AGENT_MIDDLEMAN_MAX_ATTEMPTS", 3),
			RetryBaseDelay:          getenvDuration("AGENT_MIDDLEMAN_RETRY_BASE", 250*time.Millisecond),
			CircuitFailureThreshold: getenvInt("AGENT_MIDDLEMAN_CIRCUIT_THRESHOLD", 3),
			CircuitOpenDuration:     getenvDuration("AGENT_MIDDLEMAN_CIRCUIT_OPEN", 10*time.Second),
		})

		executor := &webhook.DefaultExecutor{
			Workspace:       *workspace,
			CodexBin:        *codexBin,
			MaxOutputBytes:  getenvInt("AGENT_WEBHOOK_MAX_OUTPUT_BYTES", 64*1024),
			CodexEnabled:    *codexEnabled,
			CodexAllowWrite: *codexAllowWrite,
		}
		jobStore := webhook.NewJobStore(getenv("AGENT_WEBHOOK_STORE", "/var/lib/gpt-go-agent/jobs/store.json"))
		worker, err = webhook.NewWorker(gk, executor, webhook.Config{
			Workers:              getenvInt("AGENT_WEBHOOK_WORKERS", 4),
			QueueSize:            getenvInt("AGENT_WEBHOOK_QUEUE_SIZE", 100),
			MaxJobs:              getenvInt("AGENT_WEBHOOK_MAX_JOBS", 10_000),
			RateLimitPerMinute:   getenvInt("AGENT_WEBHOOK_RATE_LIMIT_PER_MINUTE", 120),
			JobTimeout:           getenvDuration("AGENT_WEBHOOK_JOB_TIMEOUT", 3*time.Minute),
			Store:                jobStore,
			WebhookSecret:        *webhookToken,
			RequireWebhookAuth:   true,
			Retention:            getenvDuration("AGENT_WEBHOOK_RETENTION", 7*24*time.Hour),
			CleanupInterval:      getenvDuration("AGENT_WEBHOOK_CLEANUP_INTERVAL", time.Hour),
			MaxGatekeeperRetries: getenvInt("AGENT_WEBHOOK_MAX_GATEKEEPER_RETRIES", 0),
			RetryBaseDelay:       getenvDuration("AGENT_WEBHOOK_RETRY_BASE", 500*time.Millisecond),
			Auditor:              auditLog,
			MaxOutputBytes:       getenvInt("AGENT_WEBHOOK_MAX_OUTPUT_BYTES", 64*1024),
		})
		if err != nil {
			log.Fatal(err)
		}
		defer worker.Close()
	}

	status := server.New(cfg.Version)
	if worker != nil {
		status.Metrics = worker.Metrics
		status.Ready = worker.Ready
	}

	handler := http.NewServeMux()
	statusHandler := status.Handler()
	handler.Handle("/healthz", statusHandler)
	handler.Handle("/readyz", statusHandler)
	handler.Handle("/metrics", statusHandler)
	handler.Handle("/mcp", mcpServer.Handler())
	if worker != nil {
		webhookHandler := worker.Handler()
		handler.Handle("/webhook", webhookHandler)
		handler.Handle("/webhook/", webhookHandler)
	}

	srv := &http.Server{
		Addr:              *listen,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}

	if strings.TrimSpace(*token) == "" && !listenIsLoopback(*listen) {
		log.Fatal("AGENT_MCP_TOKEN is required for a non-loopback listener")
	}

	go func() {
		log.Printf("gpt-go-agent version=%s listen=%s webhook=%t command_mode=%s", cfg.Version, *listen, *webhookEnabled, commandMode)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("server fatal error: %v", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Printf("shutdown signal received")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("server forced shutdown: %v", err)
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
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		log.Printf("invalid boolean environment value; using default %t", fallback)
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
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func parseCommandSet(raw string) map[string]bool {
	out := map[string]bool{}
	for _, name := range strings.Fields(strings.ReplaceAll(raw, ",", " ")) {
		out[name] = true
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func listenIsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
