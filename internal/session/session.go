package session

import ("sync"; "time")

type State string
const ( Created State="created"; Pending State="pending"; Active State="active"; Stopped State="stopped"; Failed State="failed" )

type Session struct {
	ID string
	EnvironmentID string
	RemoteURL string
	State State
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Store struct { mu sync.RWMutex; sessions map[string]Session }
func NewStore()*Store{return &Store{sessions:make(map[string]Session)}}
func(s *Store)Put(v Session){s.mu.Lock();defer s.mu.Unlock();v.UpdatedAt=time.Now().UTC();s.sessions[v.ID]=v}
func(s *Store)Get(id string)(Session,bool){s.mu.RLock();defer s.mu.RUnlock();v,ok:=s.sessions[id];return v,ok}
