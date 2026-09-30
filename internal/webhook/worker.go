package webhook

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dragovics/gpt-go-agent/internal/audit"
	"github.com/dragovics/gpt-go-agent/internal/middleman"
	"github.com/dragovics/gpt-go-agent/internal/security"
)

// JobStatus represents the lifecycle state of a webhook job.
type JobStatus string

const (
	StatusPending    JobStatus = "pending"
	StatusEvaluating JobStatus = "evaluating"
	StatusRejected   JobStatus = "rejected"
	StatusRunning    JobStatus = "running"
	StatusCompleted  JobStatus = "completed"
	StatusFailed     JobStatus = "failed"
)

// Job represents a single webhook task unit.
type Job struct {
	ID             string              `json:"id"`
	Intent         string              `json:"intent"`
	Context        string              `json:"context,omitempty"`
	Status         JobStatus           `json:"status"`
	Decision       *middleman.Decision `json:"decision,omitempty"`
	Output         string              `json:"output,omitempty"`
	Error          string              `json:"error,omitempty"`
	CreatedAt      time.Time           `json:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at"`
	CompletedAt    *time.Time          `json:"completed_at,omitempty"`
	IdempotencyKey string              `json:"idempotency_key,omitempty"`
	RequestHash    string              `json:"request_hash,omitempty"`
	CorrelationID  string              `json:"correlation_id,omitempty"`
	RetryCount     int                 `json:"retry_count,omitempty"`
	DeadLettered   bool                `json:"dead_lettered,omitempty"`
	DeadLetteredAt *time.Time          `json:"dead_lettered_at,omitempty"`
	DurationMS     int64               `json:"duration_ms,omitempty"`
}

// Executor abstracts the execution of native commands and codex tasks.
type Executor interface {
	ExecuteNative(ctx context.Context, command string, args []string) (string, error)
	ExecuteCodex(ctx context.Context, prompt string, targetDir string) (string, error)
}

// DefaultExecutor implements Executor using local os/exec.
type DefaultExecutor struct {
	Workspace       string
	CodexBin        string // path to codex CLI
	MaxOutputBytes  int
	CodexEnabled    bool
	CodexAllowWrite bool
}

