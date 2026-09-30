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
		wantExec ExecType
		wantErr  bool
	}{
		{
			name:     "clean json approved native",
			input:    `{"approved": true, "reason": "cek ping", "exec_type": "native", "native": {"command": "ping", "args": ["-c", "2", "8.8.8.8"]}}`,
			wantApp:  true,
			wantExec: ExecNative,
			wantErr:  false,
		},
		{
			name: "markdown fenced json approved codex",
			input: "```json\n" +
				`{"approved": true, "reason": "refactor modul", "exec_type": "codex", "command": "refactor auth.go", "args": []}` +
				"\n```",
			wantApp:  true,
			wantExec: ExecCodex,
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
			name:    "approved native missing plan",
			input:   `{"approved": true, "exec_type": "native"}`,
			wantErr: true,
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
				if got.Approved && got.ExecType == ExecNative && got.Native == nil {
					t.Error("approved native decision was not normalized")
				}
				if got.Approved && got.ExecType == ExecCodex && got.Codex == nil {
					t.Error("approved codex decision was not normalized")
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
			ExecType: ExecNative,
			Native:   &NativePlan{Command: "uptime"},
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
	if !dec.Approved || dec.Native == nil || dec.Native.Command != "uptime" || dec.ExecType != ExecNative {
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

func TestLLMGatekeeperRetriesTransientFailures(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			http.Error(w, "temporary", http.StatusServiceUnavailable)
			return
		}
		dec, _ := json.Marshal(Decision{Approved: true, ExecType: ExecNative, Native: &NativePlan{Command: "uptime"}})
		w.Header().Set("Content-Type", "application/json")
		payload := map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(dec)}}}}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer ts.Close()
	gk := New(Config{BaseURL: ts.URL, MaxAttempts: 3, RetryBaseDelay: time.Millisecond})
	got, err := gk.Evaluate(context.Background(), "uptime", "")
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if !got.Approved || calls != 3 {
		t.Fatalf("decision=%+v calls=%d", got, calls)
	}
	m := gk.Metrics()
	if m["middleman_retries_total"] != 2 {
		t.Fatalf("retries=%d", m["middleman_retries_total"])
	}
}

func TestLLMGatekeeperCircuitBreaker(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer ts.Close()
	gk := New(Config{BaseURL: ts.URL, MaxAttempts: 1, CircuitFailureThreshold: 2, CircuitOpenDuration: 30 * time.Millisecond})
	for i := 0; i < 2; i++ {
		if _, err := gk.Evaluate(context.Background(), "x", ""); err == nil {
			t.Fatal("expected failure")
		}
	}
	if _, err := gk.Evaluate(context.Background(), "x", ""); err == nil || !strings.Contains(err.Error(), "circuit breaker") {
		t.Fatalf("expected open breaker, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("breaker did not stop request, calls=%d", calls)
	}
	time.Sleep(40 * time.Millisecond)
	if _, err := gk.Evaluate(context.Background(), "x", ""); err == nil {
		t.Fatal("half-open request should still observe upstream failure")
	}
	if calls != 3 {
		t.Fatalf("half-open call count=%d", calls)
	}
}
