package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestValidateNativeCommand(t *testing.T) {
	workspace := "/var/lib/gpt-go-agent/workspace"
	cases := []struct {
		name    string
		command string
		args    []string
		wantErr bool
	}{
		{name: "echo allowed", command: "echo", args: []string{"OK"}},
		{name: "uptime allowed", command: "uptime"},
		{name: "python denied", command: "python3", args: []string{"-c", "print(1)"}, wantErr: true},
		{name: "shell denied", command: "sh", args: []string{"-c", "id"}, wantErr: true},
		{name: "absolute command denied", command: "/bin/echo", args: []string{"OK"}, wantErr: true},
		{name: "absolute path denied", command: "cat", args: []string{"/etc/passwd"}, wantErr: true},
		{name: "traversal denied", command: "cat", args: []string{"../../etc/passwd"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateNativeCommand(tc.command, tc.args, workspace)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateNativeCommand() err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestWorker_DeterministicNativeGate(t *testing.T) {
	runCount := 0
	gk := &mockGatekeeper{fn: func(ctx context.Context, intent string, extraContext string) (middleman.Decision, error) {
		return middleman.Decision{Approved: true, ExecType: "native", Command: "python3", Args: []string{"-c", "print('blocked')"}}, nil
	}}
	exec := &mockExecutor{nativeFn: func(ctx context.Context, command string, args []string) (string, error) {
		runCount++
		return "should-not-run", nil
	}}
	w := NewWorker(gk, exec, Config{Workers: 1})
	defer w.Close()
	job, err := w.Enqueue("run code", "")
	if err != nil {
		t.Fatal(err)
	}
	var finished *Job
	for i := 0; i < 40; i++ {
		time.Sleep(10 * time.Millisecond)
		j, _ := w.GetJob(job.ID)
		if j != nil && j.Status == StatusFailed {
			finished = j
			break
		}
	}
	if finished == nil {
		t.Fatal("job did not fail")
	}
	if runCount != 0 {
		t.Fatalf("native executor invoked %d times", runCount)
	}
	if !strings.Contains(finished.Error, "not permitted") {
		t.Fatalf("unexpected error: %s", finished.Error)
	}
}

func TestWebhookAuthentication(t *testing.T) {
	gk := &mockGatekeeper{fn: func(ctx context.Context, intent string, extraContext string) (middleman.Decision, error) {
		return middleman.Decision{Approved: true, ExecType: "native", Command: "echo", Args: []string{"AUTH_OK"}}, nil
	}}
	exec := &mockExecutor{nativeFn: func(ctx context.Context, command string, args []string) (string, error) {
		return "AUTH_OK", nil
	}}
	w := NewWorker(gk, exec, Config{Workers: 1, WebhookSecret: "test-secret", RequireWebhookAuth: true})
	defer w.Close()

	h := w.Handler()
	unauth := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(`{"intent":"test"}`))
	unauthRec := httptest.NewRecorder()
	h.ServeHTTP(unauthRec, unauth)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated POST status=%d", unauthRec.Code)
	}

	bad := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(`{"intent":"test"}`))
	bad.Header.Set("Authorization", "Bearer wrong")
	badRec := httptest.NewRecorder()
	h.ServeHTTP(badRec, bad)
	if badRec.Code != http.StatusUnauthorized {
		t.Fatalf("bad-token POST status=%d", badRec.Code)
	}

	good := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(`{"intent":"test"}`))
	good.Header.Set("Authorization", "Bearer test-secret")
	goodRec := httptest.NewRecorder()
	h.ServeHTTP(goodRec, good)
	if goodRec.Code != http.StatusAccepted {
		t.Fatalf("authenticated POST status=%d", goodRec.Code)
	}
}

func TestWebhookIdempotency(t *testing.T) {
	gk := &mockGatekeeper{fn: func(ctx context.Context, intent string, extraContext string) (middleman.Decision, error) {
		return middleman.Decision{Approved: true, ExecType: "native", Command: "echo", Args: []string{"OK"}}, nil
	}}
	w := NewWorker(gk, &mockExecutor{}, Config{Workers: 1})
	defer w.Close()
	h := w.Handler()
	body := bytes.NewBufferString(`{"intent":"same"}`)
	req1 := httptest.NewRequest(http.MethodPost, "/webhook", body)
	req1.Header.Set("Idempotency-Key", "same-key")
	r1 := httptest.NewRecorder()
	h.ServeHTTP(r1, req1)
	if r1.Code != http.StatusAccepted {
		t.Fatalf("first status=%d", r1.Code)
	}
	var a WebhookResponse
	if err := json.NewDecoder(r1.Body).Decode(&a); err != nil {
		t.Fatal(err)
	}
	req2 := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(`{"intent":"same"}`))
	req2.Header.Set("Idempotency-Key", "same-key")
	r2 := httptest.NewRecorder()
	h.ServeHTTP(r2, req2)
	if r2.Code != http.StatusOK {
		t.Fatalf("duplicate status=%d", r2.Code)
	}
	var b WebhookResponse
	if err := json.NewDecoder(r2.Body).Decode(&b); err != nil {
		t.Fatal(err)
	}
	if a.JobID == "" || a.JobID != b.JobID {
		t.Fatalf("idempotency mismatch: %q vs %q", a.JobID, b.JobID)
	}
}

