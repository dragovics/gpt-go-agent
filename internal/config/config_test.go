package config

import "testing"

func TestDefaultIsLoopback(t *testing.T) {
	c := Default()
	if c.ListenAddr != "127.0.0.1:8787" {
		t.Fatalf("listen addr = %q", c.ListenAddr)
	}
	if c.MaxOutputBytes <= 0 {
		t.Fatal("max output must be positive")
	}
}
