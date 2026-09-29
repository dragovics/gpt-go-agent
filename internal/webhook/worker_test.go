package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dragovics/gpt-go-agent/internal/middleman"
)

type mockGatekeeper struct {
	fn func(ctx context.Context, intent string, extraContext string) (middleman.Decision, error)
}

func (m *mockGatekeeper) Evaluate(ctx context.Context, intent string, extraContext string) (middleman.Decision, error) {
	return m.fn(ctx, intent, extraContext)
}

type mockExecutor struct {
	nativeFn func(ctx context.Context, command string, args []string) (string, error)
	codexFn  func(ctx context.Context, prompt string, targetDir string) (string, error)
}

func (m *mockExecutor) ExecuteNative(ctx context.Context, command string, args []string) (string, error) {
	if m.nativeFn != nil {
		return m.nativeFn(ctx, command, args)
	}
	return "mock native output", nil
}

func (m *mockExecutor) ExecuteCodex(ctx context.Context, prompt string, targetDir string) (string, error) {
	if m.codexFn != nil {
		return m.codexFn(ctx, prompt, targetDir)
	}
	return "mock codex output", nil
}

func TestWorker_NativeExecutionPipeline(t *testing.T) {
	gk := &mockGatekeeper{
		fn: func(ctx context.Context, intent string, extraContext string) (middleman.Decision, error) {
			return middleman.Decision{
				Approved: true,
				ExecType: "native",
				Command:  "echo",
				Args:     []string{"hello", "world"},
			}, nil
		},
	}
	exec := &mockExecutor{
		nativeFn: func(ctx context.Context, command string, args []string) (string, error) {
			return "hello world", nil
		},
	}

	w := NewWorker(gk, exec, Config{Workers: 1})
	defer w.Close()

	job, err := w.Enqueue("print hello world", "test-context")
	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	// Wait for worker to finish
	var finished *Job
	for i := 0; i < 20; i++ {
		time.Sleep(20 * time.Millisecond)
		j, ok := w.GetJob(job.ID)
		if ok && (j.Status == StatusCompleted || j.Status == StatusFailed) {
			finished = j
			break
		}
	}

	if finished == nil {
		t.Fatalf("job did not complete in time")
	}
	if finished.Status != StatusCompleted {
		t.Errorf("job status = %v, want %v (error: %s)", finished.Status, StatusCompleted, finished.Error)
	}
	if finished.Output != "hello world" {
		t.Errorf("job output = %q, want %q", finished.Output, "hello world")
	}
}

func TestWorker_PolicyRejectionPipeline(t *testing.T) {
	gk := &mockGatekeeper{
		fn: func(ctx context.Context, intent string, extraContext string) (middleman.Decision, error) {
			return middleman.Decision{
				Approved: false,
				Reason:   "Dilarang menghapus root folder",
			}, nil
		},
	}
	exec := &mockExecutor{}

	w := NewWorker(gk, exec, Config{Workers: 1})
	defer w.Close()

	job, err := w.Enqueue("rm -rf /", "")
	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	var finished *Job
	for i := 0; i < 20; i++ {
		time.Sleep(20 * time.Millisecond)
		j, ok := w.GetJob(job.ID)
		if ok && j.Status == StatusRejected {
			finished = j
			break
		}
	}

	if finished == nil {
		t.Fatalf("job was not rejected in time")
	}
	if finished.Status != StatusRejected {
		t.Errorf("got status = %v, want %v", finished.Status, StatusRejected)
	}
}

func TestWorker_HTTPHandler(t *testing.T) {
	gk := &mockGatekeeper{
		fn: func(ctx context.Context, intent string, extraContext string) (middleman.Decision, error) {
			return middleman.Decision{
				Approved: true,
				ExecType: "native",
				Command:  "uptime",
			}, nil
		},
	}
	exec := &mockExecutor{
		nativeFn: func(ctx context.Context, command string, args []string) (string, error) {
			return "up 2 hours", nil
		},
	}

	w := NewWorker(gk, exec, Config{Workers: 1})
	defer w.Close()

	handler := w.Handler()

	// 1. Test POST /webhook
	reqBody := `{"intent": "cek uptime server"}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(reqBody))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST /webhook code = %d, want %d", rec.Code, http.StatusAccepted)
	}

	var resp WebhookResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.JobID == "" {
		t.Fatalf("expected non-empty job_id")
	}

	// 2. Poll GET /webhook/{id}
	time.Sleep(50 * time.Millisecond)
	getReq := httptest.NewRequest(http.MethodGet, "/webhook/"+resp.JobID, nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /webhook/{id} code = %d, want 200", getRec.Code)
	}

	var jobResult Job
	if err := json.NewDecoder(getRec.Body).Decode(&jobResult); err != nil {
		t.Fatalf("decode job: %v", err)
	}
	if jobResult.Status != StatusCompleted {
		t.Errorf("job status = %v, want %v", jobResult.Status, StatusCompleted)
	}
	if jobResult.Output != "up 2 hours" {
		t.Errorf("job output = %q, want %q", jobResult.Output, "up 2 hours")
	}
}
