package webhook

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
)

type Journal struct {
	mu sync.Mutex
	path string
}

type journalRecord struct {
	Job Job `json:"job"`
	Done bool `json:"done"`
	Dead bool `json:"dead"`
	Error string `json:"error,omitempty"`
}

func NewJournal(path string) *Journal { return &Journal{path:path} }

func (j *Journal) append(r journalRecord) error {
	j.mu.Lock(); defer j.mu.Unlock()
	f, err := os.OpenFile(j.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil { return err }
	defer f.Close()
	b, err := json.Marshal(r); if err != nil { return err }
	_, err = f.Write(append(b,'
'))
	return err
}

func (j *Journal) Enqueue(job Job) error { return j.append(journalRecord{Job:job}) }
func (j *Journal) Done(job Job) error { return j.append(journalRecord{Job:job, Done:true}) }
func (j *Journal) Dead(job Job, err error) error { msg := ""; if err != nil { msg = err.Error() }; return j.append(journalRecord{Job:job, Dead:true, Error:msg}) }

func (j *Journal) Pending() ([]Job,error) {
	f, err := os.Open(j.path)
	if os.IsNotExist(err) { return nil,nil }
	if err != nil { return nil,err }
	defer f.Close()
	pending := map[string]Job{}
	done := map[string]bool{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		var r journalRecord
		if json.Unmarshal(s.Bytes(), &r) != nil || r.Job.ID == "" { continue }
		if r.Done || r.Dead { done[r.Job.ID]=true; delete(pending,r.Job.ID) } else if !done[r.Job.ID] { pending[r.Job.ID]=r.Job }
	}
	if err:=s.Err(); err!=nil{return nil,err}
	out:=make([]Job,0,len(pending)); for _,v:=range pending { out=append(out,v) }
	return out,nil
}
