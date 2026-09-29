package middleman

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Decision represents the structured judgment from the middleman gatekeeper (Satpam + Mandor).
type Decision struct {
	Approved bool     `json:"approved"`
	Reason   string   `json:"reason,omitempty"`
	ExecType string   `json:"exec_type,omitempty"` // "native" (shell/api, $0 quota) or "codex" (heavy coding)
	Command  string   `json:"command,omitempty"`
	Args     []string `json:"args,omitempty"`
}

// Config holds the configuration for the Middleman LLM client.
type Config struct {
	BaseURL                 string
	APIKey                  string
	Model                   string
	Timeout                 time.Duration
	SystemPrompt            string
	MaxAttempts             int
	RetryBaseDelay          time.Duration
	CircuitFailureThreshold int
	CircuitOpenDuration     time.Duration
}

// DefaultSystemPrompt is the default instructions for the Satpam (Policy Enforcer) + Mandor (Technical Translator).
const DefaultSystemPrompt = `Return one valid JSON object with these fields: approved, reason, exec_type, command, args.
Use approved=true for ordinary harmless requests and approved=false when the request is clearly not appropriate.
Use exec_type native or codex. Keep reason brief. Return JSON only.`

// Gatekeeper defines the interface for evaluating incoming intent.
type Gatekeeper interface {
	Evaluate(ctx context.Context, intent string, extraContext string) (Decision, error)
}

// LLMGatekeeper implements Gatekeeper using an OpenAI-compatible endpoint.
type LLMGatekeeper struct {
	cfg        Config
	httpClient *http.Client
	breaker    *circuitBreaker
	statsMu    sync.Mutex
	stats      Stats
}

type Stats struct {
	Requests      int64
	Retries       int64
	Failures      int64
	CircuitOpens  int64
	LastLatencyMs int64
}

type circuitBreaker struct {
	mu         sync.Mutex
	failures   int
	threshold  int
	openUntil  time.Time
	halfOpen   bool
	openWindow time.Duration
}

func newCircuitBreaker(threshold int, openWindow time.Duration) *circuitBreaker {
	if threshold <= 0 {
		threshold = 3
	}
	if openWindow <= 0 {
		openWindow = 10 * time.Second
	}
	return &circuitBreaker{threshold: threshold, openWindow: openWindow}
}

func (b *circuitBreaker) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.openUntil.IsZero() {
		return true
	}
	if now.Before(b.openUntil) {
		return false
	}
	if b.halfOpen {
		return false
	}
	b.halfOpen = true
	return true
}

func (b *circuitBreaker) success() {
	b.mu.Lock()
	b.failures = 0
	b.openUntil = time.Time{}
	b.halfOpen = false
	b.mu.Unlock()
}

func (b *circuitBreaker) failure(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.halfOpen {
		b.openUntil = now.Add(b.openWindow)
		b.halfOpen = false
		return true
	}
	b.failures++
	if b.failures >= b.threshold {
		b.failures = 0
		b.openUntil = now.Add(b.openWindow)
		return true
	}
	return false
}

// New creates a new generic OpenAI-compatible LLM Gatekeeper.
func New(cfg Config) *LLMGatekeeper {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://127.0.0.1:20128/v1" // Default to local 9Router
	}
	if cfg.Model == "" {
		cfg.Model = "glm-5.3"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = DefaultSystemPrompt
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.RetryBaseDelay <= 0 {
		cfg.RetryBaseDelay = 250 * time.Millisecond
	}
	if cfg.CircuitFailureThreshold <= 0 {
		cfg.CircuitFailureThreshold = 3
	}
	if cfg.CircuitOpenDuration <= 0 {
		cfg.CircuitOpenDuration = 10 * time.Second
	}

	return &LLMGatekeeper{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: cfg.Timeout},
		breaker:    newCircuitBreaker(cfg.CircuitFailureThreshold, cfg.CircuitOpenDuration),
	}
}

type openAIChatRequest struct {
	Model       string              `json:"model"`
	Messages    []openAIChatMessage `json:"messages"`
	Temperature float64             `json:"temperature"`
}

type openAIChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type statusError struct {
	status int
	body   string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("middleman API returned status %d: %s", e.status, e.body)
}

