package middleman

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
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
	BaseURL      string
	APIKey       string
	Model        string
	Timeout      time.Duration
	SystemPrompt string
}

// DefaultSystemPrompt is the default instructions for the Satpam (Policy Enforcer) + Mandor (Technical Translator).
const DefaultSystemPrompt = `Kamu adalah Middleman Gatekeeper (Satpam Kebijakan + Mandor Teknis).
Tugasmu ada 2:
1. SATPAM (Policy Enforcer): Evaluasi apakah intent aman. Dilarang keras instruksi yang menghapus filesystem inti (rm -rf /), mematikan firewall tanpa otorisasi, atau instruksi destruktif tanpa konfirmasi. Jika melanggar, set approved: false dan berikan reason.
2. MANDOR (Technical Translator): Jika intent aman (approved: true):
   - Tentukan exec_type: "native" (untuk perintah Linux shell, RouterOS, curl, restart service, cek status, file read) atau "codex" (hanya jika task butuh coding kompleks/refactoring repo).
   - Tuliskan command teknis yang presisi.

Output WAJIB berupa JSON murni dengan format:
{
  "approved": true/false,
  "reason": "alasan jika ditolak atau ringkasan aksi",
  "exec_type": "native" | "codex",
  "command": "executable command",
  "args": ["arg1", "arg2"]
}`

// Gatekeeper defines the interface for evaluating incoming intent.
type Gatekeeper interface {
	Evaluate(ctx context.Context, intent string, extraContext string) (Decision, error)
}

// LLMGatekeeper implements Gatekeeper using an OpenAI-compatible endpoint.
type LLMGatekeeper struct {
	cfg        Config
	httpClient *http.Client
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

	return &LLMGatekeeper{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
	}
}

type openAIChatRequest struct {
	Model       string               `json:"model"`
	Messages    []openAIChatMessage  `json:"messages"`
	Temperature float64              `json:"temperature"`
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

// Evaluate sends the intent and context to the LLM and parses the structured Decision.
func (g *LLMGatekeeper) Evaluate(ctx context.Context, intent string, extraContext string) (Decision, error) {
	if strings.TrimSpace(intent) == "" {
		return Decision{Approved: false, Reason: "intent tidak boleh kosong"}, nil
	}

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
	if g.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+g.cfg.APIKey)
	}

	resp, err := g.httpClient.Do(httpReq)
	if err != nil {
		return Decision{}, fmt.Errorf("execute middleman request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Decision{}, fmt.Errorf("middleman API returned status %d", resp.StatusCode)
	}

	var chatResp openAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return Decision{}, fmt.Errorf("decode middleman response: %w", err)
	}

	if chatResp.Error != nil && chatResp.Error.Message != "" {
		return Decision{}, errors.New(chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return Decision{}, errors.New("empty choices from middleman LLM")
	}

	rawText := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	return ParseDecisionJSON(rawText)
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

	var d Decision
	if err := json.Unmarshal([]byte(clean), &d); err != nil {
		return Decision{}, fmt.Errorf("invalid json decision (%w): %s", err, clean)
	}
	return d, nil
}