type boundedBuffer struct {
	mu        sync.Mutex
	buf       []byte
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit <= 0 {
		b.limit = 64 * 1024
	}
	remaining := b.limit - len(b.buf)
	if remaining > 0 {
		n := len(p)
		if n > remaining {
			n = remaining
			b.truncated = true
		}
		b.buf = append(b.buf, p[:n]...)
	} else {
		b.truncated = true
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := string(b.buf)
	if b.truncated {
		out += "\n[output truncated]"
	}
	return out
}

// ExecuteNative executes a command locally using os/exec with output capture.
func (e *DefaultExecutor) ExecuteNative(ctx context.Context, command string, args []string) (string, error) {
	// #nosec G204 -- native commands pass the deterministic validateNativeCommand gate before execution.
	cmd := exec.CommandContext(ctx, command, args...)
	security.ConfigureProcessGroup(cmd)
	if e.Workspace != "" {
		cmd.Dir = e.Workspace
	}
	cmd.Env = security.MinimalEnvironment(os.Environ())
	var out boundedBuffer
	out.limit = e.MaxOutputBytes
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	result := strings.TrimSpace(out.String())
	if err != nil {
		return result, fmt.Errorf("command %s %v failed (%w): %s", command, args, err, result)
	}
	return result, nil
}

// ExecuteCodex delegates coding tasks to Codex CLI.
func validateCodexPrompt(prompt, workspace, targetDir string) error {
	if strings.TrimSpace(prompt) == "" {
		return errors.New("codex prompt cannot be empty")
	}
	if len(prompt) > 32768 {
		return errors.New("codex prompt too large")
	}
	if strings.IndexByte(prompt, 0) >= 0 {
		return errors.New("codex prompt contains NUL")
	}
	if strings.TrimSpace(workspace) == "" && strings.TrimSpace(targetDir) == "" {
		return errors.New("codex workspace is required")
	}
	return nil
}

func (e *DefaultExecutor) ExecuteCodex(ctx context.Context, prompt string, targetDir string) (string, error) {
	if !e.CodexEnabled {
		return "", errors.New("codex execution is disabled")
	}
	if err := validateCodexPrompt(prompt, e.Workspace, targetDir); err != nil {
		return "", err
	}
	bin := e.CodexBin
	if bin == "" {
		bin = "codex"
	}
	dir := targetDir
	if dir == "" {
		dir = e.Workspace
	}
	sandbox := "read-only"
	if e.CodexAllowWrite {
		sandbox = "workspace-write"
	}
	// #nosec G204 -- Codex binary is service configuration and sandbox mode is explicit service policy.
	cmd := exec.CommandContext(ctx, bin, "exec", "--sandbox", sandbox, "--skip-git-repo-check", "-C", dir, prompt)
	security.ConfigureProcessGroup(cmd)
	cmd.Env = security.ChildEnvironment(os.Environ())
	var out boundedBuffer
	out.limit = e.MaxOutputBytes
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	result := strings.TrimSpace(out.String())
	if err != nil {
		return result, fmt.Errorf("codex execution failed (%w): %s", err, result)
	}
	return result, nil
}

// Worker coordinates intent evaluation via Middleman and dispatches execution.
type Worker struct {
	planner            middleman.Planner
	executor           Executor
	store              *JobStore
	webhookSecret      string
	requireWebhookAuth bool
	mu                 sync.RWMutex
	jobs               map[string]*Job
	queue              chan *Job
	workers            int
	quit               chan struct{}
	idempotency        map[string]string
	retention          time.Duration
	cleanupInterval    time.Duration
	maxPlannerRetry    int
	retryBaseDelay     time.Duration
	auditor            *audit.Logger
	maxOutputBytes     int
	jobTimeout         time.Duration
	maxJobs            int
	rateLimitPerMinute int
	storageHealthy     atomic.Bool
	ctx                context.Context
	cancel             context.CancelFunc
	wg                 sync.WaitGroup
	closeOnce          sync.Once
}

// Config configures the Webhook Worker pool.
type Config struct {
	Workers              int
	QueueSize            int
	MaxJobs              int
	RateLimitPerMinute   int
	JobTimeout           time.Duration
	Store                *JobStore
	WebhookSecret        string
	RequireWebhookAuth   bool
	Retention            time.Duration
	CleanupInterval      time.Duration
	MaxGatekeeperRetries int
	RetryBaseDelay       time.Duration
	Auditor              *audit.Logger
	MaxOutputBytes       int
}

// NewWorker creates and starts a Worker pool.
func NewWorker(planner middleman.Planner, exec Executor, cfg Config) (*Worker, error) {
	if planner == nil {
		return nil, errors.New("planner is required")
	}
	if exec == nil {
		return nil, errors.New("executor is required")
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 2
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 100
	}
	if cfg.MaxJobs <= 0 {
		cfg.MaxJobs = 10_000
	}
	if cfg.RateLimitPerMinute <= 0 {
		cfg.RateLimitPerMinute = 120
	}
	if cfg.JobTimeout <= 0 {
		cfg.JobTimeout = 180 * time.Second
	}
	if cfg.Retention <= 0 {
		cfg.Retention = 7 * 24 * time.Hour
	}
	if cfg.CleanupInterval <= 0 {
		cfg.CleanupInterval = time.Hour
	}
	if cfg.MaxGatekeeperRetries < 0 {
		cfg.MaxGatekeeperRetries = 0
	}
	if cfg.RetryBaseDelay <= 0 {
		cfg.RetryBaseDelay = 500 * time.Millisecond
	}
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = 64 * 1024
	}

	workerCtx, workerCancel := context.WithCancel(context.Background())
	w := &Worker{
		planner:            planner,
		executor:           exec,
		store:              cfg.Store,
		webhookSecret:      cfg.WebhookSecret,
		requireWebhookAuth: cfg.RequireWebhookAuth,
		jobs:               make(map[string]*Job),
		queue:              make(chan *Job, cfg.QueueSize),
		workers:            cfg.Workers,
		quit:               make(chan struct{}),
		idempotency:        make(map[string]string),
		retention:          cfg.Retention,
		cleanupInterval:    cfg.CleanupInterval,
		maxPlannerRetry:    cfg.MaxGatekeeperRetries,
		retryBaseDelay:     cfg.RetryBaseDelay,
		auditor:            cfg.Auditor,
		maxOutputBytes:     cfg.MaxOutputBytes,
		jobTimeout:         cfg.JobTimeout,
		maxJobs:            cfg.MaxJobs,
		rateLimitPerMinute: cfg.RateLimitPerMinute,
		ctx:                workerCtx,
		cancel:             workerCancel,
	}
	w.storageHealthy.Store(true)

	if w.store != nil {
		if err := w.store.Load(); err != nil {
			workerCancel()
			return nil, fmt.Errorf("load webhook job store: %w", err)
		}
		for _, stored := range w.store.List() {
			job := stored
			if job.RequestHash == "" {
				job.RequestHash = requestHash(job.Intent, job.Context)
			}
			w.jobs[job.ID] = &job
			if job.IdempotencyKey != "" {
				w.idempotency[job.IdempotencyKey] = job.ID
			}
		}
	}

	w.recoverJobs()

	for i := 0; i < w.workers; i++ {
		w.wg.Add(1)
		go w.runLoop()
	}
	w.wg.Add(1)
	go w.cleanupLoop()
	return w, nil
}

