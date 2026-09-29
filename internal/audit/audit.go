package audit

import ("encoding/json"; "os"; "sync"; "time")

type Event struct {
	Time time.Time `json:"time"`
	Action string `json:"action"`
	Target string `json:"target,omitempty"`
	Allowed bool `json:"allowed"`
	Detail string `json:"detail,omitempty"`
}
type Logger struct { mu sync.Mutex; path string }
func New(path string) *Logger { return &Logger{path:path} }
func (l *Logger) Record(e Event) error {
	l.mu.Lock(); defer l.mu.Unlock()
	e.Time=e.Time.UTC(); b,err:=json.Marshal(e); if err!=nil{return err}
	f,err:=os.OpenFile(l.path,os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600); if err!=nil{return err}; defer f.Close()
	_,err=f.Write(append(b,'
')); return err
}
