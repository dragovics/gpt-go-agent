package config

import "time"

type Config struct {
	Version string
	ListenAddr string
	Workspace string
	AuditPath string
	CommandTimeout time.Duration
	MaxOutputBytes int
	AllowNetwork bool
}

func Default() Config {
	return Config{Version:"0.1.0", ListenAddr:"127.0.0.1:8787", Workspace:".", AuditPath:"agent-audit.jsonl", CommandTimeout:60*time.Second, MaxOutputBytes:16*1024}
}
