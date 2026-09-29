package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/dragovics/gpt-go-agent/internal/codex"
)

func main() {
	remote := flag.String("remote", os.Getenv("CODEX_REMOTE_URL"), "Codex environment remote URL")
	envID := flag.String("environment-id", os.Getenv("CODEX_ENVIRONMENT_ID"), "Codex environment ID")
	workspace := flag.String("workspace", os.Getenv("CODEX_WORKSPACE"), "optional workspace label")
	flag.Parse()

	if *remote == "" || *envID == "" {
		fmt.Fprintln(os.Stderr, "missing CODEX_REMOTE_URL or CODEX_ENVIRONMENT_ID")
		os.Exit(2)
	}
	if os.Getenv("CODEX_API_KEY") == "" {
		fmt.Fprintln(os.Stderr, "missing CODEX_API_KEY")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	e := codex.ExecServer(*remote, *envID, *workspace)
	cmd, err := e.Start(ctx)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("codex exec-server started (pid=%d)", cmd.Process.Pid)
	if err := cmd.Wait(); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}
