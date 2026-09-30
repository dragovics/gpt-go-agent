package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordSetsTimestamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit", "events.jsonl")
	logger := New(path)
	before := time.Now().UTC().Add(-time.Second)
	if err := logger.Record(Event{Action: "test", Allowed: true}); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		t.Fatal("missing audit event")
	}
	var event Event
	if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event.Time.IsZero() || event.Time.Before(before) {
		t.Fatalf("invalid timestamp: %s", event.Time)
	}
}