func (w *Worker) Ready() bool {
	return w.storageHealthy.Load()
}

func (w *Worker) cleanupLoop() {
	defer w.wg.Done()
	ticker := time.NewTicker(w.cleanupInterval)
	defer ticker.Stop()
	w.cleanup()
	for {
		select {
		case <-w.quit:
			return
		case <-ticker.C:
			w.cleanup()
		}
	}
}

func (w *Worker) cleanup() {
	if w.store == nil || w.retention <= 0 {
		return
	}
	ids, err := w.store.PruneBefore(time.Now().UTC().Add(-w.retention))
	if err != nil {
		w.storageHealthy.Store(false)
		log.Printf("webhook retention cleanup: %v", err)
		return
	}
	w.storageHealthy.Store(true)
	if len(ids) == 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, id := range ids {
		if job, ok := w.jobs[id]; ok {
			if job.IdempotencyKey != "" {
				delete(w.idempotency, job.IdempotencyKey)
			}
			delete(w.jobs, id)
		}
	}
}

func (w *Worker) recoverJobs() {
	w.mu.Lock()
	var retry []*Job
	for _, job := range w.jobs {
		switch job.Status {
		case StatusPending:
			retry = append(retry, job)
		case StatusEvaluating, StatusRunning:
			now := time.Now().UTC()
			job.Status = StatusFailed
			job.Error = "worker restarted while job was in-flight; manual retry required"
			job.CompletedAt = &now
			job.UpdatedAt = now
			if w.store != nil {
				if err := w.store.Put(*job); err != nil {
					w.storageHealthy.Store(false)
					log.Printf("webhook store recovery persist %s: %v", job.ID, err)
				} else {
					w.storageHealthy.Store(true)
				}
			}
		}
	}
	w.mu.Unlock()

	for _, job := range retry {
		queuedJob := *job
		select {
		case w.queue <- &queuedJob:
		default:
			if err := w.updateJob(job.ID, func(j *Job) {
				j.Status = StatusFailed
				j.Error = "recovery queue is full"
			}); err != nil {
				log.Printf("webhook recovery queue failure persist %s: %v", job.ID, err)
			}
		}
	}
}

