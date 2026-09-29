package agent

// Agent is the local execution boundary controlled by ChatGPT/Codex.
// The model/reasoning layer intentionally lives outside this process.
type Agent struct {
	Version string
}

func New(version string) *Agent {
	return &Agent{Version: version}
}
