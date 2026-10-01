package webhook

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dragovics/gpt-go-agent/internal/middleman"
)

func TestJobStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	store := NewJobStore(path)
	job := Job{ID: "j1", Intent: "test", Status: StatusCompleted, CreatedAt: time.Now().UTC()}
	if err := store.Put(job); err != nil {
		t.Fatal(err)
	}
	loaded := NewJobStore(path)
	if err := loaded.Load(); err != nil {
		t.Fatal(err)
	}
	jobs := loaded.List()
	if len(jobs) != 1 || jobs[0].ID != "j1" || jobs[0].Status != StatusCompleted {
		t.Fatalf("unexpected persisted jobs: %#v", jobs)
	}
}

func TestWorkerRecoveryMarksInflightFailed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	store := NewJobStore(path)
	created := time.Now().UTC()
	if err := store.Put(Job{ID: "j1", Intent: "inflight", Status: StatusRunning, CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	gk := &mockGatekeeper{fn: func(ctx context.Context, intent string, extraContext string) (middleman.Decision, error) {
		return middleman.Decision{Approved: true, ExecType: "native", Command: "uptime"}, nil
	}}
	exec := &mockExecutor{}
	w := NewWorker(gk, exec, Config{Workers: 1, Store: store})
	defer w.Close()
	job, ok := w.GetJob("j1")
	if !ok {
		t.Fatal("recovered job missing")
	}
	if job.Status != StatusFailed || job.CompletedAt == nil {
		t.Fatalf("expected in-flight job to be failed on recovery: %#v", job)
	}
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	jobs := store.List()
	if len(jobs) != 1 || jobs[0].Status != StatusFailed {
		t.Fatalf("expected persisted failed recovery state: %#v", jobs)
	}
}

func TestJobStoreLoadsBackupWhenPrimaryCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	store := NewJobStore(path)
	job := Job{ID: "j1", Intent: "backup", Status: StatusCompleted, CreatedAt: time.Now().UTC()}
	if err := store.Put(job); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(Job{ID: "j2", Intent: "new", Status: StatusCompleted, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	loaded := NewJobStore(path)
	if err := loaded.Load(); err != nil {
		t.Fatalf("backup recovery failed: %v", err)
	}
	jobs := loaded.List()
	if len(jobs) != 1 || jobs[0].ID != "j1" {
		t.Fatalf("unexpected backup jobs: %#v", jobs)
	}
}

func TestJobStorePruneBefore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	store := NewJobStore(path)
	old := time.Now().UTC().Add(-48 * time.Hour)
	if err := store.Put(Job{ID: "old", Intent: "old", Status: StatusCompleted, CreatedAt: old}); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(Job{ID: "live", Intent: "live", Status: StatusRunning, CreatedAt: old}); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.jobs["old"] = Job{ID: "old", Intent: "old", Status: StatusCompleted, CreatedAt: old, UpdatedAt: old}
	store.jobs["live"] = Job{ID: "live", Intent: "live", Status: StatusRunning, CreatedAt: old, UpdatedAt: old}
	store.mu.Unlock()
	removed, err := store.PruneBefore(time.Now().UTC().Add(-24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "old" {
		t.Fatalf("removed=%v", removed)
	}
	jobs := store.List()
	if len(jobs) != 1 || jobs[0].ID != "live" {
		t.Fatalf("remaining=%#v", jobs)
	}
}

func TestJobStoreLoadsBackupWhenPrimaryMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	store := NewJobStore(path)
	if err := store.Put(Job{ID: "j1", Intent: "first", Status: StatusCompleted, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(Job{ID: "j2", Intent: "second", Status: StatusCompleted, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	loaded := NewJobStore(path)
	if err := loaded.Load(); err != nil {
		t.Fatalf("backup recovery failed: %v", err)
	}
	jobs := loaded.List()
	if len(jobs) != 1 || jobs[0].ID != "j1" {
		t.Fatalf("unexpected backup jobs: %#v", jobs)
	}
}