func (w *Worker) runLoop() {
	defer w.wg.Done()
	for {
		select {
		case <-w.quit:
			return
		case job, ok := <-w.queue:
			if !ok {
				return
			}
			copyJob := *job
			w.processJob(&copyJob)
		}
	}
}

func requestHash(intent, extraContext string) string {
	sum := sha256.Sum256([]byte(intent + "\x00" + extraContext))
	return fmt.Sprintf("%x", sum[:])
}

func randomID(prefix string) string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err == nil {
		return prefix + hex.EncodeToString(buf)
	}
	return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
}

// Enqueue registers a job and queues it for background execution.
func (w *Worker) Enqueue(intent string, extraContext string) (*Job, error) {
	job, _, err := w.EnqueueWithKeyAndCorrelation(intent, extraContext, "", "")
	return job, err
}

func (w *Worker) EnqueueWithKey(intent string, extraContext string, key string) (*Job, bool, error) {
	return w.EnqueueWithKeyAndCorrelation(intent, extraContext, key, "")
}

func (w *Worker) EnqueueWithKeyAndCorrelation(intent string, extraContext string, key string, correlationID string) (*Job, bool, error) {
	intent = strings.TrimSpace(intent)
	if intent == "" {
		return nil, false, fmt.Errorf("%w: intent cannot be empty", ErrInvalidInput)
	}
	if len(intent) > 32768 || len(extraContext) > 32768 {
		return nil, false, fmt.Errorf("%w: request too large", ErrInvalidInput)
	}
	key = strings.TrimSpace(key)
	if len(key) > 256 {
		return nil, false, fmt.Errorf("%w: idempotency key too long", ErrInvalidInput)
	}
	hash := requestHash(intent, extraContext)
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		correlationID = randomID("corr-")
	}
	if len(correlationID) > 128 {
		return nil, false, fmt.Errorf("%w: correlation id too long", ErrInvalidInput)
	}

	w.mu.Lock()
	if key != "" {
		if existingID, ok := w.idempotency[key]; ok {
			if existing, ok := w.jobs[existingID]; ok {
				if existing.RequestHash != "" && existing.RequestHash != hash {
					w.mu.Unlock()
					return nil, false, fmt.Errorf("%w: key is already bound to different request content", ErrIdempotencyConflict)
				}
				cp := cloneJob(existing)
				w.mu.Unlock()
				return cp, true, nil
			}
		}
	}
	if len(w.jobs) >= w.maxJobs {
		w.mu.Unlock()
		return nil, false, ErrJobCapacity
	}

	now := time.Now().UTC()
	job := &Job{
		ID:             randomID("job-"),
		Intent:         intent,
		Context:        extraContext,
		IdempotencyKey: key,
		RequestHash:    hash,
		CorrelationID:  correlationID,
		Status:         StatusPending,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if w.store != nil {
		if err := w.store.Put(*job); err != nil {
			w.storageHealthy.Store(false)
			w.mu.Unlock()
			return nil, false, fmt.Errorf("persist webhook job: %w", err)
		}
		w.storageHealthy.Store(true)
	}
	w.jobs[job.ID] = job
	if key != "" {
		w.idempotency[key] = job.ID
	}
	queuedJob := cloneJob(job)
	w.mu.Unlock()

	select {
	case w.queue <- queuedJob:
		w.recordAudit(*queuedJob, "webhook.enqueue", true, "accepted", 0)
		return queuedJob, false, nil
	default:
		_ = w.updateJob(job.ID, func(j *Job) {
			j.Status = StatusFailed
			j.Error = "queue is full"
			t := time.Now().UTC()
			j.CompletedAt = &t
		})
		return nil, false, ErrQueueFull
	}
}