func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}
	var se *statusError
	if errors.As(err, &se) {
		return se.status == http.StatusTooManyRequests || se.status >= 500
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func (g *LLMGatekeeper) Evaluate(ctx context.Context, intent string, extraContext string) (Decision, error) {
	if strings.TrimSpace(intent) == "" {
		return Decision{Approved: false, Reason: "intent tidak boleh kosong"}, nil
	}
	started := time.Now()
	g.statsMu.Lock()
	g.stats.Requests++
	g.statsMu.Unlock()

	for attempt := 1; attempt <= g.cfg.MaxAttempts; attempt++ {
		if !g.breaker.allow(time.Now()) {
			g.statsMu.Lock()
			g.stats.Failures++
			g.stats.LastLatencyMs = time.Since(started).Milliseconds()
			g.statsMu.Unlock()
			return Decision{}, errors.New("middleman circuit breaker is open")
		}

		dec, err := g.evaluateOnce(ctx, intent, extraContext)
		if err == nil {
			g.breaker.success()
			g.statsMu.Lock()
			g.stats.LastLatencyMs = time.Since(started).Milliseconds()
			g.statsMu.Unlock()
			return dec, nil
		}
		if !IsRetryableError(err) || attempt == g.cfg.MaxAttempts || ctx.Err() != nil {
			opened := g.breaker.failure(time.Now())
			g.statsMu.Lock()
			g.stats.Failures++
			if opened {
				g.stats.CircuitOpens++
			}
			g.stats.LastLatencyMs = time.Since(started).Milliseconds()
			g.statsMu.Unlock()
			return Decision{}, err
		}

		g.breaker.failure(time.Now())
		g.statsMu.Lock()
		g.stats.Retries++
		g.statsMu.Unlock()
		delay := g.cfg.RetryBaseDelay * time.Duration(1<<(attempt-1))
		if delay > 4*time.Second {
			delay = 4 * time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Decision{}, ctx.Err()
		case <-timer.C:
		}
	}
	return Decision{}, errors.New("middleman retry loop exhausted")
}

func (g *LLMGatekeeper) evaluateOnce(ctx context.Context, intent string, extraContext string) (Decision, error) {
	userContent := fmt.Sprintf("INTENT: %s\nKONTEKS: %s", intent, extraContext)
	reqBody := openAIChatRequest{
		Model: g.cfg.Model,
		Messages: []openAIChatMessage{
			{Role: "system", Content: g.cfg.SystemPrompt},
			{Role: "user", Content: userContent},
		},
		Temperature: 0.1,
	}
	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return Decision{}, fmt.Errorf("marshal request: %w", err)
	}
	endpoint := strings.TrimRight(g.cfg.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(jsonBytes))
	if err != nil {
		return Decision{}, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "opencode/1.0")
	httpReq.Header.Set("originator", "opencode")
	httpReq.Header.Set("version", "1.0")
	if g.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+g.cfg.APIKey)
	}
	resp, err := g.httpClient.Do(httpReq)
	if err != nil {
		return Decision{}, fmt.Errorf("execute middleman request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Decision{}, &statusError{status: resp.StatusCode, body: strings.TrimSpace(string(body))}
	}
	var chatResp openAIChatResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&chatResp); err != nil {
		return Decision{}, fmt.Errorf("decode middleman response: %w", err)
	}
	if chatResp.Error != nil && chatResp.Error.Message != "" {
		return Decision{}, errors.New(chatResp.Error.Message)
	}
	if len(chatResp.Choices) == 0 {
		return Decision{}, errors.New("empty choices from middleman LLM")
	}
	return ParseDecisionJSON(strings.TrimSpace(chatResp.Choices[0].Message.Content))
}

func (g *LLMGatekeeper) Metrics() map[string]int64 {
	g.statsMu.Lock()
	defer g.statsMu.Unlock()
	return map[string]int64{
		"middleman_requests_total":      g.stats.Requests,
		"middleman_retries_total":       g.stats.Retries,
		"middleman_failures_total":      g.stats.Failures,
		"middleman_circuit_opens_total": g.stats.CircuitOpens,
		"middleman_last_latency_ms":     g.stats.LastLatencyMs,
	}
}

// ParseDecisionJSON cleans and parses JSON output even if wrapped in markdown codeblocks.
func ParseDecisionJSON(raw string) (Decision, error) {
	clean := strings.TrimSpace(raw)
	if strings.HasPrefix(clean, "```json") {
		clean = strings.TrimPrefix(clean, "```json")
		clean = strings.TrimSuffix(clean, "```")
	} else if strings.HasPrefix(clean, "```") {
		clean = strings.TrimPrefix(clean, "```")
		clean = strings.TrimSuffix(clean, "```")
	}
	clean = strings.TrimSpace(clean)

	if len(clean) > 32768 {
		return Decision{}, errors.New("decision response too large")
	}
	var d Decision
	if err := json.Unmarshal([]byte(clean), &d); err != nil {
		return Decision{}, fmt.Errorf("invalid json decision (%w): %s", err, clean)
	}
	if d.ExecType != "" && d.ExecType != "native" && d.ExecType != "codex" {
		return Decision{}, fmt.Errorf("invalid exec_type %q", d.ExecType)
	}
	if len(d.Command) > 256 {
		return Decision{}, errors.New("decision command too long")
	}
	if len(d.Args) > 32 {
		return Decision{}, errors.New("too many decision args")
	}
	for _, arg := range d.Args {
		if len(arg) > 4096 {
			return Decision{}, errors.New("decision argument too long")
		}
	}
	return d, nil
}