func TestRequestLimiter(t *testing.T) {
	l := &requestLimiter{limit: 2}
	now := time.Now()
	if !l.allow(now) {
		t.Fatal("first request should pass")
	}
	if !l.allow(now) {
		t.Fatal("second request should pass")
	}
	if l.allow(now) {
		t.Fatal("third request should be rejected")
	}
	if !l.allow(now.Add(time.Minute)) {
		t.Fatal("window should reset")
	}
}

func TestWebhookIdempotencyConflict(t *testing.T) {
	gk := &mockGatekeeper{fn: func(ctx context.Context, intent string, extraContext string) (middleman.Decision, error) {
		return middleman.Decision{Approved: true, ExecType: "native", Command: "echo", Args: []string{"OK"}}, nil
	}}
	w := NewWorker(gk, &mockExecutor{}, Config{Workers: 1})
	defer w.Close()
	h := w.Handler()
	first := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(`{"intent":"one"}`))
	first.Header.Set("Idempotency-Key", "same-key")
	r1 := httptest.NewRecorder()
	h.ServeHTTP(r1, first)
	if r1.Code != http.StatusAccepted {
		t.Fatalf("first status=%d", r1.Code)
	}
	second := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(`{"intent":"two"}`))
	second.Header.Set("Idempotency-Key", "same-key")
	r2 := httptest.NewRecorder()
	h.ServeHTTP(r2, second)
	if r2.Code != http.StatusConflict {
		t.Fatalf("conflict status=%d, body=%s", r2.Code, r2.Body.String())
	}
}

type retryableTestError struct{}

func (retryableTestError) Error() string   { return "temporary" }
func (retryableTestError) Timeout() bool   { return true }
func (retryableTestError) Temporary() bool { return true }

func TestWorkerRetriesOnlyGatekeeperFailures(t *testing.T) {
	calls := 0
	gk := &mockGatekeeper{fn: func(ctx context.Context, intent string, extraContext string) (middleman.Decision, error) {
		calls++
		if calls < 2 {
			return middleman.Decision{}, retryableTestError{}
		}
		return middleman.Decision{Approved: true, ExecType: "native", Command: "echo", Args: []string{"OK"}}, nil
	}}
	execCalls := 0
	exec := &mockExecutor{nativeFn: func(ctx context.Context, command string, args []string) (string, error) {
		execCalls++
		return "OK", nil
	}}
	w := NewWorker(gk, exec, Config{Workers: 1, MaxGatekeeperRetries: 2, RetryBaseDelay: time.Millisecond})
	defer w.Close()
	job, err := w.Enqueue("retry", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		time.Sleep(5 * time.Millisecond)
		j, _ := w.GetJob(job.ID)
		if j != nil && j.Status == StatusCompleted {
			if j.RetryCount != 1 || calls != 2 || execCalls != 1 {
				t.Fatalf("job=%+v calls=%d exec=%d", j, calls, execCalls)
			}
			return
		}
	}
	t.Fatal("job did not complete")
}

func TestWorkerDeadLettersFinalGatekeeperFailure(t *testing.T) {
	gk := &mockGatekeeper{fn: func(ctx context.Context, intent string, extraContext string) (middleman.Decision, error) {
		return middleman.Decision{}, retryableTestError{}
	}}
	w := NewWorker(gk, &mockExecutor{}, Config{Workers: 1, MaxGatekeeperRetries: 1, RetryBaseDelay: time.Millisecond})
	defer w.Close()
	job, err := w.Enqueue("dead", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		time.Sleep(5 * time.Millisecond)
		j, _ := w.GetJob(job.ID)
		if j != nil && j.Status == StatusFailed {
			if !j.DeadLettered || j.DeadLetteredAt == nil || j.RetryCount != 1 {
				t.Fatalf("unexpected dead letter state: %+v", j)
			}
			return
		}
	}
	t.Fatal("job did not dead-letter")
}

func TestListJobsPagination(t *testing.T) {
	gk := &mockGatekeeper{fn: func(ctx context.Context, intent string, extraContext string) (middleman.Decision, error) {
		return middleman.Decision{Approved: false, Reason: "test"}, nil
	}}
	w := NewWorker(gk, &mockExecutor{}, Config{Workers: 1})
	defer w.Close()
	for i := 0; i < 3; i++ {
		if _, err := w.Enqueue("job", ""); err != nil {
			t.Fatal(err)
		}
	}
	list, total := w.ListJobs(2, 1)
	if total != 3 || len(list) != 2 {
		t.Fatalf("total=%d len=%d", total, len(list))
	}
}

func TestBoundedBufferCapsOutput(t *testing.T) {
	var b boundedBuffer
	b.limit = 4
	_, _ = b.Write([]byte("abcdefgh"))
	if got := b.String(); !strings.Contains(got, "abcd") || !strings.Contains(got, "output truncated") {
		t.Fatalf("unexpected bounded output %q", got)
	}
}
