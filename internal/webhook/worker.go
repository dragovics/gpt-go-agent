package webhook

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/dragovics/gpt-go-agent/internal/middleman"
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
	ID          string              `json:"id"`
	Intent      string              `json:"intent"`
	Context     string              `json:"context,omitempty"`
	Status      JobStatus           `json:"status"`
	Decision    *middleman.Decision `json:"decision,omitempty"`
	Output      string              `json:"output,omitempty"`
	Error       string              `json:"error,omitempty"`
	CreatedAt   time.Time           `json:"created_at"`
	UpdatedAt   time.Time           `json:"updated_at"`
	CompletedAt *time.Time          `json:"completed_at,omitempty"`
}

// Executor abstracts the execution of native commands and codex tasks.
type Executor interface {
	ExecuteNative(ctx context.Context, command string, args []string) (string, error)
	ExecuteCodex(ctx context.Context, prompt string, targetDir string) (string, error)
}

// DefaultExecutor implements Executor using local os/exec.
type DefaultExecutor struct {
	Workspace string
	CodexBin  string // path to codex CLI
}

// ExecuteNative executes a command locally using os/exec with output capture.
func (e *DefaultExecutor) ExecuteNative(ctx context.Context, command string, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	if e.Workspace != "" {
		cmd.Dir = e.Workspace
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("command %s %v failed (%w): %s", command, args, err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

// ExecuteCodex delegates coding tasks to Codex CLI.
func (e *DefaultExecutor) ExecuteCodex(ctx context.Context, prompt string, targetDir string) (string, error) {
	bin := e.CodexBin
	if bin == "" {
		bin = "codex"
	}
	dir := targetDir
	if dir == "" {
		dir = e.Workspace
	}
	cmd := exec.CommandContext(ctx, bin, "exec", "--sandbox", "workspace-write", "--skip-git-repo-check", "-C", dir, prompt)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("codex execution failed (%w): %s", err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

// Worker coordinates intent evaluation via Middleman and dispatches execution.
type Worker struct {
	gatekeeper middleman.Gatekeeper
	executor   Executor
	mu         sync.RWMutex
	jobs       map[string]*Job
	queue      chan *Job
	workers    int
	timeout    time.Duration
	quit       chan struct{}
}

// Config configures the Webhook Worker pool.
type Config struct {
	Workers int
	Timeout time.Duration
}

// NewWorker creates and starts a Worker pool.
func NewWorker(gk middleman.Gatekeeper, exec Executor, cfg Config) *Worker {
	if cfg.Workers <= 0 {
		cfg.Workers = 2
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 180 * time.Second
	}
	w := &Worker{
		gatekeeper: gk,
		executor:   exec,
		jobs:       make(map[string]*Job),
		queue:      make(chan *Job, 100),
		workers:    cfg.Workers,
		timeout:    cfg.Timeout,
		quit:       make(chan struct{}),
	}

	for i := 0; i < w.workers; i++ {
		go w.runLoop()
	}

	return w
}

func (w *Worker) runLoop() {
	for {
		select {
		case <-w.quit:
			return
		case job, ok := <-w.queue:
			if !ok {
				return
			}
			w.processJob(job)
		}
	}
}

// Enqueue registers a job and queues it for background execution.
func (w *Worker) Enqueue(intent string, extraContext string) (*Job, error) {
	if strings.TrimSpace(intent) == "" {
		return nil, errors.New("intent cannot be empty")
	}

	w.mu.Lock()
	id := fmt.Sprintf("job-%d-%d", time.Now().UnixNano(), len(w.jobs)+1)
	job := &Job{
		ID:        id,
		Intent:    intent,
		Context:   extraContext,
		Status:    StatusPending,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	w.jobs[id] = job
	w.mu.Unlock()

	select {
	case w.queue <- job:
		return job, nil
	default:
		w.mu.Lock()
		job.Status = StatusFailed
		job.Error = "queue is full"
		w.mu.Unlock()
		return nil, errors.New("webhook worker queue is full")
	}
}

// GetJob retrieves a job by ID.
func (w *Worker) GetJob(id string) (*Job, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	j, ok := w.jobs[id]
	if !ok {
		return nil, false
	}
	// Return shallow copy
	cp := *j
	return &cp, true
}

func (w *Worker) updateJob(id string, fn func(*Job)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if j, ok := w.jobs[id]; ok {
		fn(j)
		j.UpdatedAt = time.Now().UTC()
	}
}

// processJob performs the evaluation -> policy enforcement -> execution pipeline.
func (w *Worker) processJob(job *Job) {
	ctx, cancel := context.WithTimeout(context.Background(), w.timeout)
	defer cancel()

	w.updateJob(job.ID, func(j *Job) { j.Status = StatusEvaluating })

	// Step 1: Evaluate intent via Middleman Gatekeeper
	dec, err := w.gatekeeper.Evaluate(ctx, job.Intent, job.Context)
	if err != nil {
		w.updateJob(job.ID, func(j *Job) {
			j.Status = StatusFailed
			j.Error = fmt.Sprintf("gatekeeper evaluation error: %v", err)
			now := time.Now().UTC()
			j.CompletedAt = &now
		})
		return
	}

	w.updateJob(job.ID, func(j *Job) { j.Decision = &dec })

	// Step 2: Policy Enforcement (Satpam) Check
	if !dec.Approved {
		w.updateJob(job.ID, func(j *Job) {
			j.Status = StatusRejected
			j.Error = fmt.Sprintf("policy rejected: %s", dec.Reason)
			now := time.Now().UTC()
			j.CompletedAt = &now
		})
		return
	}

	// Step 3: Execution Dispatch (Mandor technical translation)
	w.updateJob(job.ID, func(j *Job) { j.Status = StatusRunning })

	var out string
	var execErr error

	switch dec.ExecType {
	case "codex":
		out, execErr = w.executor.ExecuteCodex(ctx, dec.Command, "")
	default:
		// Default to native execution ($0 token, fast)
		out, execErr = w.executor.ExecuteNative(ctx, dec.Command, dec.Args)
	}

	now := time.Now().UTC()
	w.updateJob(job.ID, func(j *Job) {
		j.CompletedAt = &now
		j.Output = out
		if execErr != nil {
			j.Status = StatusFailed
			j.Error = execErr.Error()
		} else {
			j.Status = StatusCompleted
		}
	})
}

// Close gracefully stops the worker pool.
func (w *Worker) Close() {
	close(w.quit)
}
