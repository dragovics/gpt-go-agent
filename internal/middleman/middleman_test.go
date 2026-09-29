package middleman

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseDecisionJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantApp  bool
		wantExec string
		wantErr  bool
	}{
		{
			name:     "clean json approved native",
			input:    `{"approved": true, "reason": "cek ping", "exec_type": "native", "command": "ping", "args": ["-c", "2", "8.8.8.8"]}`,
			wantApp:  true,
			wantExec: "native",
			wantErr:  false,
		},
		{
			name: "markdown fenced json approved codex",
			input: "```json\n" +
				`{"approved": true, "reason": "refactor modul", "exec_type": "codex", "command": "refactor auth.go", "args": []}` +
				"\n```",
			wantApp:  true,
			wantExec: "codex",
			wantErr:  false,
		},
		{
			name:     "rejected by policy",
			input:    `{"approved": false, "reason": "dilarang menghapus root directory"}`,
			wantApp:  false,
			wantExec: "",
			wantErr:  false,
		},
		{
			name:    "invalid json",
			input:   `bukan json sama sekali`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDecisionJSON(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseDecisionJSON() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if got.Approved != tt.wantApp {
					t.Errorf("got.Approved = %v, want %v", got.Approved, tt.wantApp)
				}
				if got.ExecType != tt.wantExec {
					t.Errorf("got.ExecType = %v, want %v", got.ExecType, tt.wantExec)
				}
			}
		})
	}
}

func TestLLMGatekeeper_Evaluate(t *testing.T) {
	// Mock server mimicking OpenAI-compatible /v1/chat/completions
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var req openAIChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		// Return decision based on content
		decision := Decision{
			Approved: true,
			Reason:   "Perintah diizinkan",
			ExecType: "native",
			Command:  "uptime",
			Args:     []string{},
		}
		for _, m := range req.Messages {
			if m.Role == "user" && strings.Contains(m.Content, "rm -") {
				decision = Decision{
					Approved: false,
					Reason:   "Violation: destruktif",
				}
			}
		}

		decBytes, _ := json.Marshal(decision)
		resp := openAIChatResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{
				{Message: struct {
					Content string `json:"content"`
				}{Content: string(decBytes)}},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	gk := New(Config{
		BaseURL: ts.URL + "/v1",
		APIKey:  "test-key",
		Model:   "glm-5.3",
		Timeout: 5 * time.Second,
	})

	ctx := context.Background()

	// Case 1: Empty intent
	dec, err := gk.Evaluate(ctx, "", "")
	if err != nil {
		t.Fatalf("unexpected error on empty intent: %v", err)
	}
	if dec.Approved {
		t.Errorf("empty intent should not be approved")
	}

	// Case 2: Safe intent
	dec, err = gk.Evaluate(ctx, "cek uptime server", "vps sgp1")
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if !dec.Approved || dec.Command != "uptime" || dec.ExecType != "native" {
		t.Errorf("unexpected decision for safe intent: %+v", dec)
	}

	// Case 3: Destructive intent
	dec, err = gk.Evaluate(ctx, "rm -rf /", "")
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if dec.Approved {
		t.Errorf("destructive intent should be rejected: %+v", dec)
	}
}
