package policy

type Decision struct {
	Allowed bool
	Reason  string
}
type Policy struct {
	Workspace    string
	AllowWrite   bool
	AllowNetwork bool
}

func (p Policy) Check(action, target string, writes, network bool) Decision {
	if writes && !p.AllowWrite {
		return Decision{Reason: "writes disabled"}
	}
	if network && !p.AllowNetwork {
		return Decision{Reason: "network access disabled"}
	}
	return Decision{Allowed: true}
}
