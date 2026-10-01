package tools

import "context"

type Request struct {
	Action string
	Target string
	Args   map[string]string
}
type Result struct {
	OK     bool
	Output string
	Error  string
}
type Tool interface {
	Name() string
	Writes() bool
	Network() bool
	Call(context.Context, Request) Result
}
