package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dragovics/gpt-go-agent/internal/agent"
)

func TestHealthz(t *testing.T) {
	s := httptest.NewServer(New(agent.New("test")).Handler())
	defer s.Close()
	resp, err := http.Get(s.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}
