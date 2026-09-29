package codex

import ("context"; "errors"; "os"; "os/exec")

var ErrNotConfigured=errors.New("codex executor is not configured")

type Executor struct { Command string; Args []string }
func NewExecutor(command string,args ...string)*Executor{if command==""{command="codex"};return &Executor{Command:command,Args:args}}
func(e *Executor)Start(ctx context.Context)(*exec.Cmd,error){
	if len(e.Args)==0{return nil,ErrNotConfigured}
	cmd:=exec.CommandContext(ctx,e.Command,e.Args...);cmd.Stdin=os.Stdin;cmd.Stdout=os.Stdout;cmd.Stderr=os.Stderr
	if err:=cmd.Start();err!=nil{return nil,err};return cmd,nil
}