func (w *Worker) ListJobs(limit, offset int) ([]*Job, int) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	w.mu.RLock()
	all := make([]*Job, 0, len(w.jobs))
	for _, j := range w.jobs {
		all = append(all, cloneJob(j))
	}
	w.mu.RUnlock()
	sort.Slice(all, func(i, j int) bool {
		if all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].ID > all[j].ID
		}
		return all[i].CreatedAt.After(all[j].CreatedAt)
	})
	total := len(all)
	if offset >= total {
		return []*Job{}, total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return all[offset:end], total
}

// GetJob retrieves a job by ID.
func (w *Worker) GetJob(id string) (*Job, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	j, ok := w.jobs[id]
	if !ok {
		return nil, false
	}
	return cloneJob(j), true
}

func cloneJob(j *Job) *Job {
	if j == nil {
		return nil
	}
	cp := *j
	if j.Decision != nil {
		decision := *j.Decision
		if j.Decision.Native != nil {
			native := *j.Decision.Native
			native.Args = append([]string(nil), j.Decision.Native.Args...)
			decision.Native = &native
		}
		if j.Decision.Codex != nil {
			codex := *j.Decision.Codex
			decision.Codex = &codex
		}
		cp.Decision = &decision
	}
	return &cp
}

// updateJob persists the new state before publishing it in memory. This keeps
// the visible state from getting ahead of durable state on storage failures.
func (w *Worker) updateJob(id string, fn func(*Job)) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	current, ok := w.jobs[id]
	if !ok {
		return errors.New("job not found")
	}
	next := cloneJob(current)
	fn(next)
	next.UpdatedAt = time.Now().UTC()
	if w.store != nil {
		if err := w.store.Put(*next); err != nil {
			w.storageHealthy.Store(false)
			return fmt.Errorf("persist job %s: %w", id, err)
		}
		w.storageHealthy.Store(true)
	}
	w.jobs[id] = next
	return nil
}

func (w *Worker) recordAudit(job Job, action string, allowed bool, detail string, duration time.Duration) {
	if w.auditor == nil {
		return
	}
	if err := w.auditor.Record(audit.Event{Action: action, Target: job.ID, Allowed: allowed, Detail: detail, CorrelationID: job.CorrelationID, DurationMS: duration.Milliseconds()}); err != nil {
		log.Printf("webhook audit %s: %v", job.ID, err)
	}
}

func (w *Worker) failJob(id string, err error, deadLetter bool) {
	now := time.Now().UTC()
	if persistErr := w.updateJob(id, func(j *Job) {
		j.Status = StatusFailed
		j.Error = err.Error()
		j.CompletedAt = &now
		if deadLetter {
			j.DeadLettered = true
			j.DeadLetteredAt = &now
		}
	}); persistErr != nil {
		log.Printf("webhook fail state persist %s: %v", id, persistErr)
	}
	if job, ok := w.GetJob(id); ok {
		w.recordAudit(*job, "webhook.failure", false, err.Error(), time.Since(job.CreatedAt))
	}
}

func (w *Worker) startRetry(jobID string, delay time.Duration) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.scheduleRetry(jobID, delay)
	}()
}

func (w *Worker) scheduleRetry(jobID string, delay time.Duration) {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-w.quit:
		return
	}
	job, ok := w.GetJob(jobID)
	if !ok || job.Status != StatusPending {
		return
	}
	select {
	case w.queue <- job:
	case <-w.quit:
	case <-time.After(2 * time.Second):
		w.failJob(jobID, errors.New("retry queue unavailable"), true)
	}
}

// processJob performs the evaluation -> policy enforcement -> execution pipeline.
func (w *Worker) processJob(job *Job) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(w.ctx, w.jobTimeout)
	defer cancel()

	if err := w.updateJob(job.ID, func(j *Job) { j.Status = StatusEvaluating }); err != nil {
		log.Printf("webhook refusing to evaluate %s without durable state: %v", job.ID, err)
		return
	}

	// Step 1: Evaluate intent via Middleman planner
	dec, err := w.planner.Evaluate(ctx, job.Intent, job.Context)
	if err != nil {
		if middleman.IsRetryableError(err) && job.RetryCount < w.maxPlannerRetry {
			attempt := job.RetryCount + 1
			if persistErr := w.updateJob(job.ID, func(j *Job) {
				j.Status = StatusPending
				j.RetryCount = attempt
				j.Error = fmt.Sprintf("planner retry %d/%d: %v", attempt, w.maxPlannerRetry, err)
			}); persistErr != nil {
				log.Printf("webhook retry state persist %s: %v", job.ID, persistErr)
				return
			}
			delay := w.retryBaseDelay * time.Duration(1<<(attempt-1))
			if delay > 10*time.Second {
				delay = 10 * time.Second
			}
			delay = middleman.RetryDelay(err, delay)
			w.startRetry(job.ID, delay)
			return
		}
		w.failJob(job.ID, fmt.Errorf("planner evaluation error: %w", err), true)
		return
	}

	if err := w.updateJob(job.ID, func(j *Job) { j.Decision = &dec }); err != nil {
		log.Printf("webhook refusing to execute %s without durable decision: %v", job.ID, err)
		return
	}
	if current, ok := w.GetJob(job.ID); ok {
		w.recordAudit(*current, "webhook.policy", dec.Approved, dec.Reason, time.Since(started))
	}

	// Step 2: The planner can reject intent, but approval is not authorization.
	// Local deterministic policy still gates every executable action.
	if !dec.Approved {
		_ = w.updateJob(job.ID, func(j *Job) {
			j.Status = StatusRejected
			j.Error = fmt.Sprintf("planner rejected: %s", dec.Reason)
			now := time.Now().UTC()
			j.CompletedAt = &now
		})
		return
	}

	if err := w.updateJob(job.ID, func(j *Job) { j.Status = StatusRunning }); err != nil {
		log.Printf("webhook refusing to execute %s without durable running state: %v", job.ID, err)
		return
	}

	var out string
	var execErr error

	switch dec.ExecType {
	case middleman.ExecCodex:
		if dec.Codex == nil || strings.TrimSpace(dec.Codex.Prompt) == "" {
			w.failJob(job.ID, errors.New("execution denied: codex plan is missing"), false)
			return
		}
		out, execErr = w.executor.ExecuteCodex(ctx, dec.Codex.Prompt, "")
	case middleman.ExecNative:
		if dec.Native == nil {
			w.failJob(job.ID, errors.New("execution denied: native plan is missing"), false)
			return
		}
		if err := validateNativeCommand(dec.Native.Command, dec.Native.Args, ""); err != nil {
			w.failJob(job.ID, errors.New("execution denied: "+err.Error()), false)
			return
		}
		out, execErr = w.executor.ExecuteNative(ctx, dec.Native.Command, dec.Native.Args)
	default:
		w.failJob(job.ID, fmt.Errorf("execution denied: unknown exec_type %q", dec.ExecType), false)
		return
	}

	out = boundedText(out, w.maxOutputBytes)
	if execErr != nil {
		execErr = errors.New(boundedText(execErr.Error(), w.maxOutputBytes))
	}

	now := time.Now().UTC()
	duration := time.Since(started)
	if err := w.updateJob(job.ID, func(j *Job) {
		j.CompletedAt = &now
		j.DurationMS = duration.Milliseconds()
		j.Output = out
		if execErr != nil {
			j.Status = StatusFailed
			j.Error = execErr.Error()
			j.DeadLettered = true
			j.DeadLetteredAt = &now
		} else {
			j.Status = StatusCompleted
		}
	}); err != nil {
		log.Printf("webhook final state persist %s: %v", job.ID, err)
	}
	if final, ok := w.GetJob(job.ID); ok {
		w.recordAudit(*final, "webhook.execute", execErr == nil, final.Error, duration)
	}
}

func boundedText(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "\n[output truncated]"
}

// Close gracefully stops the worker pool.
func (w *Worker) Close() {
	w.closeOnce.Do(func() {
		w.cancel()
		close(w.quit)
	})
	w.wg.Wait()
}
